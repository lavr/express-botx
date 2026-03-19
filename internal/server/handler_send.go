package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
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
	RoutingMode string          `json:"routing_mode,omitempty"` // async mode: direct, catalog, mixed
	BotID       string          `json:"bot_id,omitempty"`       // async mode: bot UUID for direct routing
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

type sendResponse struct {
	OK        bool   `json:"ok"`
	SyncID    string `json:"sync_id,omitempty"`
	Queued    bool   `json:"queued,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Error     string `json:"error,omitempty"`
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

	if s.cfg.AsyncMode {
		// Async mode: for direct routing, bot_id is required.
		// For catalog/mixed modes, bot_id or bot alias can be used.
		rm := payload.RoutingMode
		if rm == "" {
			rm = s.cfg.DefaultRoutingMode
		}
		if rm == "" {
			rm = "mixed"
		}
		switch rm {
		case "catalog":
			// Catalog mode: bot can come from bot_id, bot alias, or chat-bound bot.
			// If no bot info is provided, chat_id must be a non-UUID alias
			// so the bot can be derived from the chat binding.
			if payload.BotID == "" && payload.Bot == "" && isUUID(payload.ChatID) {
				writeError(w, http.StatusBadRequest, "bot_id or bot alias is required when chat_id is a UUID in catalog mode; use a chat alias with a catalog-bound bot, or provide bot_id/bot")
				return
			}
		case "mixed":
			// Mixed mode: bot can come from bot_id (direct), bot alias, or chat-bound bot.
			if payload.BotID == "" && payload.Bot == "" && isUUID(payload.ChatID) {
				writeError(w, http.StatusBadRequest, "bot_id or bot alias is required when chat_id is a UUID in mixed mode; provide bot_id for direct routing or bot alias for catalog resolution")
				return
			}
		case "direct":
			// Direct mode: bot_id is required and must be a UUID
			if payload.BotID == "" {
				writeError(w, http.StatusBadRequest, "bot_id is required for async direct mode")
				return
			}
			if !isUUID(payload.BotID) {
				writeError(w, http.StatusBadRequest, "bot_id must be a valid UUID for direct routing mode")
				return
			}
			if !isUUID(payload.ChatID) {
				writeError(w, http.StatusBadRequest, "chat_id must be a valid UUID for direct routing mode; use catalog or mixed mode for alias resolution")
				return
			}
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid routing_mode %q: must be direct, catalog, or mixed", rm))
			return
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
		requestID, err := s.send(r.Context(), &payload)
		elapsed := time.Since(start)

		keyName := KeyName(r.Context())
		if err != nil {
			vlog.V1("server: %s %s [key: %s] -> 502 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
			writeError(w, http.StatusBadGateway, "enqueue error: "+err.Error())
			return
		}

		vlog.V1("server: %s %s [key: %s] -> 202 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(sendResponse{OK: true, Queued: true, RequestID: requestID})
		return
	}

	// Sync mode: resolve chat alias and bot, send directly
	// Resolve chat alias (before bot — chat may have a bound bot)
	chatResult, err := s.chats(payload.ChatID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload.ChatID = chatResult.ChatID

	// Resolve bot: explicit request bot > chat-bound bot > auth bot
	resolvedBot, errMsg := s.resolveRequestBot(r.Context(), payload.Bot, chatResult.Bot)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	payload.Bot = resolvedBot

	start := time.Now()
	syncID, err := s.send(r.Context(), &payload)
	elapsed := time.Since(start)

	keyName := KeyName(r.Context())
	if err != nil {
		vlog.V1("server: %s %s [key: %s] -> 502 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	vlog.V1("server: %s %s [key: %s] -> 200 (%dms)", r.Method, r.URL.Path, keyName, elapsed.Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sendResponse{OK: true, SyncID: syncID})
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

