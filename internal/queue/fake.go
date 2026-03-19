package queue

import (
	"context"
	"sync"
)

// Fake is an in-memory queue implementation for testing.
// It supports a single work queue and optional result/catalog channels.
type Fake struct {
	mu       sync.Mutex
	work     []*WorkMessage
	results  map[string][]*WorkResult  // topic -> results
	catalogs map[string][]*CatalogSnapshot // topic -> snapshots

	workHandler    func(context.Context, *WorkMessage) error
	catalogHandler func(context.Context, *CatalogSnapshot) error
}

// NewFake creates a new in-memory fake queue.
func NewFake() *Fake {
	return &Fake{
		results:  make(map[string][]*WorkResult),
		catalogs: make(map[string][]*CatalogSnapshot),
	}
}

// PublishWork adds a message to the in-memory work queue.
// If a work handler is registered (via ConsumeWork), it is called synchronously.
func (f *Fake) PublishWork(ctx context.Context, msg *WorkMessage) error {
	f.mu.Lock()
	f.work = append(f.work, msg)
	handler := f.workHandler
	f.mu.Unlock()

	if handler != nil {
		return handler(ctx, msg)
	}
	return nil
}

// PublishResult adds a result to the in-memory result store.
func (f *Fake) PublishResult(_ context.Context, topic string, res *WorkResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[topic] = append(f.results[topic], res)
	return nil
}

// PublishCatalog adds a catalog snapshot to the in-memory catalog store.
// If a catalog handler is registered, it is called synchronously.
func (f *Fake) PublishCatalog(ctx context.Context, topic string, snapshot *CatalogSnapshot) error {
	f.mu.Lock()
	f.catalogs[topic] = append(f.catalogs[topic], snapshot)
	handler := f.catalogHandler
	f.mu.Unlock()

	if handler != nil {
		return handler(ctx, snapshot)
	}
	return nil
}

// Close is a no-op for the fake queue.
func (f *Fake) Close() error { return nil }

// ConsumeWork registers a handler for work messages.
// The handler is called synchronously when PublishWork is invoked.
// This method returns immediately (does not block like real consumers).
func (f *Fake) ConsumeWork(_ context.Context, handler func(context.Context, *WorkMessage) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workHandler = handler
	return nil
}

// ConsumeCatalog registers a handler for catalog snapshots.
// The handler is called synchronously when PublishCatalog is invoked.
// The topic parameter is ignored for the fake implementation.
func (f *Fake) ConsumeCatalog(_ context.Context, _ string, handler func(context.Context, *CatalogSnapshot) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.catalogHandler = handler
	return nil
}

// WorkMessages returns all published work messages.
func (f *Fake) WorkMessages() []*WorkMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*WorkMessage, len(f.work))
	copy(out, f.work)
	return out
}

// Results returns all published results for a topic.
func (f *Fake) Results(topic string) []*WorkResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*WorkResult, len(f.results[topic]))
	copy(out, f.results[topic])
	return out
}

// Catalogs returns all published catalog snapshots for a topic.
func (f *Fake) Catalogs(topic string) []*CatalogSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*CatalogSnapshot, len(f.catalogs[topic]))
	copy(out, f.catalogs[topic])
	return out
}

// Reset clears all stored messages and handlers.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.work = nil
	f.results = make(map[string][]*WorkResult)
	f.catalogs = make(map[string][]*CatalogSnapshot)
	f.workHandler = nil
	f.catalogHandler = nil
}
