package cmd

import (
	"bytes"
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
	"strings"
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
	var mentionsFlag string
	var noParse bool

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
	fs.StringVar(&mentionsFlag, "mentions", "", "JSON array of mentions in BotX API wire format")
	fs.BoolVar(&noParse, "no-parse", false, "disable inline @mention[...] parsing")
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

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
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

	// Determine effective routing mode. Bot and chat resolution runs per target
	// chat in the enqueue loop below, since a catalog lookup can bind a different
	// bot per chat.
	mode := cfg.Producer.RoutingMode
	botAlias := flags.Bot // --bot flag: alias for catalog resolution (not resolved via config)

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

	// Validate mentions
	var ment json.RawMessage
	if mentionsFlag != "" {
		raw := json.RawMessage(mentionsFlag)
		if !json.Valid(raw) {
			return fmt.Errorf("--mentions is not valid JSON")
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return fmt.Errorf("--mentions must be a JSON array, got %s", string(trimmed[:1]))
		}
		ment = raw
	}

	// Create publisher
	pub, err := queue.NewPublisher(cfg.Queue.Driver, cfg.Queue.URL, cfg.Queue.Name)
	if err != nil {
		return fmt.Errorf("creating queue publisher: %w", err)
	}
	defer pub.Close()

	// Expand a comma-separated --chat-id (--chat-id a,b,c) into N independent
	// enqueues — one queued message per chat — rather than one message that fans
	// out inside the worker. This mirrors the async /send handler and is a
	// deliberate choice: with one message per chat, retry/ack is per-chat and
	// independent, so a transient failure re-delivers only the failed chat and
	// never produces duplicates on the chats that already succeeded (nor loses a
	// failed chat with the ack). The "one message = whole command, fan out in the
	// worker" alternative gives per-command retry, which on a partial failure
	// re-sends to the already-delivered chats (duplicates) or drops the failed
	// chat. The worker stays "one chat = one message" and is not touched.
	chats := parseChatIDs(cfg.ChatID)
	if len(chats) == 0 {
		// Empty/whitespace/only-commas: fall through with the raw value so the
		// per-mode "--chat-id is required" checks below produce their usual error.
		chats = []string{cfg.ChatID}
	}

	// Phase 1 — resolve routing and build a work message for every target chat.
	// This pass is all-or-nothing: if any chat fails routing validation or catalog
	// resolution we return before publishing anything, so nothing lands on the
	// queue for a request that a retry would re-run in full. This mirrors the async
	// /send handler, which validates every target up front and accepts or rejects
	// the request as a whole — and it is what makes the "never produces duplicates
	// on the chats that already succeeded" guarantee above hold: a bad chat in the
	// list can no longer partially publish the good chats before aborting.
	type pendingEnqueue struct {
		chat string
		msg  *queue.WorkMessage
	}
	pending := make([]pendingEnqueue, 0, len(chats))
	for _, chat := range chats {
		// Resolve bot + chat per target; a catalog lookup can bind a different bot
		// per chat, so each iteration starts from the original bot_id.
		botID := cfg.BotID
		chatID := chat
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
				resolved, rerr := resolveViaCatalog(cfg, botID, botAlias, chatID)
				if rerr != nil {
					return rerr
				}
				botID = resolved.BotID
				chatID = resolved.ChatID
				routeHost = resolved.Host
				routeBotName = resolved.BotName
				routeChatAlias = resolved.ChatAlias
				routeCatalogRevision = resolved.CatalogRevision
			}
		case config.RoutingCatalog:
			resolved, rerr := resolveViaCatalog(cfg, botID, botAlias, chatID)
			if rerr != nil {
				return rerr
			}
			botID = resolved.BotID
			chatID = resolved.ChatID
			routeHost = resolved.Host
			routeBotName = resolved.BotName
			routeChatAlias = resolved.ChatAlias
			routeCatalogRevision = resolved.CatalogRevision
		}

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
				Mentions: ment,
				Opts: queue.DeliveryOpts{
					Silent:   silent,
					Stealth:  stealth,
					ForceDND: forceDND,
					NoNotify: noNotify,
					NoParse:  noParse,
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

		pending = append(pending, pendingEnqueue{chat: chat, msg: msg})
	}

	// Phase 2 — publish each built message. Publishing is best-effort and per-chat
	// independent: a broker failure on one chat is recorded and the remaining chats
	// still publish, so a retry re-enqueues only the failed chats rather than
	// double-publishing the ones that already landed. The exit code is non-zero
	// only when every chat failed (see printEnqueueResults).
	results := make([]enqueueResult, 0, len(pending))
	for _, p := range pending {
		if err := pub.PublishWork(context.Background(), p.msg); err != nil {
			results = append(results, enqueueResult{Chat: p.chat, Error: fmt.Errorf("publishing to queue: %w", err).Error()})
			continue
		}
		results = append(results, enqueueResult{Chat: p.chat, RequestID: p.msg.RequestID})
	}

	// Output
	return printEnqueueResults(deps.Stdout, cfg.Format, results)
}

// enqueueResult is a single per-chat enqueue outcome: the target chat plus
// either the request_id assigned to its queued message (success) or an error
// string (publish failure).
type enqueueResult struct {
	Chat      string
	RequestID string
	Error     string
}

// parseChatIDs splits a raw --chat-id value on commas, trims whitespace, drops
// empties, and deduplicates while preserving first-occurrence order. A blank or
// whitespace-only input yields an empty slice. Mirrors the server-side helper so
// the CLI and HTTP surfaces expand a comma-separated chat_id the same way.
func parseChatIDs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		chat := strings.TrimSpace(p)
		if chat == "" {
			continue
		}
		if _, dup := seen[chat]; dup {
			continue
		}
		seen[chat] = struct{}{}
		out = append(out, chat)
	}
	return out
}

// printEnqueueResults outputs one request_id per successfully enqueued chat. In
// human format each request_id is printed on its own line (a single chat prints
// one line, as before); when more than one chat is targeted, a per-chat publish
// failure is printed as a "chat: ERROR .." line. In json format it emits the
// uniform multi-chat response body
// {"ok":..,"results":[{chat,request_id,queued:true}],"errors":[{chat,error}]}.
// A non-nil error (non-zero exit) is returned only when every chat failed,
// mirroring the sync send / server 502 all-fail semantics.
func printEnqueueResults(w io.Writer, format string, results []enqueueResult) error {
	okCount := 0
	for _, r := range results {
		if r.Error == "" {
			okCount++
		}
	}

	if format == "json" {
		type jsonResult struct {
			Chat      string `json:"chat"`
			RequestID string `json:"request_id"`
			Queued    bool   `json:"queued"`
		}
		type jsonError struct {
			Chat  string `json:"chat"`
			Error string `json:"error"`
		}
		type jsonResponse struct {
			OK      bool         `json:"ok"`
			Results []jsonResult `json:"results"`
			Errors  []jsonError  `json:"errors,omitempty"`
		}
		resp := jsonResponse{OK: okCount > 0, Results: make([]jsonResult, 0, len(results))}
		for _, r := range results {
			if r.Error != "" {
				resp.Errors = append(resp.Errors, jsonError{Chat: r.Chat, Error: r.Error})
				continue
			}
			resp.Results = append(resp.Results, jsonResult{Chat: r.Chat, RequestID: r.RequestID, Queued: true})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			if r.Error != "" {
				if len(results) > 1 {
					fmt.Fprintf(w, "%s: ERROR %s\n", r.Chat, r.Error)
				}
				continue
			}
			fmt.Fprintln(w, r.RequestID)
		}
	}

	if okCount == 0 {
		if len(results) == 1 {
			return fmt.Errorf("%s", results[0].Error)
		}
		return fmt.Errorf("all %d chats failed", len(results))
	}
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
