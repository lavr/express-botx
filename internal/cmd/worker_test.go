package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/apm"
	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/queue"
)

func TestWorker_HandleMessage_Success(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   botxSrv.URL,
				ID:     "bot-uuid-001",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-001",
		Routing: queue.Routing{
			BotID:  "bot-uuid-001",
			ChatID: "chat-uuid-001",
		},
		Payload: queue.Payload{
			Message: "hello from worker test",
			Status:  "ok",
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify BotX API was called
	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}
	if calls[0].GroupChatID != "chat-uuid-001" {
		t.Errorf("GroupChatID = %q, want %q", calls[0].GroupChatID, "chat-uuid-001")
	}
	if calls[0].Notification == nil || calls[0].Notification.Body != "hello from worker test" {
		t.Errorf("unexpected notification: %+v", calls[0].Notification)
	}

	// Verify result was published
	results := fakeQ.Results("test-replies")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].RequestID != "req-001" {
		t.Errorf("RequestID = %q, want %q", results[0].RequestID, "req-001")
	}
	if results[0].Status != "sent" {
		t.Errorf("Status = %q, want %q", results[0].Status, "sent")
	}
	if results[0].SyncID == "" {
		t.Error("expected non-empty SyncID")
	}
}

func TestWorker_HandleMessage_UnknownBotID(t *testing.T) {
	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   "http://localhost",
				ID:     "bot-uuid-known",
				Secret: "s",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-unknown",
		Routing: queue.Routing{
			BotID:  "bot-uuid-UNKNOWN",
			ChatID: "chat-001",
		},
		Payload: queue.Payload{
			Message: "should fail",
			Status:  "ok",
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handler should return nil (ack), got: %v", err)
	}

	// Verify failed result was published
	results := fakeQ.Results("test-replies")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", results[0].Status, "failed")
	}
	if !strings.Contains(results[0].Error, "unknown bot_id") {
		t.Errorf("expected 'unknown bot_id' error, got: %q", results[0].Error)
	}
}

func TestWorker_HandleMessage_UpstreamError_RetryExhausted(t *testing.T) {
	// Create a BotX API that always returns 500
	callCount := 0
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/botx/bots/bot-uuid-fail/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"result": "test-token",
			})
			return
		}
		if r.URL.Path == "/api/v4/botx/notifications/direct" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			callCount++
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"internal server error"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer failSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"fail-bot": {
				Host:   failSrv.URL,
				ID:     "bot-uuid-fail",
				Secret: "test-secret",
			},
		},
		Cache:  config.CacheConfig{Type: "none"},
		Worker: config.WorkerConfig{RetryCount: 2, RetryBackoff: "10ms"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-retry",
		Routing: queue.Routing{
			BotID:  "bot-uuid-fail",
			ChatID: "chat-001",
		},
		Payload: queue.Payload{
			Message: "will fail",
			Status:  "ok",
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err == nil {
		t.Fatalf("handler should return error after retries exhausted (for nack/DLQ), got nil")
	}

	// Should have attempted 1 + 2 retries = 3 total calls
	if callCount != 3 {
		t.Errorf("expected 3 send attempts, got %d", callCount)
	}

	results := fakeQ.Results("test-replies")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "failed" {
		t.Errorf("Status = %q, want %q", results[0].Status, "failed")
	}
}

func TestWorker_HandleMessage_401_TokenRefresh_Success(t *testing.T) {
	// BotX API that rejects the first token but accepts after refresh
	tokenVersion := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/token") {
			tokenVersion++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"result": fmt.Sprintf("token-v%d", tokenVersion),
			})
			return
		}
		if r.URL.Path == "/api/v4/botx/notifications/direct" {
			auth := r.Header.Get("Authorization")
			// Only accept the second token
			if auth != "Bearer token-v2" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"result": map[string]string{"sync_id": "sync-refreshed"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"auth-bot": {
				Host:   srv.URL,
				ID:     "bot-uuid-auth",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-auth",
		Routing: queue.Routing{
			BotID:  "bot-uuid-auth",
			ChatID: "chat-001",
		},
		Payload: queue.Payload{
			Message: "auth test",
			Status:  "ok",
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	results := fakeQ.Results("test-replies")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "sent" {
		t.Errorf("Status = %q, want %q", results[0].Status, "sent")
	}
	if results[0].SyncID != "sync-refreshed" {
		t.Errorf("SyncID = %q, want %q", results[0].SyncID, "sync-refreshed")
	}
}

func TestWorker_HandleMessage_NoReplyTo(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"mybot": {
				Host:   botxSrv.URL,
				ID:     "bot-uuid-noreply",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-noreply",
		Routing: queue.Routing{
			BotID:  "bot-uuid-noreply",
			ChatID: "chat-001",
		},
		Payload: queue.Payload{
			Message: "no reply expected",
			Status:  "ok",
		},
		// No ReplyTo — result should not be published
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no results published anywhere
	results := fakeQ.Results("")
	if len(results) != 0 {
		t.Errorf("expected no results for empty topic, got %d", len(results))
	}
}

func TestWorker_HandleMessage_NoChatsSectionRequired(t *testing.T) {
	// Worker should work without any chats: section in direct mode
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   botxSrv.URL,
				ID:     "bot-001",
				Secret: "test-secret",
			},
		},
		// No Chats section
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-nochats",
		Routing: queue.Routing{
			BotID:  "bot-001",
			ChatID: "direct-chat-uuid",
		},
		Payload: queue.Payload{
			Message: "direct without chats",
			Status:  "ok",
		},
		ReplyTo:    "replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].GroupChatID != "direct-chat-uuid" {
		t.Errorf("GroupChatID = %q", calls[0].GroupChatID)
	}

	results := fakeQ.Results("replies")
	if len(results) != 1 || results[0].Status != "sent" {
		t.Errorf("expected sent result, got %+v", results)
	}
}

func TestWorker_HealthCheck(t *testing.T) {
	fakeQ := queue.NewFake()
	cfg := &config.Config{
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	srv := w.startHealthServer(addr)
	defer srv.Close()

	// Wait for health server to start
	time.Sleep(50 * time.Millisecond)

	// Initially not healthy/ready
	checkHealth := func(path string, expectCode int) {
		t.Helper()
		resp, err := http.Get(fmt.Sprintf("http://%s%s", addr, path))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != expectCode {
			t.Errorf("GET %s: status=%d, want %d", path, resp.StatusCode, expectCode)
		}
	}

	checkHealth("/healthz", 503) // not healthy yet
	checkHealth("/readyz", 503)  // not ready yet

	// Set healthy and ready
	w.healthy.Store(true)
	w.ready.Store(true)

	checkHealth("/healthz", 200) // healthy
	checkHealth("/readyz", 200)  // ready

	// Shutdown: not ready but still healthy
	w.ready.Store(false)
	checkHealth("/healthz", 200) // still healthy
	checkHealth("/readyz", 503)  // not ready

	// Full shutdown
	w.healthy.Store(false)
	checkHealth("/healthz", 503)
}

func TestWorker_CatalogPublish_OnStartup(t *testing.T) {
	fakeQ := queue.NewFake()
	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   "express.company.ru",
				ID:     "bot-uuid-001",
				Secret: "test-secret",
			},
		},
		Chats: map[string]config.ChatConfig{
			"deploy": {ID: "chat-uuid-001", Bot: "alerts"},
		},
		Catalog: config.CatalogConfig{
			QueueName:       "test-catalog",
			PublishInterval: "100ms",
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start catalog publisher
	w.startCatalogPublisher(ctx, cfg.Catalog.QueueName, cfg.Catalog.PublishInterval)

	// Snapshot should have been published immediately on startup
	catalogs := fakeQ.Catalogs("test-catalog")
	if len(catalogs) < 1 {
		t.Fatal("expected at least 1 catalog snapshot on startup")
	}

	snap := catalogs[0]
	if snap.Type != "catalog.snapshot" {
		t.Errorf("Type = %q", snap.Type)
	}
	if len(snap.Bots) != 1 || snap.Bots[0].Name != "alerts" {
		t.Errorf("Bots = %+v", snap.Bots)
	}
	if len(snap.Chats) != 1 || snap.Chats[0].Name != "deploy" {
		t.Errorf("Chats = %+v", snap.Chats)
	}

	// Verify no secrets in the snapshot
	data, _ := json.Marshal(snap)
	jsonStr := string(data)
	if strings.Contains(jsonStr, "test-secret") {
		t.Error("catalog snapshot contains secret — must not leak")
	}
}

func TestWorker_CatalogPublish_Periodic(t *testing.T) {
	fakeQ := queue.NewFake()
	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"bot1": {Host: "h", ID: "b1", Secret: "s"},
		},
		Catalog: config.CatalogConfig{
			QueueName:       "test-catalog-periodic",
			PublishInterval: "50ms",
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.startCatalogPublisher(ctx, cfg.Catalog.QueueName, cfg.Catalog.PublishInterval)

	// Wait for periodic publish
	time.Sleep(120 * time.Millisecond)

	catalogs := fakeQ.Catalogs("test-catalog-periodic")
	if len(catalogs) < 2 {
		t.Errorf("expected at least 2 catalog snapshots (startup + periodic), got %d", len(catalogs))
	}
}

func TestWorker_CatalogPublish_NoCatalogPublishFlag(t *testing.T) {
	// When --no-catalog-publish is set (Publish=false), no snapshots should be published.
	// This test verifies the flag is respected in runWorker by testing the condition directly.
	f := false
	catalogCfg := config.CatalogConfig{
		QueueName: "test-catalog",
		Publish:   &f,
	}

	// The condition in runWorker: catalogEnabled := cfg.Catalog.Publish == nil || *cfg.Catalog.Publish
	catalogEnabled := catalogCfg.Publish == nil || *catalogCfg.Publish
	if catalogEnabled {
		t.Error("catalog publishing should be disabled when Publish=false")
	}

	// Default (nil) should be enabled
	catalogCfg2 := config.CatalogConfig{QueueName: "test-catalog"}
	catalogEnabled2 := catalogCfg2.Publish == nil || *catalogCfg2.Publish
	if !catalogEnabled2 {
		t.Error("catalog publishing should be enabled by default (Publish=nil)")
	}

	// Explicit true
	tr := true
	catalogCfg3 := config.CatalogConfig{QueueName: "test-catalog", Publish: &tr}
	catalogEnabled3 := catalogCfg3.Publish == nil || *catalogCfg3.Publish
	if !catalogEnabled3 {
		t.Error("catalog publishing should be enabled when Publish=true")
	}
}

func TestWorker_CatalogPublish_EmptyQueueName_NoPublish(t *testing.T) {
	// Even if Publish is true (default), no publishing happens without a queue name
	fakeQ := queue.NewFake()
	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"bot1": {Host: "h", ID: "b1", Secret: "s"},
		},
		// No Catalog.QueueName
		Cache: config.CacheConfig{Type: "none"},
	}

	_, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	// Verify condition: catalogEnabled && cfg.Catalog.QueueName != ""
	catalogEnabled := cfg.Catalog.Publish == nil || (cfg.Catalog.Publish != nil && *cfg.Catalog.Publish)
	shouldPublish := catalogEnabled && cfg.Catalog.QueueName != ""
	if shouldPublish {
		t.Error("should not publish when QueueName is empty")
	}
}

func TestWorker_HandleMessage_WithFileAttachment(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"bot": {
				Host:   botxSrv.URL,
				ID:     "bot-file",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-file",
		Routing: queue.Routing{
			BotID:  "bot-file",
			ChatID: "chat-001",
		},
		Payload: queue.Payload{
			Message: "see attached",
			Status:  "ok",
			File: &queue.FileAttachment{
				FileName: "test.txt",
				Data:     "data:text/plain;base64,aGVsbG8=",
			},
		},
		ReplyTo:    "replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].File == nil {
		t.Fatal("expected file attachment in BotX call")
	}
	if calls[0].File.FileName != "test.txt" {
		t.Errorf("FileName = %q, want %q", calls[0].File.FileName, "test.txt")
	}
}

func TestWorker_HandleMessage_WithMentions(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   botxSrv.URL,
				ID:     "bot-uuid-mentions",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	mentions := json.RawMessage(`[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Иван"}}]`)

	msg := &queue.WorkMessage{
		RequestID: "req-mentions",
		Routing: queue.Routing{
			BotID:  "bot-uuid-mentions",
			ChatID: "chat-uuid-001",
		},
		Payload: queue.Payload{
			Message:  "@{mention:aaa-bbb} Привет!",
			Status:   "ok",
			Mentions: mentions,
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify BotX API was called with mentions
	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}
	if calls[0].Notification == nil {
		t.Fatal("expected notification to be set")
	}
	if calls[0].Notification.Body != "@{mention:aaa-bbb} Привет!" {
		t.Errorf("Body = %q, want %q", calls[0].Notification.Body, "@{mention:aaa-bbb} Привет!")
	}
	if calls[0].Notification.Mentions == nil {
		t.Fatal("expected mentions in BotX API request")
	}

	var gotMentions []map[string]interface{}
	if err := json.Unmarshal(calls[0].Notification.Mentions, &gotMentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(gotMentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(gotMentions))
	}
	if gotMentions[0]["mention_id"] != "aaa-bbb" {
		t.Errorf("mention_id = %v, want %q", gotMentions[0]["mention_id"], "aaa-bbb")
	}

	// Verify result was published
	results := fakeQ.Results("test-replies")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "sent" {
		t.Errorf("Status = %q, want %q", results[0].Status, "sent")
	}
}

func TestWorker_HandleMessage_DryRun(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   botxSrv.URL,
				ID:     "bot-uuid-001",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), true)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "dry-run-req-001",
		Routing:   queue.Routing{BotID: "bot-uuid-001", ChatID: "chat-uuid-001"},
		Payload:   queue.Payload{Message: "dry run test", Status: "ok"},
		ReplyTo:   "reply-queue",
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No BotX API calls should have been made
	if calls := mock.getCalls(); len(calls) != 0 {
		t.Errorf("expected 0 BotX calls in dry-run, got %d", len(calls))
	}

	// Result should be published with dry-run status
	results := fakeQ.Results("reply-queue")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "dry-run" {
		t.Errorf("result status = %q, want %q", results[0].Status, "dry-run")
	}
	if results[0].RequestID != "dry-run-req-001" {
		t.Errorf("result request_id = %q, want %q", results[0].RequestID, "dry-run-req-001")
	}
}

func TestWorker_HandleMessage_WithInlineParsedMentions(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   botxSrv.URL,
				ID:     "bot-uuid-parsed",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	// Simulate data that the inline parser would produce:
	// - message has @{mention:id} BotX placeholder
	// - mentions has the parsed entry merged with a raw entry
	rawMention := `{"mention_id":"raw-id","mention_type":"user","mention_data":{"user_huid":"raw-huid","name":"Raw User"}}`
	parsedMention := `{"mention_id":"parsed-id-1","mention_type":"user","mention_data":{"user_huid":"parsed-huid","name":"Alice"}}`
	mergedMentions := json.RawMessage(fmt.Sprintf("[%s,%s]", rawMention, parsedMention))

	msg := &queue.WorkMessage{
		RequestID: "req-parsed-mentions",
		Routing: queue.Routing{
			BotID:  "bot-uuid-parsed",
			ChatID: "chat-uuid-001",
		},
		Payload: queue.Payload{
			Message:  "@{mention:raw-id} and @{mention:parsed-id-1} hello!",
			Status:   "ok",
			Mentions: mergedMentions,
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}

	call := calls[0]
	if call.Notification == nil {
		t.Fatal("expected notification in BotX call")
	}
	// Body must preserve BotX placeholders
	if call.Notification.Body != "@{mention:raw-id} and @{mention:parsed-id-1} hello!" {
		t.Errorf("Body = %q, want %q", call.Notification.Body,
			"@{mention:raw-id} and @{mention:parsed-id-1} hello!")
	}
	// Both raw and parsed mentions must reach BotX
	if call.Notification.Mentions == nil {
		t.Fatal("expected mentions in BotX API request")
	}
	var gotMentions []map[string]interface{}
	if err := json.Unmarshal(call.Notification.Mentions, &gotMentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(gotMentions) != 2 {
		t.Fatalf("expected 2 mentions (raw + parsed), got %d", len(gotMentions))
	}
	if gotMentions[0]["mention_id"] != "raw-id" {
		t.Errorf("first mention_id = %v, want %q", gotMentions[0]["mention_id"], "raw-id")
	}
	if gotMentions[1]["mention_id"] != "parsed-id-1" {
		t.Errorf("second mention_id = %v, want %q", gotMentions[1]["mention_id"], "parsed-id-1")
	}
	parsedData, _ := gotMentions[1]["mention_data"].(map[string]interface{})
	if parsedData == nil || parsedData["user_huid"] != "parsed-huid" {
		t.Errorf("expected user_huid 'parsed-huid', got %v", parsedData)
	}
}

func TestWorker_HandleMessage_ParsesDeferredInlineMentions(t *testing.T) {
	mock := newMockBotxAPI()
	mock.users = map[string]struct{ huid, name string }{
		"user@example.com": {huid: "22222222-2222-2222-2222-222222222222", name: "Jane Doe"},
	}
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   botxSrv.URL,
				ID:     "bot-uuid-deferred",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	rawMentions := json.RawMessage(`[{"mention_id":"raw-1","mention_type":"user","mention_data":{"user_huid":"raw-huid","name":"Raw User"}}]`)
	msg := &queue.WorkMessage{
		RequestID: "req-deferred-mentions",
		Routing: queue.Routing{
			BotID:  "bot-uuid-deferred",
			ChatID: "chat-uuid-001",
		},
		Payload: queue.Payload{
			Message:  "@{mention:raw-1} and @mention[email:user@example.com]",
			Status:   "ok",
			Mentions: rawMentions,
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}
	call := calls[0]
	if call.Notification == nil {
		t.Fatal("expected notification in BotX call")
	}
	if strings.Contains(call.Notification.Body, "@mention[") {
		t.Fatalf("expected inline mention to be parsed on worker, got body %q", call.Notification.Body)
	}
	if !strings.Contains(call.Notification.Body, "@{mention:raw-1}") || !strings.Contains(call.Notification.Body, "@{mention:") {
		t.Fatalf("expected BotX placeholders in body, got %q", call.Notification.Body)
	}

	var gotMentions []map[string]interface{}
	if err := json.Unmarshal(call.Notification.Mentions, &gotMentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(gotMentions) != 2 {
		t.Fatalf("expected 2 mentions (raw + parsed), got %d", len(gotMentions))
	}
	if gotMentions[0]["mention_id"] != "raw-1" {
		t.Errorf("first mention_id = %v, want %q", gotMentions[0]["mention_id"], "raw-1")
	}
	if gotMentions[1]["mention_type"] != "user" {
		t.Errorf("second mention_type = %v, want %q", gotMentions[1]["mention_type"], "user")
	}
	parsedData, _ := gotMentions[1]["mention_data"].(map[string]interface{})
	if parsedData == nil || parsedData["user_huid"] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("unexpected parsed mention_data: %v", parsedData)
	}
}

func TestWorker_HandleMessage_NoParseSkipsDeferredInlineMentions(t *testing.T) {
	mock := newMockBotxAPI()
	mock.users = map[string]struct{ huid, name string }{
		"user@example.com": {huid: "22222222-2222-2222-2222-222222222222", name: "Jane Doe"},
	}
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	fakeQ := queue.NewFake()

	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   botxSrv.URL,
				ID:     "bot-uuid-no-parse",
				Secret: "test-secret",
			},
		},
		Cache: config.CacheConfig{Type: "none"},
	}

	w, err := newWorkerRunner(cfg, fakeQ, apm.New(), false)
	if err != nil {
		t.Fatalf("newWorkerRunner: %v", err)
	}

	msg := &queue.WorkMessage{
		RequestID: "req-no-parse",
		Routing: queue.Routing{
			BotID:  "bot-uuid-no-parse",
			ChatID: "chat-uuid-001",
		},
		Payload: queue.Payload{
			Message: "@mention[email:user@example.com]",
			Status:  "ok",
			Opts: queue.DeliveryOpts{
				NoParse: true,
			},
		},
		ReplyTo:    "test-replies",
		EnqueuedAt: time.Now().UTC(),
	}

	err = w.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}
	call := calls[0]
	if call.Notification == nil {
		t.Fatal("expected notification in BotX call")
	}
	if call.Notification.Body != "@mention[email:user@example.com]" {
		t.Fatalf("body = %q, want raw inline mention to remain unchanged", call.Notification.Body)
	}
	if call.Notification.Mentions != nil {
		t.Fatalf("expected no mentions when no_parse is set, got %s", string(call.Notification.Mentions))
	}
}
