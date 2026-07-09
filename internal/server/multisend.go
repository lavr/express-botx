package server

// multisend holds the project-wide multi-chat fan-out primitives shared by every
// send surface (/send, /alertmanager, /grafana, /gitlab and the CLI). The
// contract is deliberately uniform: a chat_id may list several chats separated by
// commas (chat_id=a,b,c), the message is delivered best-effort to each, and the
// response is always a MultiSendResponse — even for a single chat. See
// docs/plans/20260708-multi-chat-fanout.md for the rationale.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// SendResult is a single successful per-chat delivery. Exactly one of SyncID
// (synchronous send) or RequestID+Queued (async/enqueue) is populated.
type SendResult struct {
	Chat      string `json:"chat"`
	SyncID    string `json:"sync_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Queued    bool   `json:"queued,omitempty"`
}

// SendError is a single failed per-chat delivery: the target chat and the error
// that prevented delivery (chat/bot resolution or the upstream send/enqueue).
type SendError struct {
	Chat  string `json:"chat"`
	Error string `json:"error"`
}

// MultiSendResponse is the uniform response body for every send surface. OK is
// true when at least one chat received the message (HTTP 200/202); it is false
// when delivery to every chat failed (HTTP 502). Request-level failures (bad
// JSON, empty chat_id, invalid status, unsupported media) are NOT reported here —
// they keep the {"ok":false,"error":"..."} form with 400/415.
type MultiSendResponse struct {
	OK      bool         `json:"ok"`
	Results []SendResult `json:"results,omitempty"`
	Errors  []SendError  `json:"errors,omitempty"`
}

// parseChatIDs splits a raw chat_id value on commas, trims whitespace around each
// entry, drops empties, and deduplicates while preserving first-occurrence order.
// A blank or whitespace-only input yields an empty slice.
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

// fanout delivers to each target best-effort, calling deliver per chat and
// collecting the successful results and per-chat errors independently. Order is
// preserved: results and errors appear in the target order they were produced.
func fanout(ctx context.Context, targets []string, deliver func(ctx context.Context, chat string) (SendResult, error)) (results []SendResult, errs []SendError) {
	for _, target := range targets {
		res, err := deliver(ctx, target)
		if err != nil {
			errs = append(errs, SendError{Chat: target, Error: err.Error()})
			continue
		}
		results = append(results, res)
	}
	return results, errs
}

// fanoutSend delivers message+status to every target chat best-effort using the
// shared fan-out primitive. Chat and bot are resolved per target (explicit
// requestBot ?bot= override > chat-bound bot > auth bot); mentions are NOT parsed
// here — per-bot mention parsing is a /send-only concern. Successes and per-chat
// failures (chat/bot resolution or the upstream send) are collected independently
// and returned in target order; callers write the response with writeMultiSend.
// This is the plain notification path shared by /alertmanager, /grafana and
// /gitlab, all of which deliver an already-rendered message with no mentions.
func (s *Server) fanoutSend(ctx context.Context, targets []string, requestBot, message, status string) ([]SendResult, []SendError) {
	return fanout(ctx, targets, func(ctx context.Context, chat string) (SendResult, error) {
		chatResult, err := s.chats(chat)
		if err != nil {
			return SendResult{}, fmt.Errorf("resolving chat: %w", err)
		}
		botName, errMsg := s.resolveRequestBot(ctx, requestBot, chatResult.Bot)
		if errMsg != "" {
			return SendResult{}, errors.New(errMsg)
		}
		syncID, err := s.send(ctx, &SendPayload{
			Bot:     botName,
			ChatID:  chatResult.ChatID,
			Message: message,
			Status:  status,
		})
		if err != nil {
			return SendResult{}, err
		}
		return SendResult{Chat: chat, SyncID: syncID}, nil
	})
}

// writeMultiSend writes a MultiSendResponse. When at least one delivery
// succeeded it uses successStatus (200 for sync, 202 for async); when every
// delivery failed it uses 502 and OK is false.
func writeMultiSend(w http.ResponseWriter, results []SendResult, errs []SendError, successStatus int) {
	w.Header().Set("Content-Type", "application/json")
	if len(results) == 0 {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(MultiSendResponse{OK: false, Errors: errs})
		return
	}
	if successStatus != http.StatusOK {
		w.WriteHeader(successStatus)
	}
	json.NewEncoder(w).Encode(MultiSendResponse{OK: true, Results: results, Errors: errs})
}
