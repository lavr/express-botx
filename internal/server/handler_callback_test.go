package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingHandler records calls for test assertions.
type recordingHandler struct {
	mu      sync.Mutex
	calls   []recordedCall
	err     error // if non-nil, Handle returns this error
	delay   time.Duration
}

type recordedCall struct {
	event   string
	payload []byte
}

func (h *recordingHandler) Type() string { return "recording" }
func (h *recordingHandler) Handle(_ context.Context, event string, payload []byte) error {
	if h.delay > 0 {
		time.Sleep(h.delay)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, recordedCall{event: event, payload: payload})
	return h.err
}

func (h *recordingHandler) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func (h *recordingHandler) lastCall() recordedCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls[len(h.calls)-1]
}

// newTestServerWithCallbackRouter creates a Server with a callback router for testing.
func newTestServerWithCallbackRouter(router *CallbackRouter) *Server {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "test-sync-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)
	srv.callbackRouter = router
	return srv
}

func TestHandleCommand(t *testing.T) {
	handler := &recordingHandler{}
	router, err := NewCallbackRouter(
		[][]string{{"chat_created", "added_to_chat"}, {"message"}},
		[]bool{false, false},
		map[int]CallbackHandler{0: handler, 1: handler},
	)
	if err != nil {
		t.Fatalf("NewCallbackRouter: %v", err)
	}
	srv := newTestServerWithCallbackRouter(router)

	t.Run("system event routed correctly", func(t *testing.T) {
		handler.calls = nil
		body := `{"sync_id":"s1","command":{"body":"system:chat_created"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.handleCommand(w, req)

		if w.Code != 202 {
			t.Fatalf("expected 202, got %d", w.Code)
		}

		var resp callbackResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Result != "accepted" {
			t.Fatalf("expected result 'accepted', got %q", resp.Result)
		}

		if handler.callCount() != 1 {
			t.Fatalf("expected 1 call, got %d", handler.callCount())
		}
		call := handler.lastCall()
		if call.event != "chat_created" {
			t.Fatalf("expected event 'chat_created', got %q", call.event)
		}
	})

	t.Run("message event routed correctly", func(t *testing.T) {
		handler.calls = nil
		body := `{"sync_id":"s2","command":{"body":"hello world"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
		srv.handleCommand(w, req)

		if w.Code != 202 {
			t.Fatalf("expected 202, got %d", w.Code)
		}
		if handler.callCount() != 1 {
			t.Fatalf("expected 1 call, got %d", handler.callCount())
		}
		if handler.lastCall().event != "message" {
			t.Fatalf("expected event 'message', got %q", handler.lastCall().event)
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/command", strings.NewReader("not json"))
		srv.handleCommand(w, req)

		if w.Code != 400 {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})

	t.Run("payload passed as raw JSON to handler", func(t *testing.T) {
		handler.calls = nil
		body := `{"sync_id":"s3","command":{"body":"system:added_to_chat"},"from":{"group_chat_id":"g2","user_huid":"u1"},"bot_id":"b2"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
		srv.handleCommand(w, req)

		if w.Code != 202 {
			t.Fatalf("expected 202, got %d", w.Code)
		}
		call := handler.lastCall()
		// Verify the raw JSON body was passed through.
		var p CallbackPayload
		if err := json.Unmarshal(call.payload, &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p.SyncID != "s3" {
			t.Fatalf("expected sync_id 's3', got %q", p.SyncID)
		}
		if p.BotID != "b2" {
			t.Fatalf("expected bot_id 'b2', got %q", p.BotID)
		}
	})
}

func TestHandleCommandAsync(t *testing.T) {
	syncHandler := &recordingHandler{}
	asyncHandler := &recordingHandler{delay: 10 * time.Millisecond}

	router, err := NewCallbackRouter(
		[][]string{{"message"}, {"message"}},
		[]bool{false, true},
		map[int]CallbackHandler{0: syncHandler, 1: asyncHandler},
	)
	if err != nil {
		t.Fatalf("NewCallbackRouter: %v", err)
	}
	srv := newTestServerWithCallbackRouter(router)

	body := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
	srv.handleCommand(w, req)

	// Response should come immediately (sync handler ran, async started in goroutine).
	if w.Code != 202 {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	// Sync handler should have been called before response.
	if syncHandler.callCount() != 1 {
		t.Fatalf("sync handler: expected 1 call, got %d", syncHandler.callCount())
	}

	// Wait for async handler to complete.
	time.Sleep(50 * time.Millisecond)
	if asyncHandler.callCount() != 1 {
		t.Fatalf("async handler: expected 1 call, got %d", asyncHandler.callCount())
	}
}

// mockErrTracker records errors captured via CaptureError.
type mockErrTracker struct {
	mu     sync.Mutex
	errors []error
}

func (m *mockErrTracker) Middleware(h http.Handler) http.Handler { return h }
func (m *mockErrTracker) CaptureError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, err)
}
func (m *mockErrTracker) Flush() {}
func (m *mockErrTracker) errorCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.errors)
}

func TestHandleCommandAsyncError(t *testing.T) {
	handler := &recordingHandler{err: fmt.Errorf("async handler failed")}
	router, err := NewCallbackRouter(
		[][]string{{"message"}},
		[]bool{true},
		map[int]CallbackHandler{0: handler},
	)
	if err != nil {
		t.Fatalf("NewCallbackRouter: %v", err)
	}
	tracker := &mockErrTracker{}
	srv := newTestServerWithCallbackRouter(router)
	srv.errTracker = tracker

	body := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
	srv.handleCommand(w, req)

	// Response should be 202 immediately.
	if w.Code != 202 {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	// Wait for async handler to complete.
	time.Sleep(50 * time.Millisecond)

	if handler.callCount() != 1 {
		t.Fatalf("expected 1 call, got %d", handler.callCount())
	}
	if tracker.errorCount() != 1 {
		t.Fatalf("expected 1 captured error, got %d", tracker.errorCount())
	}
}

func TestHandleCommandSyncError(t *testing.T) {
	handler := &recordingHandler{err: fmt.Errorf("handler failed")}
	router, err := NewCallbackRouter(
		[][]string{{"message"}},
		[]bool{false},
		map[int]CallbackHandler{0: handler},
	)
	if err != nil {
		t.Fatalf("NewCallbackRouter: %v", err)
	}
	srv := newTestServerWithCallbackRouter(router)

	body := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
	srv.handleCommand(w, req)

	// Even on handler error, response should be 202.
	if w.Code != 202 {
		t.Fatalf("expected 202, got %d", w.Code)
	}
}

func TestHandleNotificationCallback(t *testing.T) {
	handler := &recordingHandler{}
	router, err := NewCallbackRouter(
		[][]string{{"notification_callback"}},
		[]bool{false},
		map[int]CallbackHandler{0: handler},
	)
	if err != nil {
		t.Fatalf("NewCallbackRouter: %v", err)
	}
	srv := newTestServerWithCallbackRouter(router)

	t.Run("notification callback routed correctly", func(t *testing.T) {
		handler.calls = nil
		body := `{"sync_id":"n1","status":"ok"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/notification/callback", strings.NewReader(body))
		srv.handleNotificationCallback(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp callbackResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Result != "ok" {
			t.Fatalf("expected result 'ok', got %q", resp.Result)
		}

		if handler.callCount() != 1 {
			t.Fatalf("expected 1 call, got %d", handler.callCount())
		}
		if handler.lastCall().event != "notification_callback" {
			t.Fatalf("expected event 'notification_callback', got %q", handler.lastCall().event)
		}
	})

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/notification/callback", strings.NewReader("bad"))
		srv.handleNotificationCallback(w, req)

		if w.Code != 400 {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
}

func TestHandleNotificationCallbackNoRules(t *testing.T) {
	handler := &recordingHandler{}
	// Only "chat_created" rule — no "notification_callback" rule.
	router, err := NewCallbackRouter(
		[][]string{{"chat_created"}},
		[]bool{false},
		map[int]CallbackHandler{0: handler},
	)
	if err != nil {
		t.Fatalf("NewCallbackRouter: %v", err)
	}
	srv := newTestServerWithCallbackRouter(router)

	body := `{"sync_id":"n1","status":"ok"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/notification/callback", strings.NewReader(body))
	srv.handleNotificationCallback(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp callbackResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Result != "ok" {
		t.Fatalf("expected result 'ok', got %q", resp.Result)
	}

	if handler.callCount() != 0 {
		t.Fatalf("expected 0 calls, got %d", handler.callCount())
	}
}

func TestHandleCommandNoRules(t *testing.T) {
	handler := &recordingHandler{}
	router, err := NewCallbackRouter(
		[][]string{{"chat_created"}},
		[]bool{false},
		map[int]CallbackHandler{0: handler},
	)
	if err != nil {
		t.Fatalf("NewCallbackRouter: %v", err)
	}
	srv := newTestServerWithCallbackRouter(router)

	// Send a "message" event which has no matching rule.
	body := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
	srv.handleCommand(w, req)

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	var resp callbackResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Result != "accepted" {
		t.Fatalf("expected result 'accepted', got %q", resp.Result)
	}

	// Handler should NOT have been called.
	if handler.callCount() != 0 {
		t.Fatalf("expected 0 calls, got %d", handler.callCount())
	}
}
