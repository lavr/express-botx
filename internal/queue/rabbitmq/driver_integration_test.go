//go:build integration && rabbitmq

package rabbitmq

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/queue"
)

func rabbitmqURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		url = "amqp://guest:guest@localhost:5672/"
	}
	return url
}

func TestRabbitMQPublishConsumeWork(t *testing.T) {
	url := rabbitmqURL(t)
	queueName := "test-work-" + time.Now().Format("150405")

	pub, err := newPublisher(url, queueName)
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	defer pub.Close()

	cons, err := newConsumer(url, queueName, "")
	if err != nil {
		t.Fatalf("newConsumer: %v", err)
	}
	defer cons.Close()

	msg := &queue.WorkMessage{
		RequestID:  "req-001",
		Routing:    queue.Routing{BotID: "bot-1", ChatID: "chat-1"},
		Payload:    queue.Payload{Message: "hello from rabbitmq"},
		EnqueuedAt: time.Now().UTC(),
	}

	if err := pub.PublishWork(context.Background(), msg); err != nil {
		t.Fatalf("PublishWork: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var received *queue.WorkMessage
	var mu sync.Mutex
	done := make(chan struct{})

	go func() {
		cons.ConsumeWork(ctx, func(_ context.Context, m *queue.WorkMessage) error {
			mu.Lock()
			received = m
			mu.Unlock()
			close(done)
			cancel()
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for work message")
	}

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("no message received")
	}
	if received.RequestID != "req-001" {
		t.Errorf("request_id = %q, want %q", received.RequestID, "req-001")
	}
	if received.Payload.Message != "hello from rabbitmq" {
		t.Errorf("message = %q, want %q", received.Payload.Message, "hello from rabbitmq")
	}
}

func TestRabbitMQPublishResult(t *testing.T) {
	url := rabbitmqURL(t)
	queueName := "test-work-res-" + time.Now().Format("150405")
	replyQueue := "test-reply-" + time.Now().Format("150405")

	pub, err := newPublisher(url, queueName)
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	defer pub.Close()

	res := &queue.WorkResult{
		RequestID: "req-002",
		Status:    "sent",
		SyncID:    "sync-123",
		SentAt:    time.Now().UTC(),
	}

	if err := pub.PublishResult(context.Background(), replyQueue, res); err != nil {
		t.Fatalf("PublishResult: %v", err)
	}
}

func TestRabbitMQCatalogFanout(t *testing.T) {
	url := rabbitmqURL(t)
	queueName := "test-catalog-fanout-" + time.Now().Format("150405")
	catalogExchange := queueName + "-catalog"

	pub, err := newPublisher(url, queueName)
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	defer pub.Close()

	// Start two consumers (simulating two producer instances)
	cons1, err := newConsumer(url, queueName, "")
	if err != nil {
		t.Fatalf("newConsumer 1: %v", err)
	}
	defer cons1.Close()

	cons2, err := newConsumer(url, queueName, "")
	if err != nil {
		t.Fatalf("newConsumer 2: %v", err)
	}
	defer cons2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var received1, received2 *queue.CatalogSnapshot
	var mu1, mu2 sync.Mutex
	done1, done2 := make(chan struct{}), make(chan struct{})

	go func() {
		cons1.ConsumeCatalog(ctx, catalogExchange, func(_ context.Context, s *queue.CatalogSnapshot) error {
			mu1.Lock()
			received1 = s
			mu1.Unlock()
			close(done1)
			return nil
		})
	}()

	go func() {
		cons2.ConsumeCatalog(ctx, catalogExchange, func(_ context.Context, s *queue.CatalogSnapshot) error {
			mu2.Lock()
			received2 = s
			mu2.Unlock()
			close(done2)
			return nil
		})
	}()

	// Give consumers time to bind
	time.Sleep(200 * time.Millisecond)

	snap := &queue.CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "2026-03-18T10:00:00Z:1",
		GeneratedAt: time.Now().UTC(),
		Bots:        []config.BotEntry{{Name: "alerts", Host: "express.test", ID: "bot-uuid-1"}},
		Chats:       []config.ChatEntry{{Name: "deploy", ID: "chat-uuid-1", Bot: "alerts"}},
	}

	if err := pub.PublishCatalog(context.Background(), catalogExchange, snap); err != nil {
		t.Fatalf("PublishCatalog: %v", err)
	}

	// Both consumers should receive the snapshot
	for i, ch := range []chan struct{}{done1, done2} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for catalog on consumer %d", i+1)
		}
	}

	mu1.Lock()
	mu2.Lock()
	defer mu1.Unlock()
	defer mu2.Unlock()

	if received1 == nil || received2 == nil {
		t.Fatal("not all consumers received the snapshot")
	}
	if received1.Revision != snap.Revision {
		t.Errorf("consumer1 revision = %q, want %q", received1.Revision, snap.Revision)
	}
	if received2.Revision != snap.Revision {
		t.Errorf("consumer2 revision = %q, want %q", received2.Revision, snap.Revision)
	}
}
