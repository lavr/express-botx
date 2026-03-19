package queue

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/config"
)

func TestNewPublisher_unknownDriver(t *testing.T) {
	_, err := NewPublisher("nats", "nats://localhost:4222", "test-queue")
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
	if !strings.Contains(err.Error(), `queue driver "nats" is not compiled in`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "-tags nats") {
		t.Fatalf("error should suggest build tag, got: %v", err)
	}
}

func TestNewConsumer_unknownDriver(t *testing.T) {
	_, err := NewConsumer("redis", "redis://localhost:6379", "test-queue", "test-group")
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
	if !strings.Contains(err.Error(), `queue driver "redis" is not compiled in`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFake_publishAndConsumeWork(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	var received *WorkMessage
	if err := f.ConsumeWork(ctx, func(_ context.Context, msg *WorkMessage) error {
		received = msg
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	msg := &WorkMessage{
		RequestID: "req-1",
		Routing: Routing{
			BotID:  "bot-uuid",
			ChatID: "chat-uuid",
		},
		Payload: Payload{
			Message: "hello",
			Status:  "ok",
		},
		EnqueuedAt: time.Now(),
	}

	if err := f.PublishWork(ctx, msg); err != nil {
		t.Fatal(err)
	}

	if received == nil {
		t.Fatal("handler was not called")
	}
	if received.RequestID != "req-1" {
		t.Fatalf("got request_id %q, want %q", received.RequestID, "req-1")
	}

	msgs := f.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
}

func TestFake_publishResult(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	topic := "test-replies"

	res := &WorkResult{
		RequestID: "req-1",
		Status:    "sent",
		SyncID:    "sync-1",
		SentAt:    time.Now(),
	}

	if err := f.PublishResult(ctx, topic, res); err != nil {
		t.Fatal(err)
	}

	results := f.Results(topic)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Status != "sent" {
		t.Fatalf("got status %q, want %q", results[0].Status, "sent")
	}
}

func TestFake_publishAndConsumeCatalog(t *testing.T) {
	f := NewFake()
	ctx := context.Background()
	topic := "test-catalog"

	var received *CatalogSnapshot
	if err := f.ConsumeCatalog(ctx, topic, func(_ context.Context, snap *CatalogSnapshot) error {
		received = snap
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	snap := &CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "2026-03-18T10:15:00Z:17",
		GeneratedAt: time.Now(),
		Bots: []config.BotEntry{
			{Name: "alerts", Host: "express.company.ru", ID: "bot-uuid"},
		},
		Chats: []config.ChatEntry{
			{Name: "deploy", ID: "chat-uuid", Bot: "alerts"},
		},
	}

	if err := f.PublishCatalog(ctx, topic, snap); err != nil {
		t.Fatal(err)
	}

	if received == nil {
		t.Fatal("catalog handler was not called")
	}
	if received.Revision != snap.Revision {
		t.Fatalf("got revision %q, want %q", received.Revision, snap.Revision)
	}

	catalogs := f.Catalogs(topic)
	if len(catalogs) != 1 {
		t.Fatalf("got %d catalogs, want 1", len(catalogs))
	}
}

func TestFake_reset(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	f.PublishWork(ctx, &WorkMessage{RequestID: "r1"})
	f.PublishResult(ctx, "replies", &WorkResult{RequestID: "r1"})

	if len(f.WorkMessages()) != 1 {
		t.Fatal("expected 1 work message before reset")
	}

	f.Reset()

	if len(f.WorkMessages()) != 0 {
		t.Fatal("expected 0 work messages after reset")
	}
	if len(f.Results("replies")) != 0 {
		t.Fatal("expected 0 results after reset")
	}
}

func TestFake_publishWorkWithoutHandler(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	if err := f.PublishWork(ctx, &WorkMessage{RequestID: "r1"}); err != nil {
		t.Fatal(err)
	}

	if len(f.WorkMessages()) != 1 {
		t.Fatal("message should be stored even without handler")
	}
}

func TestFake_close(t *testing.T) {
	f := NewFake()
	if err := f.Close(); err != nil {
		t.Fatalf("Close should return nil, got %v", err)
	}
}

func TestWorkMessage_JSON(t *testing.T) {
	msg := &WorkMessage{
		RequestID: "req-1",
		Routing: Routing{
			Host:   "express.company.ru",
			BotID:  "bot-uuid",
			ChatID: "chat-uuid",
		},
		Payload: Payload{
			Message: "deploy ok",
			Status:  "ok",
			Opts:    DeliveryOpts{ForceDND: true},
		},
		ReplyTo:    "express-botx-replies",
		EnqueuedAt: time.Date(2026, 3, 18, 10, 15, 5, 0, time.UTC),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	var decoded WorkMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.RequestID != msg.RequestID {
		t.Fatalf("request_id: got %q, want %q", decoded.RequestID, msg.RequestID)
	}
	if decoded.Routing.BotID != msg.Routing.BotID {
		t.Fatalf("bot_id: got %q, want %q", decoded.Routing.BotID, msg.Routing.BotID)
	}
	if !decoded.Payload.Opts.ForceDND {
		t.Fatal("expected force_dnd to be true")
	}
}

func TestCatalogSnapshot_JSON(t *testing.T) {
	snap := &CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "2026-03-18T10:15:00Z:17",
		GeneratedAt: time.Date(2026, 3, 18, 10, 15, 0, 0, time.UTC),
		Bots: []config.BotEntry{
			{Name: "alerts", Host: "express.company.ru", ID: "bot-uuid"},
		},
		Chats: []config.ChatEntry{
			{Name: "deploy", ID: "chat-uuid", Bot: "alerts"},
		},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}

	var decoded CatalogSnapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.Type != "catalog.snapshot" {
		t.Fatalf("type: got %q, want %q", decoded.Type, "catalog.snapshot")
	}
	if len(decoded.Bots) != 1 || decoded.Bots[0].Name != "alerts" {
		t.Fatalf("unexpected bots: %+v", decoded.Bots)
	}
}

// Compile-time interface checks.
var (
	_ Publisher = (*Fake)(nil)
	_ Consumer  = (*Fake)(nil)
)
