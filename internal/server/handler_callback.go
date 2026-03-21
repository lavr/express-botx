package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	vlog "github.com/lavr/express-botx/internal/log"
)

// callbackResponse is the JSON response for callback endpoints.
type callbackResponse struct {
	Result string `json:"result"`
}

// handleCommand handles POST /command from the BotX platform.
// It parses the callback payload, determines the event type, routes to matching
// handlers, and responds with 202 Accepted.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "failed to read request body"})
		return
	}

	var payload CallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid JSON payload"})
		return
	}

	if payload.BotID == "" || payload.SyncID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing required fields: bot_id and sync_id"})
		return
	}

	if payload.From.GroupChatID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing required field: from.group_chat_id"})
		return
	}

	// If JWT was verified, ensure payload bot_id matches the authenticated aud claim.
	// This prevents cross-bot impersonation where a token signed for bot A
	// is used with a payload claiming bot_id = bot B.
	if jwtBotID := JWTAud(r.Context()); jwtBotID != "" && jwtBotID != payload.BotID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "bot_id does not match authenticated token"})
		return
	}

	event := parseEventType(payload.Command.Body)

	matched := s.callbackRouter.Route(event)
	if len(matched) == 0 {
		vlog.V2("server: no matching callback rules for event %q", event)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(callbackResponse{Result: "accepted"})
		return
	}

	for _, m := range matched {
		if m.async {
			s.callbackWG.Add(1)
			go func(h CallbackHandler, ev string, data []byte) {
				defer s.callbackWG.Done()
				defer func() {
					if r := recover(); r != nil {
						err := fmt.Errorf("server: panic in async callback handler %s for event %q: %v", h.Type(), ev, r)
						vlog.V1("%s", err)
						s.errTracker.CaptureError(err)
					}
				}()
				if err := h.Handle(s.callbackCtx, ev, data); err != nil {
					vlog.V1("server: async callback handler %s error for event %q: %v", h.Type(), ev, err)
					s.errTracker.CaptureError(err)
				}
			}(m.handler, event, body)
		} else {
			if err := m.handler.Handle(r.Context(), event, body); err != nil {
				vlog.V1("server: sync callback handler %s error for event %q: %v", m.handler.Type(), event, err)
				s.errTracker.CaptureError(err)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(callbackResponse{Result: "accepted"})
}

// handleNotificationCallback handles POST /notification/callback from the BotX platform.
// It parses the notification callback payload, routes as a "notification_callback" event,
// and responds with 200 OK.
func (s *Server) handleNotificationCallback(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10 MB
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "failed to read request body"})
		return
	}

	var payload NotificationCallbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid JSON payload"})
		return
	}

	if payload.SyncID == "" || payload.Status == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing required fields: sync_id and status"})
		return
	}

	matched := s.callbackRouter.Route(EventNotificationCallback)
	if len(matched) == 0 {
		vlog.V2("server: no matching callback rules for event %q", EventNotificationCallback)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(callbackResponse{Result: "ok"})
		return
	}

	for _, m := range matched {
		if m.async {
			s.callbackWG.Add(1)
			go func(h CallbackHandler, data []byte) {
				defer s.callbackWG.Done()
				defer func() {
					if r := recover(); r != nil {
						err := fmt.Errorf("server: panic in async callback handler %s for event %q: %v", h.Type(), EventNotificationCallback, r)
						vlog.V1("%s", err)
						s.errTracker.CaptureError(err)
					}
				}()
				if err := h.Handle(s.callbackCtx, EventNotificationCallback, data); err != nil {
					vlog.V1("server: async callback handler %s error for event %q: %v", h.Type(), EventNotificationCallback, err)
					s.errTracker.CaptureError(err)
				}
			}(m.handler, body)
		} else {
			if err := m.handler.Handle(r.Context(), EventNotificationCallback, body); err != nil {
				vlog.V1("server: sync callback handler %s error for event %q: %v", m.handler.Type(), EventNotificationCallback, err)
				s.errTracker.CaptureError(err)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(callbackResponse{Result: "ok"})
}
