package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/input"
	"github.com/lavr/express-botx/internal/queue"
)

func runEnqueue(args []string, deps Deps) error {
	fs := flag.NewFlagSet("enqueue", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var routingMode string
	var from string
	var filePath string
	var fileName string
	var status string
	var silent bool
	var stealth bool
	var forceDND bool
	var noNotify bool
	var metadata string

	globalFlags(fs, &flags)
	fs.StringVar(&flags.ChatID, "chat-id", "", "target chat UUID or alias")
	fs.StringVar(&routingMode, "routing-mode", "", "routing mode: direct, catalog, or mixed (default: mixed)")
	fs.StringVar(&from, "body-from", "", "read message text from file")
	fs.StringVar(&filePath, "file", "", "path to file to attach (or - for stdin)")
	fs.StringVar(&fileName, "file-name", "", "file name (required when --file -)")
	fs.StringVar(&status, "status", "ok", "notification status: ok or error")
	fs.BoolVar(&silent, "silent", false, "no push notification to recipient")
	fs.BoolVar(&stealth, "stealth", false, "stealth mode (message visible only to bot)")
	fs.BoolVar(&forceDND, "force-dnd", false, "deliver even if recipient has DND")
	fs.BoolVar(&noNotify, "no-notify", false, "do not send notification at all")
	fs.StringVar(&metadata, "metadata", "", "arbitrary JSON for notification.metadata")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, `Usage: express-botx enqueue [options] [message]

Enqueue a message for async delivery via broker.
The message is published to the work queue instead of being sent directly.

Message sources (in priority order):
  --body-from FILE   Read message from file
  [message]          Positional argument
  stdin              Pipe input (auto-detected)

Routing modes:
  direct    Requires --bot-id and --chat-id (UUIDs). No catalog lookup.
  catalog   Resolves --bot and --chat-id aliases via local catalog cache.
  mixed     Uses direct if UUIDs provided, otherwise falls back to catalog.

Options:
`)
		fs.PrintDefaults()
	}

	if hasHelpFlag(args) {
		fs.Usage()
		return nil
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	applyVerboseFlag(flags)

	cfg, err := config.LoadForEnqueue(flags)
	if err != nil {
		return err
	}

	// Override routing mode from flag
	if routingMode != "" {
		if err := config.ValidateRoutingMode(routingMode); err != nil {
			return err
		}
		cfg.Producer.RoutingMode = routingMode
	}

	// Validate status
	if status != "ok" && status != "error" {
		return fmt.Errorf("--status must be ok or error, got %q", status)
	}

	// Determine effective routing mode and validate requirements
	mode := cfg.Producer.RoutingMode
	botID := cfg.BotID
	botAlias := flags.Bot // --bot flag: alias for catalog resolution (not resolved via config)
	chatID := cfg.ChatID

	// Observability fields for work message
	var routeHost, routeBotName, routeChatAlias, routeCatalogRevision string

	switch config.RoutingMode(mode) {
	case config.RoutingDirect:
		if botID == "" {
			return fmt.Errorf("--bot-id is required for direct routing mode")
		}
		if !config.IsUUID(botID) {
			return fmt.Errorf("--bot-id must be a valid UUID for direct routing mode, got %q", botID)
		}
		if chatID == "" {
			return fmt.Errorf("--chat-id is required for direct routing mode")
		}
		if !config.IsUUID(chatID) {
			return fmt.Errorf("--chat-id must be a valid UUID for direct routing mode, got %q; use catalog or mixed mode for alias resolution", chatID)
		}
	case config.RoutingMixed:
		// Mixed: use direct if bot_id and chat_id are both provided and both
		// look like UUIDs. Otherwise fall back to catalog for alias resolution.
		if botID != "" && chatID != "" && config.IsUUID(botID) && config.IsUUID(chatID) {
			// Direct path — no catalog needed
		} else {
			// Need catalog for alias resolution
			resolved, err := resolveViaCatalog(cfg, botID, botAlias, chatID)
			if err != nil {
				return err
			}
			botID = resolved.BotID
			chatID = resolved.ChatID
			routeHost = resolved.Host
			routeBotName = resolved.BotName
			routeChatAlias = resolved.ChatAlias
			routeCatalogRevision = resolved.CatalogRevision
		}
	case config.RoutingCatalog:
		resolved, err := resolveViaCatalog(cfg, botID, botAlias, chatID)
		if err != nil {
			return err
		}
		botID = resolved.BotID
		chatID = resolved.ChatID
		routeHost = resolved.Host
		routeBotName = resolved.BotName
		routeChatAlias = resolved.ChatAlias
		routeCatalogRevision = resolved.CatalogRevision
	}

	// Read file attachment if requested
	var fileAttachment *botapi.SendFile
	if filePath != "" {
		var data []byte
		var name string

		if filePath == "-" {
			if fileName == "" {
				return fmt.Errorf("--file-name is required when using --file -")
			}
			data, err = io.ReadAll(deps.Stdin)
			if err != nil {
				return fmt.Errorf("reading file from stdin: %w", err)
			}
			if len(data) == 0 {
				return fmt.Errorf("empty file from stdin")
			}
			name = fileName
		} else {
			data, err = os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("reading file %q: %w", filePath, err)
			}
			name = filepath.Base(filePath)
			if fileName != "" {
				name = fileName
			}
		}

		// Enforce max_file_size limit
		maxFileSize, parseErr := config.ParseFileSize(cfg.Queue.MaxFileSize)
		if parseErr != nil {
			return fmt.Errorf("invalid queue.max_file_size %q: %w", cfg.Queue.MaxFileSize, parseErr)
		}
		if maxFileSize == 0 {
			maxFileSize = 1024 * 1024 // default: 1 MiB
		}
		if int64(len(data)) > maxFileSize {
			return fmt.Errorf("file size %d bytes exceeds queue limit %d bytes; use 'send' for large files or increase queue.max_file_size", len(data), maxFileSize)
		}

		fileAttachment = botapi.BuildFileAttachment(name, data)
	}

	// Read message text (optional if file is present)
	var message string
	stdinAvailable := filePath != "-"
	if from != "" || fs.NArg() > 0 {
		message, err = input.ReadMessage(from, fs.Args(), deps.Stdin, deps.IsTerminal)
		if err != nil {
			return err
		}
	} else if stdinAvailable && !deps.IsTerminal {
		message, err = input.ReadMessage("", nil, deps.Stdin, false)
		if err != nil {
			if fileAttachment != nil {
				message = ""
			} else {
				return err
			}
		}
	}

	// Must have at least text or file
	if message == "" && fileAttachment == nil {
		return fmt.Errorf("nothing to send: provide a message and/or --file")
	}

	// Validate metadata
	var meta json.RawMessage
	if metadata != "" {
		raw := json.RawMessage(metadata)
		if !json.Valid(raw) {
			return fmt.Errorf("--metadata is not valid JSON")
		}
		meta = raw
	}

	// Create publisher
	pub, err := queue.NewPublisher(cfg.Queue.Driver, cfg.Queue.URL, cfg.Queue.Name)
	if err != nil {
		return fmt.Errorf("creating queue publisher: %w", err)
	}
	defer pub.Close()

	// Build work message
	requestID := newRequestID()

	msg := &queue.WorkMessage{
		RequestID: requestID,
		Routing: queue.Routing{
			Host:            routeHost,
			BotID:           botID,
			ChatID:          chatID,
			BotName:         routeBotName,
			ChatAlias:       routeChatAlias,
			CatalogRevision: routeCatalogRevision,
		},
		Payload: queue.Payload{
			Message:  message,
			Status:   status,
			Metadata: meta,
			Opts: queue.DeliveryOpts{
				Silent:   silent,
				Stealth:  stealth,
				ForceDND: forceDND,
				NoNotify: noNotify,
			},
		},
		ReplyTo:    cfg.Queue.ReplyQueue,
		EnqueuedAt: time.Now().UTC(),
	}

	if fileAttachment != nil {
		msg.Payload.File = &queue.FileAttachment{
			FileName: fileAttachment.FileName,
			Data:     fileAttachment.Data,
		}
	}

	// Publish
	if err := pub.PublishWork(context.Background(), msg); err != nil {
		return fmt.Errorf("publishing to queue: %w", err)
	}

	// Output
	return printEnqueueResult(deps.Stdout, cfg.Format, requestID)
}

// printEnqueueResult outputs the request_id in the appropriate format.
func printEnqueueResult(w io.Writer, format, requestID string) error {
	type enqueueResponse struct {
		OK        bool   `json:"ok"`
		Queued    bool   `json:"queued"`
		RequestID string `json:"request_id"`
	}

	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(enqueueResponse{OK: true, Queued: true, RequestID: requestID})
	}

	fmt.Fprintln(w, requestID)
	return nil
}

// resolveViaCatalog resolves bot/chat aliases through the local catalog cache.
// It loads the catalog from the configured cache file and resolves:
//   - bot alias (--bot) → bot_id, host
//   - chat alias (--chat-id non-UUID) → chat_id, optionally bound bot
//
// If botID is already provided, it skips bot resolution.
// If chatID is a UUID, it skips chat resolution.
func resolveViaCatalog(cfg *config.Config, botID, botAlias, chatID string) (queue.ResolvedRoute, error) {
	var maxAge time.Duration
	if cfg.Catalog.MaxAge != "" {
		var err error
		maxAge, err = time.ParseDuration(cfg.Catalog.MaxAge)
		if err != nil {
			return queue.ResolvedRoute{}, fmt.Errorf("invalid catalog.max_age %q: %w", cfg.Catalog.MaxAge, err)
		}
	}
	cache := queue.NewCatalogCache(cfg.Catalog.CacheFile, maxAge)
	snap := cache.Get()
	if snap == nil {
		return queue.ResolvedRoute{}, fmt.Errorf("no valid catalog snapshot available; ensure worker is publishing catalog and cache_file is configured (catalog.cache_file), or use --routing-mode direct with --bot-id and --chat-id")
	}

	var resolved queue.ResolvedRoute
	resolved.CatalogRevision = snap.Revision

	// Resolve chat alias
	if chatID != "" && !config.IsUUID(chatID) {
		chat, err := snap.ResolveChat(chatID)
		if err != nil {
			return queue.ResolvedRoute{}, err
		}
		resolved.ChatID = chat.ID
		resolved.ChatAlias = chatID
		// Chat may have a bound bot
		if botAlias == "" && botID == "" && chat.Bot != "" {
			botAlias = chat.Bot
		}
	} else {
		resolved.ChatID = chatID
	}

	// Resolve bot
	if botID != "" && config.IsUUID(botID) {
		resolved.BotID = botID
		// Enrich with name/host from catalog if available
		if bot, ok := snap.ResolveBotByID(botID); ok {
			resolved.BotName = bot.Name
			resolved.Host = bot.Host
		}
	} else if botID != "" && !config.IsUUID(botID) {
		// bot_id is not a UUID — treat as alias
		bot, err := snap.ResolveBot(botID)
		if err != nil {
			return queue.ResolvedRoute{}, fmt.Errorf("bot_id %q is not a valid UUID and could not be resolved as alias: %w", botID, err)
		}
		resolved.BotID = bot.ID
		resolved.BotName = bot.Name
		resolved.Host = bot.Host
	} else if botAlias != "" {
		bot, err := snap.ResolveBot(botAlias)
		if err != nil {
			return queue.ResolvedRoute{}, err
		}
		resolved.BotID = bot.ID
		resolved.BotName = bot.Name
		resolved.Host = bot.Host
	} else {
		return queue.ResolvedRoute{}, fmt.Errorf("bot is required: provide --bot (alias) or --bot-id (UUID)")
	}

	if resolved.ChatID == "" {
		return queue.ResolvedRoute{}, fmt.Errorf("--chat-id is required")
	}

	return resolved, nil
}

// newRequestID generates a UUID v4 for message tracing.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
