package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/mentions"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

// SendPayload is the parsed request for sending a message.
type SendPayload struct {
	Bot         string          `json:"bot,omitempty"`
	ChatID      string          `json:"chat_id"`
	Message     string          `json:"message"`
	File        *FilePayload    `json:"file,omitempty"`
	Status      string          `json:"status"`
	Opts        *OptsPayload    `json:"opts,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	Mentions    json.RawMessage `json:"mentions,omitempty"`
	RoutingMode string          `json:"routing_mode,omitempty"` // async mode: direct, catalog, mixed
	BotID       string          `json:"bot_id,omitempty"`       // async mode: bot UUID for direct routing
	NoParse     bool            `json:"-"`                      // internal: skip mentions parsing (set by handler for async mode)
}

// FilePayload represents a file attachment in the JSON request.
type FilePayload struct {
	Name string `json:"name"`
	Data string `json:"data"` // base64
}

// OptsPayload holds delivery options.
type OptsPayload struct {
	Silent   bool `json:"silent"`
	Stealth  bool `json:"stealth"`
	ForceDND bool `json:"force_dnd"`
	NoNotify bool `json:"no_notify"`
}

// sendResponse is the request-level error envelope for /send (400/415/500). The
// success/partial-success body is MultiSendResponse; a per-chat outcome is a
// SendResult. sendResponse now carries only ok/error — the earlier single-chat
// sync_id/queued/request_id fields moved into MultiSendResponse.results[].
type sendResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ct)

	var payload SendPayload
	var err error

	switch mediaType {
	case "application/json":
		err = parseJSON(r.Body, &payload)
	case "multipart/form-data":
		err = parseMultipart(r, &payload)
	default:
		writeError(w, http.StatusUnsupportedMediaType, "unsupported content type: use application/json or multipart/form-data")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if string(payload.Mentions) == "null" {
		payload.Mentions = nil
	}
	if len(payload.Mentions) > 0 && payload.Mentions[0] != '[' {
		writeError(w, http.StatusBadRequest, "mentions must be a JSON array")
		return
	}

	if payload.ChatID == "" {
		if s.cfg.DefaultChatAlias != "" {
			payload.ChatID = s.cfg.DefaultChatAlias
		} else {
			writeError(w, http.StatusBadRequest, "chat_id is required")
			return
		}
	}
	if payload.Message == "" && payload.File == nil {
		writeError(w, http.StatusBadRequest, "message or file required")
		return
	}
	if payload.Status == "" {
		payload.Status = "ok"
	}
	if payload.Status != "ok" && payload.Status != "error" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status %q: must be ok or error", payload.Status))
		return
	}

	// Inline mentions parsing: enabled by default, disabled with ?no_parse=true.
	noParse := r.URL.Query().Get("no_parse") == "true"

	if s.cfg.AsyncMode {
		// Async mode: defer mentions parsing to the send function, which runs
		// after catalog routing resolves the actual target bot. This ensures
		// email lookups hit the correct eXpress host in multi-bot setups.
		payload.NoParse = noParse

		// Expand a comma-separated chat_id (chat_id=a,b,c) into N independent
		// enqueues — one queued message per chat — rather than one queued message
		// that fans out inside the worker. This is a deliberate choice: with one
		// message per chat, retry/ack is per-chat and independent, so a transient
		// failure on chat b never re-delivers to chat a (no duplicates) and never
		// drops b. The "one message = whole command, fan out in worker"
		// alternative would give per-command retry: on a partial failure the retry
		// re-sends to the chats that already succeeded (duplicates) or the failed
		// chat is lost with the ack. The worker stays "one chat = one message" and
		// is not touched. See docs/plans/20260708-multi-chat-fanout.md.
		targets := parseChatIDs(payload.ChatID)
		if len(targets) == 0 {
			writeError(w, http.StatusBadRequest, "chat_id is required")
			return
		}

		// Async mode: for direct routing, bot_id is required.
		// For catalog/mixed modes, bot_id or bot alias can be used.
		rm := payload.RoutingMode
		if rm == "" {
			rm = s.cfg.DefaultRoutingMode
		}
		if rm == "" {
			rm = "mixed"
		}
		// Routing validation is request-level (400): apply the routing-mode
		// requirements to every target chat before enqueuing any of them, so a
		// multi-chat request is accepted or rejected as a whole.
		for _, chat := range targets {
			if msg := validateAsyncRouting(rm, payload.BotID, payload.Bot, chat); msg != "" {
				writeError(w, http.StatusBadRequest, msg)
				return
			}
		}

		// Enforce max_file_size for async mode
		if payload.File != nil {
			maxSize := s.cfg.MaxFileSize
			if maxSize == 0 {
				maxSize = 1024 * 1024 // default: 1 MiB
			}
			// File.Data is base64-encoded; decode length is ~3/4 of encoded length
			rawSize := int64(len(payload.File.Data)) * 3 / 4
			if rawSize > maxSize {
				writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file size exceeds queue limit of %d bytes; use synchronous /send or increase queue.max_file_size", maxSize))
				return
			}
		}

		start := time.Now()
		results, errs := fanout(r.Context(), targets, func(ctx context.Context, chat string) (SendResult, error) {
			// Copy the shared payload and enqueue one message for this chat only.
			p := payload
			p.ChatID = chat
			requestID, err := s.send(ctx, &p)
			if err != nil {
				return SendResult{}, err
			}
			return SendResult{Chat: chat, RequestID: requestID, Queued: true}, nil
		})
		elapsed := time.Since(start)

		keyName := KeyName(r.Context())
		if len(results) == 0 {
			vlog.V1("server: %s %s [key: %s] -> 502 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
		} else {
			vlog.V1("server: %s %s [key: %s] -> 202 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
		}
		writeMultiSend(w, results, errs, http.StatusAccepted)
		return
	}

	// Sync mode: fan out to every target chat best-effort. chat_id may list
	// several chats (chat_id=a,b,c); each target independently resolves its chat
	// alias, then its bot, then parses mentions under that bot's own resolver —
	// a chat-bound bot can differ per chat in multi-bot setups, so mentions must
	// be resolved after the per-target bot is known. The shared payload (file,
	// metadata, opts, status) is reused for every target; only chat/bot/message/
	// mentions differ. The response is the uniform MultiSendResponse even for a
	// single chat (results[0]).
	targets := parseChatIDs(payload.ChatID)
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, "chat_id is required")
		return
	}

	start := time.Now()
	results, errs := fanout(r.Context(), targets, func(ctx context.Context, chat string) (SendResult, error) {
		// Resolve chat alias (before bot — chat may have a bound bot).
		chatResult, err := s.chats(chat)
		if err != nil {
			return SendResult{}, err
		}
		// Resolve bot: explicit request bot > chat-bound bot > auth bot.
		resolvedBot, errMsg := s.resolveRequestBot(ctx, payload.Bot, chatResult.Bot)
		if errMsg != "" {
			return SendResult{}, errors.New(errMsg)
		}
		// Parse mentions after bot resolution so the correct per-bot resolver is
		// used when the bot was derived from chat binding rather than the payload.
		resolver := s.mentionsResolver
		if resolvedBot != "" && s.botMentionsResolvers != nil {
			if br, ok := s.botMentionsResolvers[resolvedBot]; ok {
				resolver = br
			}
		}
		parseResult := mentions.Parse(ctx, payload.Message, payload.Mentions, !noParse, resolver)
		if len(parseResult.Errors) > 0 {
			vlog.V2("server: mentions parse: %d error(s)", len(parseResult.Errors))
		}
		// Copy the shared payload and override the per-target fields.
		p := payload
		p.ChatID = chatResult.ChatID
		p.Bot = resolvedBot
		p.Message = parseResult.Message
		p.Mentions = parseResult.Mentions
		syncID, err := s.send(ctx, &p)
		if err != nil {
			return SendResult{}, err
		}
		return SendResult{Chat: chat, SyncID: syncID}, nil
	})
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if len(results) == 0 {
		vlog.V1("server: %s %s [key: %s] -> 502 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
	} else {
		vlog.V1("server: %s %s [key: %s] -> 200 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
	}
	writeMultiSend(w, results, errs, http.StatusOK)
}

// validateAsyncRouting checks a single target chat against the async routing-mode
// requirements. It returns an empty string when the chat is acceptable, or a
// request-level error message (HTTP 400) describing what is missing. When
// chat_id lists several chats it is called once per chat so the whole request is
// accepted or rejected together.
func validateAsyncRouting(rm, botID, botAlias, chat string) string {
	switch rm {
	case "catalog":
		// Catalog mode: bot can come from bot_id, bot alias, or chat-bound bot.
		// If no bot info is provided, chat_id must be a non-UUID alias
		// so the bot can be derived from the chat binding.
		if botID == "" && botAlias == "" && isUUID(chat) {
			return "bot_id or bot alias is required when chat_id is a UUID in catalog mode; use a chat alias with a catalog-bound bot, or provide bot_id/bot"
		}
	case "mixed":
		// Mixed mode: bot can come from bot_id (direct), bot alias, or chat-bound bot.
		if botID == "" && botAlias == "" && isUUID(chat) {
			return "bot_id or bot alias is required when chat_id is a UUID in mixed mode; provide bot_id for direct routing or bot alias for catalog resolution"
		}
	case "direct":
		// Direct mode: bot_id is required and must be a UUID.
		if botID == "" {
			return "bot_id is required for async direct mode"
		}
		if !isUUID(botID) {
			return "bot_id must be a valid UUID for direct routing mode"
		}
		if !isUUID(chat) {
			return "chat_id must be a valid UUID for direct routing mode; use catalog or mixed mode for alias resolution"
		}
	default:
		return fmt.Sprintf("invalid routing_mode %q: must be direct, catalog, or mixed", rm)
	}
	return ""
}

func parseJSON(body io.ReadCloser, p *SendPayload) error {
	defer body.Close()
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(p); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func parseMultipart(r *http.Request, p *SendPayload) error {
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32 MB
		return fmt.Errorf("parsing multipart form: %w", err)
	}

	p.Bot = r.FormValue("bot")
	p.ChatID = r.FormValue("chat_id")
	p.Message = r.FormValue("message")
	p.Status = r.FormValue("status")
	p.RoutingMode = r.FormValue("routing_mode")
	p.BotID = r.FormValue("bot_id")

	if optsStr := r.FormValue("opts"); optsStr != "" {
		p.Opts = &OptsPayload{}
		if err := json.Unmarshal([]byte(optsStr), p.Opts); err != nil {
			return fmt.Errorf("invalid opts JSON: %w", err)
		}
	}

	if metaStr := r.FormValue("metadata"); metaStr != "" {
		raw := json.RawMessage(metaStr)
		if !json.Valid(raw) {
			return fmt.Errorf("invalid metadata JSON")
		}
		p.Metadata = raw
	}

	if mentionsStr := r.FormValue("mentions"); mentionsStr != "" {
		raw := json.RawMessage(mentionsStr)
		if !json.Valid(raw) {
			return fmt.Errorf("invalid mentions JSON")
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return fmt.Errorf("mentions must be a JSON array")
		}
		p.Mentions = raw
	}

	file, header, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		fp, err := readFilePart(file, header)
		if err != nil {
			return err
		}
		p.File = fp
	}

	return nil
}

func readFilePart(file multipart.File, header *multipart.FileHeader) (*FilePayload, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("reading uploaded file: %w", err)
	}
	return &FilePayload{
		Name: header.Filename,
		Data: base64.StdEncoding.EncodeToString(data),
	}, nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(sendResponse{OK: false, Error: msg})
}
