package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

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

	cfg, err := config.LoadForSend(flags)
	if err != nil {
		return err
	}

	// Determine target chats up front so an empty --chat-id fails fast (before
	// authenticating or reading files). A comma-separated --chat-id (--chat-id a,b,c)
	// fans the same message out to each chat. Tokens are kept as given (aliases, not
	// yet resolved to UUIDs) so each chat's bot binding is honoured per target in
	// multi-bot configs. An empty value falls back to the single/default configured
	// chat by alias, preserving that chat's bot binding.
	chats := parseChatIDs(cfg.ChatID)
	if len(chats) == 0 {
		alias, ok := cfg.SingleOrDefaultChatAlias()
		if !ok {
			return cfg.RequireChatID()
		}
		chats = []string{alias}
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

	// Fan out to every target chat, resolving the bot bound to each chat so a
	// multi-bot config delivers each chat via its own bot/token (matching the
	// sync /send handler). Per-bot state — token, HTTP client, and the inline
	// mentions parse (email→huid lookups are per-host) — is built once per bot and
	// reused across that bot's chats. Delivery is best-effort and per-chat
	// independent: a failed chat is recorded, the rest still send, and a non-zero
	// exit is returned only if every chat failed.
	execs := make(map[string]*botExec)
	getExec := func(botName string) (*botExec, error) {
		if be, ok := execs[botName]; ok {
			return be, nil
		}
		botCfg := *cfg
		if cfg.IsMultiBot() {
			if botName == "" {
				return nil, fmt.Errorf("no bot is bound to this chat; add a bot binding in the chats section or pass --bot")
			}
			if aerr := (&botCfg).ApplyChatBot(botName); aerr != nil {
				return nil, aerr
			}
		}
		tok, cache, aerr := authenticate(&botCfg)
		if aerr != nil {
			return nil, aerr
		}
		client := botapi.NewClient(botCfg.Host, tok, botCfg.HTTPTimeout())
		// Inline mentions parser (per-bot resolver; token refreshed on 401).
		pr := mentions.Parse(
			context.Background(),
			message,
			ment,
			!noParse,
			&refreshableClientResolver{client: client, cfg: &botCfg, cache: cache},
		)
		for _, e := range pr.Errors {
			fmt.Fprintf(deps.Stderr, "warning: mention %s: %s\n", e.Kind, e.Cause)
		}
		be := &botExec{client: client, cfg: &botCfg, cache: cache, message: pr.Message, mentions: pr.Mentions}
		execs[botName] = be
		return be, nil
	}

	results := make([]sendCmdResult, 0, len(chats))
	for _, chat := range chats {
		chatID, botName, rerr := resolveSendTarget(cfg, chat)
		if rerr != nil {
			results = append(results, sendCmdResult{Chat: chat, Error: rerr.Error()})
			continue
		}
		be, eerr := getExec(botName)
		if eerr != nil {
			results = append(results, sendCmdResult{Chat: chat, Error: eerr.Error()})
			continue
		}
		sr := botapi.BuildSendRequest(&botapi.SendParams{
			ChatID:   chatID,
			Message:  be.message,
			Status:   status,
			File:     fileAttachment,
			Metadata: meta,
			Mentions: be.mentions,
			Silent:   silent,
			Stealth:  stealth,
			ForceDND: forceDND,
			NoNotify: noNotify,
		})
		syncID, serr := sendWithRefresh(be.client, be.cfg, be.cache, sr)
		if serr != nil {
			results = append(results, sendCmdResult{Chat: chat, Error: serr.Error()})
			continue
		}
		results = append(results, sendCmdResult{Chat: chat, SyncID: syncID})
	}

	return printSendResults(deps.Stdout, cfg.Format, results)
}

// botExec is a per-bot send context: an authenticated client plus the message and
// mentions resolved with that bot's (per-host) mentions resolver.
type botExec struct {
	client   *botapi.Client
	cfg      *config.Config
	cache    token.Cache
	message  string
	mentions json.RawMessage
}

// resolveSendTarget maps a --chat-id token to its chat UUID and the bot bound to
// it. A raw UUID, or any single-bot config, has no per-chat bot and returns
// botName "" (the caller then uses the command's single bot). In multi-bot mode
// an alias's bound bot is returned so each chat sends via its own bot; an alias
// with no binding returns "" and getExec reports it as an error for that chat.
func resolveSendTarget(cfg *config.Config, chat string) (chatID, botName string, err error) {
	if config.IsUUID(chat) {
		return chat, "", nil
	}
	chatID, err = cfg.ResolveChatAlias(chat)
	if err != nil {
		return "", "", err
	}
	if !cfg.IsMultiBot() {
		return chatID, "", nil
	}
	return chatID, cfg.Chats[chat].Bot, nil
}

// sendWithRefresh posts one SendRequest and returns its sync_id, refreshing the
// token once on a 401 exactly as the original single-chat path did.
func sendWithRefresh(client *botapi.Client, cfg *config.Config, cache token.Cache, sr *botapi.SendRequest) (string, error) {
	syncID, err := client.SendWithSyncID(context.Background(), sr)
	if err != nil && errors.Is(err, botapi.ErrUnauthorized) {
		if cfg.BotToken != "" {
			return "", fmt.Errorf("bot token rejected (401), re-configure token")
		}
		tok, rerr := refreshToken(cfg, cache)
		if rerr != nil {
			return "", fmt.Errorf("refreshing token: %w", rerr)
		}
		client.Token = tok
		syncID, err = client.SendWithSyncID(context.Background(), sr)
	}
	if err != nil {
		return "", fmt.Errorf("sending: %w", err)
	}
	return syncID, nil
}

// sendCmdResult is one per-chat sync send outcome: the target chat plus either
// its sync_id (success) or an error string.
type sendCmdResult struct {
	Chat   string
	SyncID string
	Error  string
}

// printSendResults renders per-chat sync send outcomes. In json format it emits
// the uniform multi-chat body
// {"ok":..,"results":[{chat,sync_id}],"errors":[{chat,error}]}. In human format a
// multi-chat send prints one "chat: sync_id" (or "chat: ERROR ..") line per target;
// a single-chat send stays silent on success and surfaces a failure through the
// returned error, preserving the previous CLI behavior. A non-nil error (non-zero
// exit) is returned only when every target failed, mirroring the server's 502
// all-fail semantics.
func printSendResults(w io.Writer, format string, results []sendCmdResult) error {
	okCount := 0
	for _, r := range results {
		if r.Error == "" {
			okCount++
		}
	}

	if format == "json" {
		type jsonResult struct {
			Chat   string `json:"chat"`
			SyncID string `json:"sync_id,omitempty"`
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
		resp := jsonResponse{OK: okCount > 0, Results: []jsonResult{}}
		for _, r := range results {
			if r.Error != "" {
				resp.Errors = append(resp.Errors, jsonError{Chat: r.Chat, Error: r.Error})
			} else {
				resp.Results = append(resp.Results, jsonResult{Chat: r.Chat, SyncID: r.SyncID})
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			return err
		}
	} else {
		// Multi-chat: one line per chat. Single-chat: silent on success, error
		// surfaced via the returned error below (previous behavior).
		if len(results) > 1 {
			for _, r := range results {
				if r.Error != "" {
					fmt.Fprintf(w, "%s: ERROR %s\n", r.Chat, r.Error)
				} else {
					fmt.Fprintf(w, "%s: %s\n", r.Chat, r.SyncID)
				}
			}
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
