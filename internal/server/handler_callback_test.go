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

// blockingHandler blocks until its context is cancelled or release is called.
type blockingHandler struct {
	mu       sync.Mutex
	calls    int
	started  chan struct{} // closed when Handle begins
	release  chan struct{} // close to unblock Handle
	ctxDone  bool         // set to true if context was cancelled during Handle
}

func newBlockingHandler() *blockingHandler {
	return &blockingHandler{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (h *blockingHandler) Type() string { return "blocking" }

func (h *blockingHandler) Handle(ctx context.Context, event string, payload []byte) error {
	h.mu.Lock()
	h.calls++
	if h.calls == 1 {
		close(h.started)
	}
	h.mu.Unlock()

	select {
	case <-h.release:
	case <-ctx.Done():
		h.mu.Lock()
		h.ctxDone = true
		h.mu.Unlock()
		return ctx.Err()
	}
	return nil
}

func (h *blockingHandler) wasContextCancelled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ctxDone
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
	srv.callbackWG.Wait()
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
	srv.callbackWG.Wait()

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

// panickingHandler panics when Handle is called — used to test panic recovery.
type panickingHandler struct{}

func (h *panickingHandler) Type() string { return "panicking" }
func (h *panickingHandler) Handle(_ context.Context, _ string, _ []byte) error {
	panic("test panic in handler")
}

func TestAsyncPanicRecovery(t *testing.T) {
	t.Run("panic in async command handler is recovered", func(t *testing.T) {
		ph := &panickingHandler{}
		router, err := NewCallbackRouter(
			[][]string{{"message"}},
			[]bool{true},
			map[int]CallbackHandler{0: ph},
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

		if w.Code != 202 {
			t.Fatalf("expected 202, got %d", w.Code)
		}

		// Wait for async goroutine to complete (panic + recover).
		srv.callbackWG.Wait()

		// Error tracker should have captured the panic.
		if tracker.errorCount() != 1 {
			t.Fatalf("expected 1 captured error from panic, got %d", tracker.errorCount())
		}

		tracker.mu.Lock()
		errMsg := tracker.errors[0].Error()
		tracker.mu.Unlock()
		if !strings.Contains(errMsg, "panic") || !strings.Contains(errMsg, "test panic in handler") {
			t.Fatalf("expected panic error message, got: %s", errMsg)
		}
	})

	t.Run("panic in async notification handler is recovered", func(t *testing.T) {
		ph := &panickingHandler{}
		router, err := NewCallbackRouter(
			[][]string{{"notification_callback"}},
			[]bool{true},
			map[int]CallbackHandler{0: ph},
		)
		if err != nil {
			t.Fatalf("NewCallbackRouter: %v", err)
		}

		tracker := &mockErrTracker{}
		srv := newTestServerWithCallbackRouter(router)
		srv.errTracker = tracker

		body := `{"sync_id":"n1","status":"ok"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/notification/callback", strings.NewReader(body))
		srv.handleNotificationCallback(w, req)

		if w.Code != 200 {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		srv.callbackWG.Wait()

		if tracker.errorCount() != 1 {
			t.Fatalf("expected 1 captured error from panic, got %d", tracker.errorCount())
		}
	})

	t.Run("server continues after panic", func(t *testing.T) {
		ph := &panickingHandler{}
		normalHandler := &recordingHandler{}
		router, err := NewCallbackRouter(
			[][]string{{"message"}, {"chat_created"}},
			[]bool{true, false},
			map[int]CallbackHandler{0: ph, 1: normalHandler},
		)
		if err != nil {
			t.Fatalf("NewCallbackRouter: %v", err)
		}

		tracker := &mockErrTracker{}
		srv := newTestServerWithCallbackRouter(router)
		srv.errTracker = tracker

		// First request: panicking handler (async).
		body1 := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
		w1 := httptest.NewRecorder()
		req1 := httptest.NewRequest("POST", "/command", strings.NewReader(body1))
		srv.handleCommand(w1, req1)
		srv.callbackWG.Wait()

		// Second request: normal handler (sync) — server should still work.
		body2 := `{"sync_id":"s2","command":{"body":"system:chat_created"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest("POST", "/command", strings.NewReader(body2))
		srv.handleCommand(w2, req2)

		if w2.Code != 202 {
			t.Fatalf("expected 202 after panic recovery, got %d", w2.Code)
		}
		if normalHandler.callCount() != 1 {
			t.Fatalf("expected normal handler to be called after panic recovery, got %d calls", normalHandler.callCount())
		}
	})
}

func TestGracefulShutdown(t *testing.T) {
	t.Run("shutdown waits for async handlers", func(t *testing.T) {
		bh := newBlockingHandler()
		router, err := NewCallbackRouter(
			[][]string{{"message"}},
			[]bool{true},
			map[int]CallbackHandler{0: bh},
		)
		if err != nil {
			t.Fatalf("NewCallbackRouter: %v", err)
		}

		srv := newTestServerWithCallbackRouter(router)

		// Send a request that starts an async handler.
		body := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
		srv.handleCommand(w, req)

		if w.Code != 202 {
			t.Fatalf("expected 202, got %d", w.Code)
		}

		// Wait for handler to start.
		<-bh.started

		// WaitGroup should not be done yet (handler is still running).
		done := make(chan struct{})
		go func() {
			srv.callbackWG.Wait()
			close(done)
		}()

		select {
		case <-done:
			t.Fatal("WaitGroup finished before handler was released")
		case <-time.After(20 * time.Millisecond):
			// Expected: handler still running.
		}

		// Release the handler.
		close(bh.release)

		// Now WaitGroup should complete.
		select {
		case <-done:
			// OK
		case <-time.After(time.Second):
			t.Fatal("WaitGroup did not finish after handler release")
		}
	})

	t.Run("shutdown cancels context for async handlers", func(t *testing.T) {
		bh := newBlockingHandler()
		router, err := NewCallbackRouter(
			[][]string{{"message"}},
			[]bool{true},
			map[int]CallbackHandler{0: bh},
		)
		if err != nil {
			t.Fatalf("NewCallbackRouter: %v", err)
		}

		srv := newTestServerWithCallbackRouter(router)

		// Send a request that starts an async handler.
		body := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
		srv.handleCommand(w, req)

		// Wait for handler to start.
		<-bh.started

		// Cancel the callback context (simulating shutdown).
		srv.callbackCancel()

		// Wait for the handler to finish (context cancellation should unblock it).
		srv.callbackWG.Wait()

		if !bh.wasContextCancelled() {
			t.Fatal("expected handler to observe context cancellation")
		}
	})

	t.Run("multiple async handlers tracked", func(t *testing.T) {
		h1 := &recordingHandler{delay: 30 * time.Millisecond}
		h2 := &recordingHandler{delay: 30 * time.Millisecond}
		router, err := NewCallbackRouter(
			[][]string{{"message"}, {"message"}},
			[]bool{true, true},
			map[int]CallbackHandler{0: h1, 1: h2},
		)
		if err != nil {
			t.Fatalf("NewCallbackRouter: %v", err)
		}

		srv := newTestServerWithCallbackRouter(router)

		body := `{"sync_id":"s1","command":{"body":"hello"},"from":{"group_chat_id":"g1"},"bot_id":"b1"}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/command", strings.NewReader(body))
		srv.handleCommand(w, req)

		if w.Code != 202 {
			t.Fatalf("expected 202, got %d", w.Code)
		}

		// Wait for all async handlers to complete via WaitGroup.
		srv.callbackWG.Wait()

		if h1.callCount() != 1 {
			t.Fatalf("handler 1: expected 1 call, got %d", h1.callCount())
		}
		if h2.callCount() != 1 {
			t.Fatalf("handler 2: expected 1 call, got %d", h2.callCount())
		}
	})
}
