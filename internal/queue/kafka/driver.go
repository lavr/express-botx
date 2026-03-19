//go:build kafka

package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/queue"
)

func init() {
	queue.Register("kafka", queue.DriverFactory{
		NewPublisher: newPublisher,
		NewConsumer:  newConsumer,
	})
}

// publisher implements queue.Publisher for Kafka.
type publisher struct {
	workWriter *kafkago.Writer
	brokers    []string
	mu         sync.Mutex
	writers    map[string]*kafkago.Writer // cached writers keyed by topic
}

func newPublisher(url, topicName string) (queue.Publisher, error) {
	brokers := strings.Split(url, ",")

	if topicName != "" {
		ensureTopics(brokers, topicName)
	}

	w := &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Topic:                  topicName,
		Balancer:               &kafkago.LeastBytes{},
		RequiredAcks:           kafkago.RequireAll,
		MaxAttempts:            3,
		WriteTimeout:           publishTimeout,
		AllowAutoTopicCreation: true,
	}

	return &publisher{workWriter: w, brokers: brokers, writers: make(map[string]*kafkago.Writer)}, nil
}

func (p *publisher) PublishWork(ctx context.Context, msg *queue.WorkMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("kafka: marshal work message: %w", err)
	}
	return p.workWriter.WriteMessages(ctx, kafkago.Message{
		Key:   []byte(msg.RequestID),
		Value: data,
		Time:  msg.EnqueuedAt,
	})
}

func (p *publisher) PublishResult(ctx context.Context, topic string, res *queue.WorkResult) error {
	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("kafka: marshal result: %w", err)
	}

	w := p.getOrCreateWriter(topic)
	return w.WriteMessages(ctx, kafkago.Message{
		Key:   []byte(res.RequestID),
		Value: data,
	})
}

func (p *publisher) PublishCatalog(ctx context.Context, topic string, snapshot *queue.CatalogSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("kafka: marshal catalog snapshot: %w", err)
	}

	w := p.getOrCreateWriter(topic)
	// Use a fixed key so Kafka log compaction retains only the latest snapshot
	return w.WriteMessages(ctx, kafkago.Message{
		Key:   []byte("catalog"),
		Value: data,
		Time:  snapshot.GeneratedAt,
	})
}

// getOrCreateWriter returns a cached writer for the given topic, creating one if needed.
func (p *publisher) getOrCreateWriter(topic string) *kafkago.Writer {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w, ok := p.writers[topic]; ok {
		return w
	}
	ensureTopics(p.brokers, topic)
	w := &kafkago.Writer{
		Addr:                   kafkago.TCP(p.brokers...),
		Topic:                  topic,
		Balancer:               &kafkago.LeastBytes{},
		RequiredAcks:           kafkago.RequireAll,
		MaxAttempts:            3,
		WriteTimeout:           publishTimeout,
		AllowAutoTopicCreation: true,
	}
	p.writers[topic] = w
	return w
}

func (p *publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var lastErr error
	if p.workWriter != nil {
		if err := p.workWriter.Close(); err != nil {
			lastErr = err
		}
	}
	for _, w := range p.writers {
		if err := w.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// consumer implements queue.Consumer for Kafka.
type consumer struct {
	brokers   []string
	workTopic string
	group     string
	mu        sync.Mutex
	readers   []*kafkago.Reader
	closed    bool
}

func newConsumer(url, topicName, group string) (queue.Consumer, error) {
	brokers := strings.Split(url, ",")
	if group == "" {
		group = topicName
	}

	if topicName != "" {
		ensureTopics(brokers, topicName)
	}

	return &consumer{brokers: brokers, workTopic: topicName, group: group}, nil
}

func (c *consumer) ConsumeWork(ctx context.Context, handler func(context.Context, *queue.WorkMessage) error) error {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:  c.brokers,
		Topic:    c.workTopic,
		GroupID:  c.group,
		MinBytes: 1,
		MaxBytes: 10 * 1024 * 1024, // 10 MB
		Dialer: &kafkago.Dialer{
			Timeout:   10 * time.Second,
			DualStack: true,
		},
		CommitInterval:   0, // manual commit
		MaxWait:          time.Second,
		StartOffset:      kafkago.FirstOffset,
		SessionTimeout:   30 * time.Second,
		RebalanceTimeout: 30 * time.Second,
	})
	c.trackReader(r)
	defer r.Close()

	for {
		m, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("kafka: fetch work message: %w", err)
		}

		var msg queue.WorkMessage
		if err := json.Unmarshal(m.Value, &msg); err != nil {
			// Malformed — commit and skip
			r.CommitMessages(ctx, m)
			continue
		}

		if err := handler(ctx, &msg); err != nil {
			if ctx.Err() != nil {
				// Context cancelled (shutdown) — don't commit, let broker redeliver
				return ctx.Err()
			}
			// Handler error — do NOT commit offset so the message can be redelivered
			// or routed to a DLQ by the broker/consumer group rebalance.
			// This matches RabbitMQ driver's nack-without-requeue behavior.
			continue
		}

		if err := r.CommitMessages(ctx, m); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("kafka: commit work offset: %w", err)
		}
	}
}

func (c *consumer) ConsumeCatalog(ctx context.Context, topic string, handler func(context.Context, *queue.CatalogSnapshot) error) error {
	catalogTopic := topic

	// For catalog topic: try to read the latest snapshot on startup by seeking to end-1.
	// Use a dedicated reader without consumer group to control offset manually.
	conn, err := kafkago.DialLeader(ctx, "tcp", c.brokers[0], catalogTopic, 0)
	if err != nil {
		// Topic may not exist yet — start consuming from the end so we pick up
		// new snapshots as soon as the topic is created and the worker publishes.
		vlog.Info("kafka: catalog topic %q not reachable yet, will consume from end: %v", catalogTopic, err)
		return c.consumeCatalogFromOffset(ctx, catalogTopic, kafkago.LastOffset, handler)
	}

	// Get end offset
	_, endOffset, err := conn.ReadOffsets()
	conn.Close()
	if err != nil {
		// Error reading offsets — start from beginning to ensure we don't miss an existing snapshot
		vlog.Info("kafka: catalog topic %q offset read error, consuming from start: %v", catalogTopic, err)
		return c.consumeCatalogFromOffset(ctx, catalogTopic, kafkago.FirstOffset, handler)
	}
	if endOffset == 0 {
		// Empty topic — no snapshot yet, start consuming from end for new snapshots
		return c.consumeCatalogFromOffset(ctx, catalogTopic, kafkago.LastOffset, handler)
	}

	// Read last snapshot (seek to end-1)
	return c.consumeCatalogFromOffset(ctx, catalogTopic, endOffset-1, handler)
}

func (c *consumer) consumeCatalogFromOffset(ctx context.Context, topic string, startOffset int64, handler func(context.Context, *queue.CatalogSnapshot) error) error {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:   c.brokers,
		Topic:     topic,
		Partition: 0,
		MinBytes:  1,
		MaxBytes:  10 * 1024 * 1024,
		MaxWait:   time.Second,
	})
	r.SetOffset(startOffset)
	c.trackReader(r)
	defer r.Close()

	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("kafka: read catalog message: %w", err)
		}

		var snap queue.CatalogSnapshot
		if err := json.Unmarshal(m.Value, &snap); err != nil {
			continue
		}
		if err := handler(ctx, &snap); err != nil {
			vlog.Info("kafka: catalog handler error: %v", err)
		}
	}
}

func (c *consumer) trackReader(r *kafkago.Reader) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readers = append(c.readers, r)
}

func (c *consumer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true

	var lastErr error
	for _, r := range c.readers {
		if err := r.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// ensureTopics creates topics if they don't exist. Best-effort: logs errors
// but does not fail, since the broker may create them automatically.
func ensureTopics(brokers []string, topics ...string) {
	client := &kafkago.Client{
		Addr:    kafkago.TCP(brokers...),
		Timeout: 10 * time.Second,
	}

	configs := make([]kafkago.TopicConfig, len(topics))
	for i, t := range topics {
		configs[i] = kafkago.TopicConfig{
			Topic:             t,
			NumPartitions:     1,
			ReplicationFactor: 1,
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.CreateTopics(ctx, &kafkago.CreateTopicsRequest{
		Addr:   kafkago.TCP(brokers...),
		Topics: configs,
	})
	if err != nil {
		vlog.V1("kafka: create topics: %v", err)
		return
	}
	for topic, topicErr := range resp.Errors {
		if topicErr != nil && !errors.Is(topicErr, kafkago.TopicAlreadyExists) {
			vlog.V1("kafka: create topic %q: %v", topic, topicErr)
		}
	}
}

const publishTimeout = 10 * time.Second

var _ queue.Publisher = (*publisher)(nil)
var _ queue.Consumer = (*consumer)(nil)
