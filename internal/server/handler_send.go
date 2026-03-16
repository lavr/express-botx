package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

// SendPayload is the parsed request for sending a message.
type SendPayload struct {
	Bot      string          `json:"bot,omitempty"`
	ChatID   string          `json:"chat_id"`
	Message  string          `json:"message"`
	File     *FilePayload    `json:"file,omitempty"`
	Status   string          `json:"status"`
	Opts     *OptsPayload    `json:"opts,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
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
	OK     bool   `json:"ok"`
	SyncID string `json:"sync_id,omitempty"`
	Error  string `json:"error,omitempty"`
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

func isMultipart(ct string) bool {
	return strings.HasPrefix(ct, "multipart/form-data")
}
