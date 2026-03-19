//go:build integration && kafka

package kafka

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/queue"
)

func kafkaBroker(t *testing.T) string {
	t.Helper()
	broker := os.Getenv("KAFKA_BROKER")
	if broker == "" {
		broker = "localhost:9092"
	}
	return broker
}

func createTopic(t *testing.T, broker, topic string, numPartitions int) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", broker)
	if err != nil {
		t.Fatalf("kafka dial: %v", err)
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		t.Fatalf("kafka controller: %v", err)
	}

	controllerConn, err := kafkago.Dial("tcp", net.JoinHostPort(controller.Host, fmt.Sprintf("%d", controller.Port)))
	if err != nil {
		t.Fatalf("kafka dial controller: %v", err)
	}
	defer controllerConn.Close()

	err = controllerConn.CreateTopics(kafkago.TopicConfig{
		Topic:             topic,
		NumPartitions:     numPartitions,
		ReplicationFactor: 1,
	})
	if err != nil {
		t.Logf("create topic %q: %v (may already exist)", topic, err)
	}
}

func TestKafkaPublishConsumeWork(t *testing.T) {
	broker := kafkaBroker(t)
	topic := fmt.Sprintf("test-work-%d", time.Now().UnixNano())
	group := topic + "-group"

	createTopic(t, broker, topic, 1)

	pub, err := newPublisher(broker, topic)
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	defer pub.Close()

	msg := &queue.WorkMessage{
		RequestID:  "req-k001",
		Routing:    queue.Routing{BotID: "bot-k1", ChatID: "chat-k1"},
		Payload:    queue.Payload{Message: "hello from kafka"},
		EnqueuedAt: time.Now().UTC(),
	}

	if err := pub.PublishWork(context.Background(), msg); err != nil {
		t.Fatalf("PublishWork: %v", err)
	}

	cons, err := newConsumer(broker, topic, group)
	if err != nil {
		t.Fatalf("newConsumer: %v", err)
	}
	defer cons.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for work message")
	}

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("no message received")
	}
	if received.RequestID != "req-k001" {
		t.Errorf("request_id = %q, want %q", received.RequestID, "req-k001")
	}
	if received.Payload.Message != "hello from kafka" {
		t.Errorf("message = %q, want %q", received.Payload.Message, "hello from kafka")
	}
}

func TestKafkaPublishResult(t *testing.T) {
	broker := kafkaBroker(t)
	workTopic := fmt.Sprintf("test-work-res-%d", time.Now().UnixNano())
	replyTopic := fmt.Sprintf("test-reply-%d", time.Now().UnixNano())

	createTopic(t, broker, workTopic, 1)
	createTopic(t, broker, replyTopic, 1)

	pub, err := newPublisher(broker, workTopic)
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	defer pub.Close()

	res := &queue.WorkResult{
		RequestID: "req-k002",
		Status:    "sent",
		SyncID:    "sync-k123",
		SentAt:    time.Now().UTC(),
	}

	if err := pub.PublishResult(context.Background(), replyTopic, res); err != nil {
		t.Fatalf("PublishResult: %v", err)
	}
}

func TestKafkaCatalogLastSnapshot(t *testing.T) {
	broker := kafkaBroker(t)
	workTopic := fmt.Sprintf("test-catalog-%d", time.Now().UnixNano())
	catalogTopic := workTopic + "-catalog"

	createTopic(t, broker, workTopic, 1)
	createTopic(t, broker, catalogTopic, 1)

	pub, err := newPublisher(broker, workTopic)
	if err != nil {
		t.Fatalf("newPublisher: %v", err)
	}
	defer pub.Close()

	// Publish two snapshots
	snap1 := &queue.CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "rev-1",
		GeneratedAt: time.Now().UTC(),
		Bots:        []config.BotEntry{{Name: "bot1", Host: "h1", ID: "id1"}},
	}
	snap2 := &queue.CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "rev-2",
		GeneratedAt: time.Now().UTC(),
		Bots:        []config.BotEntry{{Name: "bot2", Host: "h2", ID: "id2"}},
		Chats:       []config.ChatEntry{{Name: "deploy", ID: "c1", Bot: "bot2"}},
	}

	if err := pub.PublishCatalog(context.Background(), catalogTopic, snap1); err != nil {
		t.Fatalf("PublishCatalog 1: %v", err)
	}
	if err := pub.PublishCatalog(context.Background(), catalogTopic, snap2); err != nil {
		t.Fatalf("PublishCatalog 2: %v", err)
	}

	// Give Kafka time to replicate
	time.Sleep(500 * time.Millisecond)

	// Consumer should read the last snapshot on startup
	cons, err := newConsumer(broker, workTopic, "")
	if err != nil {
		t.Fatalf("newConsumer: %v", err)
	}
	defer cons.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var snapshots []*queue.CatalogSnapshot
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	go func() {
		cons.ConsumeCatalog(ctx, catalogTopic, func(_ context.Context, s *queue.CatalogSnapshot) error {
			mu.Lock()
			snapshots = append(snapshots, s)
			if len(snapshots) >= 1 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
			mu.Unlock()
			return nil
		})
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for catalog snapshot")
	}

	// Wait a bit for potential extra messages
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(snapshots) == 0 {
		t.Fatal("no snapshot received")
	}

	// The first snapshot received should be from offset end-1 (seek to last)
	// which is snap2 (the latest)
	last := snapshots[len(snapshots)-1]
	if last.Revision != "rev-2" {
		t.Errorf("latest revision = %q, want %q", last.Revision, "rev-2")
	}
}

func TestKafkaEmptyCatalogTopic(t *testing.T) {
	broker := kafkaBroker(t)
	workTopic := fmt.Sprintf("test-empty-catalog-%d", time.Now().UnixNano())

	// Don't create the catalog topic — it shouldn't exist

	cons, err := newConsumer(broker, workTopic, "")
	if err != nil {
		t.Fatalf("newConsumer: %v", err)
	}
	defer cons.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// ConsumeCatalog should return nil for non-existent topic (direct mode fallback)
	err = cons.ConsumeCatalog(ctx, workTopic+"-catalog", func(_ context.Context, s *queue.CatalogSnapshot) error {
		t.Error("unexpected snapshot on empty topic")
		return nil
	})
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("ConsumeCatalog returned: %v (acceptable for missing topic)", err)
	}
}
