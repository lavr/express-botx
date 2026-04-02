package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"
	"os"
	"path/filepath"

	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/input"
	"github.com/lavr/express-botx/internal/mentions"
	"github.com/lavr/express-botx/internal/token"
)

// refreshableClientResolver wraps a botapi.Client and automatically refreshes
// the token on 401 errors. Used in long-running processes (serve, serve --enqueue)
// where the token may expire between requests. A mutex serializes access to the
// shared client to prevent data races under concurrent HTTP requests.
type refreshableClientResolver struct {
	mu     sync.Mutex
	client *botapi.Client
	cfg    *config.Config
	cache  token.Cache
}

func (r *refreshableClientResolver) GetUserByEmail(ctx context.Context, email string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Lazy auth: if token is empty, authenticate now.
	if r.client.Token == "" {
		tok, err := refreshToken(r.cfg, r.cache)
		if err != nil {
			return "", "", fmt.Errorf("authenticating for email lookup: %w", err)
		}
		r.client.Token = tok
	}

	info, err := r.client.GetUserByEmail(ctx, email)
	if err != nil {
		// If the token expired, refresh and retry once.
		if errors.Is(err, botapi.ErrUnauthorized) && r.cfg.BotSecret != "" {
			tok, refreshErr := refreshToken(r.cfg, r.cache)
			if refreshErr == nil {
				r.client.Token = tok
				info, retryErr := r.client.GetUserByEmail(ctx, email)
				if retryErr == nil {
					return info.HUID, info.Name, nil
				}
				return "", "", retryErr
			}
		}
		return "", "", err
	}
	return info.HUID, info.Name, nil
}

func runSend(args []string, deps Deps) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var from string
	var filePath string
	var fileName string
	var status string
	var silent bool
	var stealth bool
	var forceDND bool
	var noNotify bool
	var noParse bool
	var metadata string
	var mentionsFlag string

	globalFlags(fs, &flags)
	fs.StringVar(&flags.ChatID, "chat-id", "", "target chat UUID or alias")
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
		fmt.Fprintf(deps.Stderr, `Usage: express-botx send [options] [message]

Send a message and/or file to an eXpress chat.

Message sources (in priority order):
  --body-from FILE   Read message from file
  [message]     Positional argument
  stdin         Pipe input (auto-detected, only if --file is not -)

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

	if flags.Secret != "" && flags.Token != "" {
		return fmt.Errorf("--secret and --token are mutually exclusive")
	}

	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}
	if err := cfg.RequireChatID(); err != nil {
		return err
	}

	// Validate status
	if status != "ok" && status != "error" {
		return fmt.Errorf("--status must be ok or error, got %q", status)
	}

	// Read file attachment if requested
	var fileAttachment *botapi.SendFile
	if filePath != "" {
		var data []byte
		var name string

		if filePath == "-" {
			// Read file from stdin
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

		fileAttachment = botapi.BuildFileAttachment(name, data)
	}

	// Read message text (optional if file is present)
	var message string
	stdinAvailable := filePath != "-" // stdin already consumed by file
	if from != "" || fs.NArg() > 0 {
		message, err = input.ReadMessage(from, fs.Args(), deps.Stdin, deps.IsTerminal)
		if err != nil {
			return err
		}
	} else if stdinAvailable && !deps.IsTerminal {
		message, err = input.ReadMessage("", nil, deps.Stdin, false)
		if err != nil {
			// If file is present, empty stdin is ok
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
		// Must be a JSON array
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return fmt.Errorf("--mentions must be a JSON array, got %s", string(trimmed[:1]))
		}
		ment = raw
	}

	// Authenticate
	tok, cache, err := authenticate(cfg)
	if err != nil {
		return err
	}

	client := botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout())

	// Run inline mentions parser.
	// Use refreshableClientResolver so the token is refreshed on 401 — the
	// cached token may have expired since it was stored.
	parseResult := mentions.Parse(
		context.Background(),
		message,
		ment,
		!noParse,
		&refreshableClientResolver{client: client, cfg: cfg, cache: cache},
	)
	for _, e := range parseResult.Errors {
		fmt.Fprintf(deps.Stderr, "warning: mention %s: %s\n", e.Kind, e.Cause)
	}

	// Build SendRequest
	sr := botapi.BuildSendRequest(&botapi.SendParams{
		ChatID:   cfg.ChatID,
		Message:  parseResult.Message,
		Status:   status,
		File:     fileAttachment,
		Metadata: meta,
		Mentions: parseResult.Mentions,
		Silent:   silent,
		Stealth:  stealth,
		ForceDND: forceDND,
		NoNotify: noNotify,
	})

	// Send
	err = client.Send(context.Background(), sr)
	if err != nil {
		if errors.Is(err, botapi.ErrUnauthorized) {
			if cfg.BotToken != "" {
				return fmt.Errorf("bot token rejected (401), re-configure token")
			}
			tok, err = refreshToken(cfg, cache)
			if err != nil {
				return fmt.Errorf("refreshing token: %w", err)
			}
			client.Token = tok
			err = client.Send(context.Background(), sr)
		}
		if err != nil {
			return fmt.Errorf("sending: %w", err)
		}
	}

	return nil
}
