//go:build rabbitmq

package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/queue"
)

func init() {
	queue.Register("rabbitmq", queue.DriverFactory{
		NewPublisher: newPublisher,
		NewConsumer:  newConsumer,
	})
}

// publisher implements queue.Publisher for RabbitMQ.
// All publish methods are protected by mu because amqp091-go channels
// are not safe for concurrent use.
type publisher struct {
	conn      *amqp.Connection
	ch        *amqp.Channel
	mu        sync.Mutex
	workQueue string
}

func newPublisher(rawURL, queueName string) (queue.Publisher, error) {
	conn, err := amqp.Dial(rawURL)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial %s: %w", redactURL(rawURL), err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}

	// Declare work queue (durable, not auto-delete)
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("rabbitmq: declare work queue %q: %w", queueName, err)
	}

	return &publisher{conn: conn, ch: ch, workQueue: queueName}, nil
}

func (p *publisher) PublishWork(ctx context.Context, msg *queue.WorkMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("rabbitmq: marshal work message: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ch.PublishWithContext(ctx, "", p.workQueue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    msg.RequestID,
		Timestamp:    msg.EnqueuedAt,
		Body:         data,
	})
}

func (p *publisher) PublishResult(ctx context.Context, topic string, res *queue.WorkResult) error {
	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("rabbitmq: marshal result: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Declare result queue on-the-fly (idempotent)
	if _, err := p.ch.QueueDeclare(topic, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare result queue %q: %w", topic, err)
	}

	return p.ch.PublishWithContext(ctx, "", topic, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    res.RequestID,
		Body:         data,
	})
}

func (p *publisher) PublishCatalog(ctx context.Context, topic string, snapshot *queue.CatalogSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("rabbitmq: marshal catalog snapshot: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Declare fanout exchange for catalog (idempotent)
	if err := p.ch.ExchangeDeclare(topic, "fanout", true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare catalog exchange %q: %w", topic, err)
	}

	return p.ch.PublishWithContext(ctx, topic, "", false, false, amqp.Publishing{
		ContentType: "application/json",
		MessageId:   snapshot.Revision,
		Timestamp:   snapshot.GeneratedAt,
		Body:        data,
	})
}

func (p *publisher) Close() error {
	if p.ch != nil {
		p.ch.Close()
	}
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

// consumer implements queue.Consumer for RabbitMQ.
type consumer struct {
	conn      *amqp.Connection
	ch        *amqp.Channel
	workQueue string
	mu        sync.Mutex
	closed    bool
}

func newConsumer(rawURL, queueName, _ string) (queue.Consumer, error) {
	conn, err := amqp.Dial(rawURL)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial %s: %w", redactURL(rawURL), err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}

	// Declare work queue (durable, not auto-delete).
	// Skip declaration if queueName is empty (catalog-only consumer).
	if queueName != "" {
		if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
			ch.Close()
			conn.Close()
			return nil, fmt.Errorf("rabbitmq: declare work queue %q: %w", queueName, err)
		}
	}

	return &consumer{conn: conn, ch: ch, workQueue: queueName}, nil
}

func (c *consumer) ConsumeWork(ctx context.Context, handler func(context.Context, *queue.WorkMessage) error) error {
	// Set prefetch to 1 so the broker delivers one message at a time.
	// Without QoS, RabbitMQ pushes all available messages into the client buffer,
	// causing unnecessary redelivery of buffered messages on graceful shutdown.
	if err := c.ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("rabbitmq: set qos: %w", err)
	}

	msgs, err := c.ch.Consume(c.workQueue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq: consume work queue %q: %w", c.workQueue, err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-msgs:
			if !ok {
				return nil
			}
			var msg queue.WorkMessage
			if err := json.Unmarshal(d.Body, &msg); err != nil {
				// Malformed message — nack without requeue
				d.Nack(false, false)
				continue
			}
			if err := handler(ctx, &msg); err != nil {
				// On context cancellation (shutdown), requeue so broker redelivers
				requeue := ctx.Err() != nil
				d.Nack(false, requeue)
			} else {
				d.Ack(false)
			}
		}
	}
}

func (c *consumer) ConsumeCatalog(ctx context.Context, topic string, handler func(context.Context, *queue.CatalogSnapshot) error) error {
	// Declare fanout exchange
	exchangeName := topic
	if err := c.ch.ExchangeDeclare(exchangeName, "fanout", true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare catalog exchange %q: %w", exchangeName, err)
	}

	// Create exclusive auto-delete queue bound to the exchange
	q, err := c.ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq: declare catalog queue: %w", err)
	}
	if err := c.ch.QueueBind(q.Name, "", exchangeName, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: bind catalog queue: %w", err)
	}

	msgs, err := c.ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq: consume catalog queue: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-msgs:
			if !ok {
				return nil
			}
			var snap queue.CatalogSnapshot
			if err := json.Unmarshal(d.Body, &snap); err != nil {
				continue // skip malformed
			}
			if err := handler(ctx, &snap); err != nil {
				vlog.Info("rabbitmq: catalog handler error: %v", err)
			}
		}
	}
}

func (c *consumer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.ch != nil {
		c.ch.Close()
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// redactURL removes credentials from an AMQP URL for safe logging.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	u.User = nil
	return u.String()
}

var _ queue.Publisher = (*publisher)(nil)
var _ queue.Consumer = (*consumer)(nil)
