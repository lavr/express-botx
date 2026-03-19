package queue

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lavr/express-botx/internal/config"
)

// WorkMessage is the unit of work published to the work queue.
type WorkMessage struct {
	RequestID  string    `json:"request_id"`
	Routing    Routing   `json:"routing"`
	Payload    Payload   `json:"payload"`
	ReplyTo    string    `json:"reply_to,omitempty"`
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// Routing describes how the worker should deliver the message.
// BotID and ChatID are required; other fields are optional metadata for tracing.
type Routing struct {
	Host            string `json:"host,omitempty"`
	BotID           string `json:"bot_id"`
	ChatID          string `json:"chat_id"`
	BotName         string `json:"bot_name,omitempty"`
	ChatAlias       string `json:"chat_alias,omitempty"`
	CatalogRevision string `json:"catalog_revision,omitempty"`
}

// Payload carries the message content and delivery options.
type Payload struct {
	Message  string          `json:"message"`
	Status   string          `json:"status,omitempty"`
	File     *FileAttachment `json:"file,omitempty"`
	Opts     DeliveryOpts    `json:"opts"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// FileAttachment is a base64-encoded file for async delivery.
type FileAttachment struct {
	FileName string `json:"file_name"`
	Data     string `json:"data"` // data:mime;base64,...
}

// DeliveryOpts controls notification behavior.
type DeliveryOpts struct {
	Silent   bool `json:"silent,omitempty"`
	Stealth  bool `json:"stealth,omitempty"`
	ForceDND bool `json:"force_dnd,omitempty"`
	NoNotify bool `json:"no_notify,omitempty"`
}

// WorkResult is the outcome of processing a work message, published to the reply queue.
type WorkResult struct {
	RequestID string    `json:"request_id"`
	Status    string    `json:"status"` // "sent" or "failed"
	SyncID    string    `json:"sync_id,omitempty"`
	Error     string    `json:"error,omitempty"`
	SentAt    time.Time `json:"sent_at"`
}

// CatalogSnapshot is a full routing catalog published by the worker.
type CatalogSnapshot struct {
	Type        string             `json:"type"` // "catalog.snapshot"
	Revision    string             `json:"revision"`
	GeneratedAt time.Time          `json:"generated_at"`
	Bots        []config.BotEntry  `json:"bots"`
	Chats       []config.ChatEntry `json:"chats"`
}

// Publisher publishes messages to the broker.
type Publisher interface {
	PublishWork(ctx context.Context, msg *WorkMessage) error
	PublishResult(ctx context.Context, topic string, res *WorkResult) error
	PublishCatalog(ctx context.Context, topic string, snapshot *CatalogSnapshot) error
	Close() error
}

// Consumer reads messages from the broker.
type Consumer interface {
	ConsumeWork(ctx context.Context, handler func(context.Context, *WorkMessage) error) error
	ConsumeCatalog(ctx context.Context, topic string, handler func(context.Context, *CatalogSnapshot) error) error
	Close() error
}
