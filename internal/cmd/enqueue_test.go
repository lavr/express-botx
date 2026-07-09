package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/queue"
)

// testFakeQueue is a shared fake queue for enqueue tests.
// Registered as "testfake" driver so runEnqueue can create publishers via factory.
var testFakeQueue = queue.NewFake()

// testFailQueue is a shared publisher that fails PublishWork for chats listed in
// its failChats set. Registered as the "testfailpub" driver so enqueue tests can
// exercise the best-effort phase-2 publish path (partial and all-fail).
var testFailQueue = &failingPublisher{}

func init() {
	queue.Register("testfake", queue.DriverFactory{
		NewPublisher: func(url, name string) (queue.Publisher, error) {
			return testFakeQueue, nil
		},
		NewConsumer: func(url, name, group string) (queue.Consumer, error) {
			return testFakeQueue, nil
		},
	})
	queue.Register("testfailpub", queue.DriverFactory{
		NewPublisher: func(url, name string) (queue.Publisher, error) {
			return testFailQueue, nil
		},
		NewConsumer: func(url, name, group string) (queue.Consumer, error) {
			return nil, fmt.Errorf("testfailpub has no consumer")
		},
	})
}

// failingPublisher records published work but returns an error for any chat in
// failChats, so a comma-separated enqueue can be driven into a partial or total
// publish failure.
type failingPublisher struct {
	mu        sync.Mutex
	published []*queue.WorkMessage
	failChats map[string]bool
}

func (p *failingPublisher) reset(failChats ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = nil
	p.failChats = make(map[string]bool, len(failChats))
	for _, c := range failChats {
		p.failChats[c] = true
	}
}

func (p *failingPublisher) publishedChats() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.published))
	for _, m := range p.published {
		out = append(out, m.Routing.ChatID)
	}
	return out
}

func (p *failingPublisher) PublishWork(_ context.Context, msg *queue.WorkMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failChats[msg.Routing.ChatID] {
		return fmt.Errorf("broker down for %s", msg.Routing.ChatID)
	}
	p.published = append(p.published, msg)
	return nil
}

func (p *failingPublisher) PublishResult(context.Context, string, *queue.WorkResult) error {
	return nil
}

func (p *failingPublisher) PublishCatalog(context.Context, string, *queue.CatalogSnapshot) error {
	return nil
}

func (p *failingPublisher) Close() error { return nil }

func TestEnqueue_DirectMode_Success(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
  reply_queue: test-replies
`)
	deps, stdout, _ := testDeps()
	deps.Stdin = strings.NewReader("") // no stdin
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"hello from enqueue",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check output is a UUID
	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 || strings.Count(out, "-") != 4 {
		t.Errorf("expected UUID request_id, got %q", out)
	}

	// Check message was published
	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 work message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msg.Routing.BotID != "00000000-0000-0000-0000-000000000b01" {
		t.Errorf("BotID = %q, want %q", msg.Routing.BotID, "00000000-0000-0000-0000-000000000b01")
	}
	if msg.Routing.ChatID != "00000000-0000-0000-0000-000000000c01" {
		t.Errorf("ChatID = %q, want %q", msg.Routing.ChatID, "00000000-0000-0000-0000-000000000c01")
	}
	if msg.Payload.Message != "hello from enqueue" {
		t.Errorf("Message = %q, want %q", msg.Payload.Message, "hello from enqueue")
	}
	if msg.Payload.Status != "ok" {
		t.Errorf("Status = %q, want %q", msg.Payload.Status, "ok")
	}
	if msg.ReplyTo != "test-replies" {
		t.Errorf("ReplyTo = %q, want %q", msg.ReplyTo, "test-replies")
	}
	if msg.RequestID != out {
		t.Errorf("RequestID = %q, want %q (from output)", msg.RequestID, out)
	}
}

func TestEnqueue_DirectMode_JSONOutput(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b02",
		"--chat-id", "00000000-0000-0000-0000-000000000c02",
		"--routing-mode", "direct",
		"--format", "json",
		"json test",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp struct {
		OK      bool `json:"ok"`
		Results []struct {
			Chat      string `json:"chat"`
			RequestID string `json:"request_id"`
			Queued    bool   `json:"queued"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, stdout.String())
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %+v, want 1", resp.Results)
	}
	if !resp.Results[0].Queued {
		t.Error("expected queued=true")
	}
	if resp.Results[0].RequestID == "" {
		t.Error("expected non-empty request_id")
	}
	if resp.Results[0].Chat != "00000000-0000-0000-0000-000000000c02" {
		t.Errorf("chat = %q, want target chat", resp.Results[0].Chat)
	}
}

func TestEnqueue_DirectMode_WithOpts(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b03",
		"--chat-id", "00000000-0000-0000-0000-000000000c03",
		"--routing-mode", "direct",
		"--silent",
		"--stealth",
		"--force-dnd",
		"--status", "error",
		"--metadata", `{"key":"val"}`,
		"msg with opts",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	msg := msgs[0]
	if !msg.Payload.Opts.Silent {
		t.Error("expected Silent=true")
	}
	if !msg.Payload.Opts.Stealth {
		t.Error("expected Stealth=true")
	}
	if !msg.Payload.Opts.ForceDND {
		t.Error("expected ForceDND=true")
	}
	if msg.Payload.Status != "error" {
		t.Errorf("Status = %q, want %q", msg.Payload.Status, "error")
	}
	if string(msg.Payload.Metadata) != `{"key":"val"}` {
		t.Errorf("Metadata = %s, want %s", msg.Payload.Metadata, `{"key":"val"}`)
	}
}

func TestEnqueue_DirectMode_MissingBotID(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--chat-id", "chat-001",
		"--routing-mode", "direct",
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error for missing bot_id")
	}
	if !strings.Contains(err.Error(), "--bot-id is required") {
		t.Errorf("expected bot-id required error, got: %v", err)
	}
}

func TestEnqueue_DirectMode_MissingChatID(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b04",
		"--routing-mode", "direct",
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error for missing chat_id")
	}
	if !strings.Contains(err.Error(), "--chat-id is required") {
		t.Errorf("expected chat-id required error, got: %v", err)
	}
}

func TestEnqueue_MixedMode_DirectPath(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	// Mixed mode with bot_id and chat_id → treated as direct
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b08",
		"--chat-id", "00000000-0000-0000-0000-00000000cafe",
		"mixed direct",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 {
		t.Errorf("expected UUID, got %q", out)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Routing.BotID != "00000000-0000-0000-0000-000000000b08" {
		t.Errorf("BotID = %q", msgs[0].Routing.BotID)
	}
}

func TestEnqueue_MixedMode_NoBotID_NoCatalog_Error(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	// Mixed mode without bot_id and no catalog → error about missing catalog
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--chat-id", "chat-001",
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no valid catalog snapshot") {
		t.Errorf("expected catalog snapshot error, got: %v", err)
	}
}

func TestEnqueue_CatalogMode_NoCatalog_Error(t *testing.T) {
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--routing-mode", "catalog",
		"--chat-id", "deploy",
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no valid catalog snapshot") {
		t.Errorf("expected catalog snapshot error, got: %v", err)
	}
}

func TestEnqueue_StdinMessage(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)

	var stdout, stderr bytes.Buffer
	deps := Deps{
		Stdout:     &stdout,
		Stderr:     &stderr,
		Stdin:      strings.NewReader("message from stdin"),
		IsTerminal: false,
	}

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b05",
		"--chat-id", "00000000-0000-0000-0000-000000000c05",
		"--routing-mode", "direct",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Payload.Message != "message from stdin" {
		t.Errorf("Message = %q, want %q", msgs[0].Payload.Message, "message from stdin")
	}
}

func TestEnqueue_DirectMode_NoCatalogCache_Works(t *testing.T) {
	// Direct mode should work even without any catalog cache
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b06",
		"--chat-id", "00000000-0000-0000-0000-000000000c06",
		"--routing-mode", "direct",
		"direct without catalog",
	}, deps)
	if err != nil {
		t.Fatalf("direct mode should work without catalog cache: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 {
		t.Errorf("expected UUID, got %q", out)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Routing.BotID != "00000000-0000-0000-0000-000000000b06" {
		t.Errorf("BotID = %q", msgs[0].Routing.BotID)
	}
}

func TestEnqueue_CatalogMode_NoCatalogCache_Error(t *testing.T) {
	// Catalog mode without a catalog cache should fail with a clear error
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--routing-mode", "catalog",
		"--chat-id", "deploy",
		"catalog without cache",
	}, deps)
	if err == nil {
		t.Fatal("expected error for catalog mode without cache")
	}
	if !strings.Contains(err.Error(), "no valid catalog snapshot") {
		t.Errorf("expected catalog snapshot error, got: %v", err)
	}
}

func TestEnqueue_MixedMode_NoBotID_NoCatalogCache_Error(t *testing.T) {
	// Mixed mode without bot_id should fail because catalog is not available
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--chat-id", "deploy",
		"mixed without ids",
	}, deps)
	if err == nil {
		t.Fatal("expected error for mixed mode without direct IDs and no catalog")
	}
	if !strings.Contains(err.Error(), "no valid catalog snapshot") {
		t.Errorf("expected catalog snapshot error, got: %v", err)
	}
}

// writeCatalogCache creates a catalog cache file in the given directory
// and returns the path to the cache file.
func writeCatalogCache(t *testing.T, dir string, snap *queue.CatalogSnapshot) string {
	t.Helper()
	path := dir + "/catalog.json"
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testCatalogSnapshot() *queue.CatalogSnapshot {
	return &queue.CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "2026-03-18T10:00:00Z:4",
		GeneratedAt: time.Now().UTC(),
		Bots: []config.BotEntry{
			{Name: "alerts", Host: "express.company.ru", ID: "bot-uuid-alerts"},
			{Name: "deploy", Host: "express.company.ru", ID: "bot-uuid-deploy"},
		},
		Chats: []config.ChatEntry{
			{Name: "deploy", ID: "chat-uuid-deploy", Bot: "alerts"},
			{Name: "general", ID: "chat-uuid-general", Default: true},
		},
	}
}

func TestEnqueue_CatalogMode_AliasResolution(t *testing.T) {
	testFakeQueue.Reset()

	dir := t.TempDir()
	cachePath := writeCatalogCache(t, dir, testCatalogSnapshot())
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
  reply_queue: test-replies
catalog:
  cache_file: `+cachePath+`
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	// Catalog mode: resolve bot alias "alerts" and chat alias "deploy"
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot", "alerts",
		"--chat-id", "deploy",
		"--routing-mode", "catalog",
		"catalog hello",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 {
		t.Errorf("expected UUID request_id, got %q", out)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0]

	// Verify resolved IDs
	if msg.Routing.BotID != "bot-uuid-alerts" {
		t.Errorf("BotID = %q, want %q", msg.Routing.BotID, "bot-uuid-alerts")
	}
	if msg.Routing.ChatID != "chat-uuid-deploy" {
		t.Errorf("ChatID = %q, want %q", msg.Routing.ChatID, "chat-uuid-deploy")
	}

	// Verify observability fields
	if msg.Routing.BotName != "alerts" {
		t.Errorf("BotName = %q, want %q", msg.Routing.BotName, "alerts")
	}
	if msg.Routing.ChatAlias != "deploy" {
		t.Errorf("ChatAlias = %q, want %q", msg.Routing.ChatAlias, "deploy")
	}
	if msg.Routing.Host != "express.company.ru" {
		t.Errorf("Host = %q, want %q", msg.Routing.Host, "express.company.ru")
	}
	if msg.Routing.CatalogRevision == "" {
		t.Error("expected non-empty CatalogRevision")
	}
	if msg.Payload.Message != "catalog hello" {
		t.Errorf("Message = %q, want %q", msg.Payload.Message, "catalog hello")
	}
}

func TestEnqueue_CatalogMode_ChatBoundBot(t *testing.T) {
	// Chat "deploy" is bound to bot "alerts" in the catalog.
	// When --bot is not specified, the bound bot should be used.
	testFakeQueue.Reset()

	dir := t.TempDir()
	cachePath := writeCatalogCache(t, dir, testCatalogSnapshot())
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
catalog:
  cache_file: `+cachePath+`
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--chat-id", "deploy",
		"--routing-mode", "catalog",
		"chat-bound bot test",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	// Bot should be resolved from chat's bound bot "alerts"
	if msgs[0].Routing.BotID != "bot-uuid-alerts" {
		t.Errorf("BotID = %q, want %q (from chat-bound bot)", msgs[0].Routing.BotID, "bot-uuid-alerts")
	}
	if msgs[0].Routing.BotName != "alerts" {
		t.Errorf("BotName = %q, want %q", msgs[0].Routing.BotName, "alerts")
	}
}

func TestEnqueue_CatalogMode_UnknownAlias(t *testing.T) {
	testFakeQueue.Reset()

	dir := t.TempDir()
	cachePath := writeCatalogCache(t, dir, testCatalogSnapshot())
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
catalog:
  cache_file: `+cachePath+`
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot", "nonexistent",
		"--chat-id", "deploy",
		"--routing-mode", "catalog",
		"unknown bot test",
	}, deps)
	if err == nil {
		t.Fatal("expected error for unknown bot alias")
	}
	if !strings.Contains(err.Error(), "unknown bot") {
		t.Errorf("expected 'unknown bot' error, got: %v", err)
	}
}

func TestEnqueue_CatalogMode_StaleCatalog(t *testing.T) {
	// Catalog with very short max_age should expire immediately
	testFakeQueue.Reset()

	dir := t.TempDir()
	staleSnap := testCatalogSnapshot()
	staleSnap.GeneratedAt = time.Now().Add(-1 * time.Hour) // 1 hour old
	cachePath := writeCatalogCache(t, dir, staleSnap)
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
catalog:
  cache_file: `+cachePath+`
  max_age: 1s
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot", "alerts",
		"--chat-id", "deploy",
		"--routing-mode", "catalog",
		"stale catalog test",
	}, deps)
	if err == nil {
		t.Fatal("expected error for stale catalog")
	}
	if !strings.Contains(err.Error(), "no valid catalog snapshot") {
		t.Errorf("expected stale catalog error, got: %v", err)
	}
}

func TestEnqueue_MixedMode_DirectFieldsPlusStaleCatalog(t *testing.T) {
	// Mixed mode with direct fields should work even if catalog is stale
	testFakeQueue.Reset()

	dir := t.TempDir()
	staleSnap := testCatalogSnapshot()
	staleSnap.GeneratedAt = time.Now().Add(-1 * time.Hour)
	cachePath := writeCatalogCache(t, dir, staleSnap)
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
catalog:
  cache_file: `+cachePath+`
  max_age: 1s
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	// Mixed mode with both bot_id and chat_id → should use direct path
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b09",
		"--chat-id", "00000000-0000-0000-0000-00000000beef",
		"direct despite stale catalog",
	}, deps)
	if err != nil {
		t.Fatalf("mixed mode with direct fields should work with stale catalog: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 {
		t.Errorf("expected UUID, got %q", out)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Routing.BotID != "00000000-0000-0000-0000-000000000b09" {
		t.Errorf("BotID = %q, want %q", msgs[0].Routing.BotID, "00000000-0000-0000-0000-000000000b09")
	}
}

func TestEnqueue_MixedMode_CatalogFallback(t *testing.T) {
	// Mixed mode without bot_id should fall back to catalog resolution
	testFakeQueue.Reset()

	dir := t.TempDir()
	cachePath := writeCatalogCache(t, dir, testCatalogSnapshot())
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
catalog:
  cache_file: `+cachePath+`
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot", "deploy",
		"--chat-id", "general",
		"mixed catalog fallback",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 {
		t.Errorf("expected UUID, got %q", out)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.Routing.BotID != "bot-uuid-deploy" {
		t.Errorf("BotID = %q, want %q", msg.Routing.BotID, "bot-uuid-deploy")
	}
	if msg.Routing.ChatID != "chat-uuid-general" {
		t.Errorf("ChatID = %q, want %q", msg.Routing.ChatID, "chat-uuid-general")
	}
	if msg.Routing.BotName != "deploy" {
		t.Errorf("BotName = %q, want %q", msg.Routing.BotName, "deploy")
	}
	if msg.Routing.ChatAlias != "general" {
		t.Errorf("ChatAlias = %q, want %q", msg.Routing.ChatAlias, "general")
	}
}

func TestEnqueue_WithMentions(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	mentions := `[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Ivan"}}]`
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"--mentions", mentions,
		"@{mention:aaa-bbb} hello",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 {
		t.Errorf("expected UUID request_id, got %q", out)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 work message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msg.Payload.Message != "@{mention:aaa-bbb} hello" {
		t.Errorf("Message = %q, want %q", msg.Payload.Message, "@{mention:aaa-bbb} hello")
	}
	if string(msg.Payload.Mentions) != mentions {
		t.Errorf("Mentions = %s, want %s", string(msg.Payload.Mentions), mentions)
	}
}

func TestEnqueue_MentionsInvalidJSON(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"--mentions", `{not valid json`,
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "--mentions is not valid JSON") {
		t.Errorf("expected '--mentions is not valid JSON' error, got: %v", err)
	}
}

func TestEnqueue_MentionsNotArray(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"--mentions", `{"mention_id":"aaa"}`,
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error for non-array JSON")
	}
	if !strings.Contains(err.Error(), "--mentions must be a JSON array") {
		t.Errorf("expected '--mentions must be a JSON array' error, got: %v", err)
	}
}

// mockLookupServer creates a test HTTP server that responds to user-by-email lookups.
func mockLookupServer(users map[string]struct{ huid, name string }) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/botx/users/by_email", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		email := r.URL.Query().Get("email")
		u, ok := users[email]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"status":"error","reason":"not_found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","result":{"user_huid":%q,"name":%q,"emails":[%q],"active":true}}`, u.huid, u.name, email)
	})
	return httptest.NewServer(mux)
}

func TestEnqueue_InlineMentionEmail_DeferredToWorker(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--host", "http://unused.local",
		"--token", "test-token",
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"Hello @mention[email:user@example.com]!",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := strings.TrimSpace(stdout.String())
	if len(out) != 36 {
		t.Errorf("expected UUID request_id, got %q", out)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 work message, got %d", len(msgs))
	}
	msg := msgs[0]

	// Message should be passed through unchanged — parsing is deferred to the worker
	if msg.Payload.Message != "Hello @mention[email:user@example.com]!" {
		t.Errorf("Message = %q, want raw passthrough", msg.Payload.Message)
	}

	// No mentions should be generated at enqueue time
	if len(msg.Payload.Mentions) > 0 {
		t.Errorf("expected no mentions at enqueue time, got %s", string(msg.Payload.Mentions))
	}

	// NoParse should be false (parsing will happen on worker)
	if msg.Payload.Opts.NoParse {
		t.Error("NoParse should be false")
	}
}

func TestEnqueue_RawAndInlineMentionsDeferredToWorker(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	rawMentions := `[{"mention_id":"raw-1","mention_type":"user","mention_data":{"user_huid":"aaaa","name":"Raw User"}}]`
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--host", "http://unused.local",
		"--token", "test-token",
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"--mentions", rawMentions,
		"@{mention:raw-1} and @mention[email:user@example.com]",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 work message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msg.Payload.Message != "@{mention:raw-1} and @mention[email:user@example.com]" {
		t.Errorf("Message = %q, want raw passthrough", msg.Payload.Message)
	}
	if string(msg.Payload.Mentions) != rawMentions {
		t.Errorf("Mentions = %s, want %s", string(msg.Payload.Mentions), rawMentions)
	}
	if msg.Payload.Opts.NoParse {
		t.Error("NoParse should be false so worker can parse inline mentions")
	}
}

func TestEnqueue_NoParse(t *testing.T) {
	testFakeQueue.Reset()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"--no-parse",
		"Hello @mention[email:user@example.com]!",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 work message, got %d", len(msgs))
	}

	// With --no-parse, the token should remain as-is
	if !strings.Contains(msgs[0].Payload.Message, "@mention[email:user@example.com]") {
		t.Errorf("Message should contain original token with --no-parse: %q", msgs[0].Payload.Message)
	}
	// No mentions should be generated
	if len(msgs[0].Payload.Mentions) > 0 {
		t.Errorf("expected no mentions with --no-parse, got: %s", string(msgs[0].Payload.Mentions))
	}
}

func TestEnqueue_ParseErrorDoesNotFail(t *testing.T) {
	testFakeQueue.Reset()

	srv := mockLookupServer(nil) // no users -> lookup will fail
	defer srv.Close()

	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	// Use an email that won't resolve and a malformed token
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--host", srv.URL,
		"--token", "test-token",
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-000000000c01",
		"--routing-mode", "direct",
		"Hello @mention[email:nobody@example.com] and @mention[bad syntax",
	}, deps)
	if err != nil {
		t.Fatalf("parse/lookup error should not fail the command, got: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 work message, got %d", len(msgs))
	}

	// Tokens with errors should remain as literal text
	if !strings.Contains(msgs[0].Payload.Message, "@mention[email:nobody@example.com]") {
		t.Errorf("failed lookup token should stay as literal text: %q", msgs[0].Payload.Message)
	}
	if !strings.Contains(msgs[0].Payload.Message, "@mention[bad syntax") {
		t.Errorf("parse error token should stay as literal text: %q", msgs[0].Payload.Message)
	}
}

func TestEnqueue_NoMessage_Error(t *testing.T) {
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b07",
		"--chat-id", "00000000-0000-0000-0000-000000000c07",
		"--routing-mode", "direct",
	}, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nothing to send") {
		t.Errorf("expected 'nothing to send' error, got: %v", err)
	}
}

// Task 5: --chat-id accepts a comma-separated list and expands into N
// independent enqueues — one queued message per chat — printing one request_id
// per chat. Uses direct mode with UUID chats so no catalog is needed.

func TestEnqueue_MultiChat_Expands(t *testing.T) {
	testFakeQueue.Reset()
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.Stdin = strings.NewReader("")
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-00000000000a,00000000-0000-0000-0000-00000000000b,00000000-0000-0000-0000-00000000000c",
		"--routing-mode", "direct",
		"fanned out",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// One work message per chat (expand), each carrying only its own chat.
	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 work messages, got %d", len(msgs))
	}
	gotChats := map[string]bool{}
	gotReqIDs := map[string]bool{}
	for _, m := range msgs {
		gotChats[m.Routing.ChatID] = true
		gotReqIDs[m.RequestID] = true
		if m.Payload.Message != "fanned out" {
			t.Errorf("message = %q, want shared payload", m.Payload.Message)
		}
	}
	for _, c := range []string{
		"00000000-0000-0000-0000-00000000000a",
		"00000000-0000-0000-0000-00000000000b",
		"00000000-0000-0000-0000-00000000000c",
	} {
		if !gotChats[c] {
			t.Errorf("missing enqueue for chat %q", c)
		}
	}
	if len(gotReqIDs) != 3 {
		t.Errorf("expected 3 distinct request_ids, got %d", len(gotReqIDs))
	}

	// Human output: one request_id per line.
	lines := strings.Fields(strings.TrimSpace(stdout.String()))
	if len(lines) != 3 {
		t.Fatalf("expected 3 request_id lines, got %d: %q", len(lines), stdout.String())
	}
	for _, l := range lines {
		if !gotReqIDs[l] {
			t.Errorf("printed request_id %q not among enqueued %v", l, gotReqIDs)
		}
	}
}

func TestEnqueue_MultiChat_Dedup(t *testing.T) {
	testFakeQueue.Reset()
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.Stdin = strings.NewReader("")
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-00000000000a, 00000000-0000-0000-0000-00000000000a ,00000000-0000-0000-0000-00000000000b",
		"--routing-mode", "direct",
		"dedup me",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 deduped work messages, got %d", len(msgs))
	}
}

func TestEnqueue_MultiChat_JSONOutput(t *testing.T) {
	testFakeQueue.Reset()
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.Stdin = strings.NewReader("")
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-00000000000a,00000000-0000-0000-0000-00000000000b",
		"--routing-mode", "direct",
		"--format", "json",
		"json multi",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp struct {
		OK      bool `json:"ok"`
		Results []struct {
			Chat      string `json:"chat"`
			RequestID string `json:"request_id"`
			Queued    bool   `json:"queued"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, stdout.String())
	}
	if !resp.OK || len(resp.Results) != 2 {
		t.Fatalf("response = %+v, want ok with 2 results", resp)
	}
	if resp.Results[0].Chat != "00000000-0000-0000-0000-00000000000a" ||
		resp.Results[1].Chat != "00000000-0000-0000-0000-00000000000b" {
		t.Errorf("chats = %q/%q, want a then b", resp.Results[0].Chat, resp.Results[1].Chat)
	}
	for _, r := range resp.Results {
		if !r.Queued || r.RequestID == "" {
			t.Errorf("result = %+v, want queued with request_id", r)
		}
	}
}

func TestEnqueue_SingleChat_UnchangedHumanOutput(t *testing.T) {
	testFakeQueue.Reset()
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.Stdin = strings.NewReader("")
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-00000000000a",
		"--routing-mode", "direct",
		"single",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := testFakeQueue.WorkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 work message, got %d", len(msgs))
	}
	// Single-chat human output is one line = the request_id (backward compatible).
	out := strings.TrimSpace(stdout.String())
	if out != msgs[0].RequestID {
		t.Errorf("output = %q, want single request_id %q", out, msgs[0].RequestID)
	}
}

// TestEnqueue_MultiChat_ValidationFailPublishesNothing locks in the all-or-nothing
// phase-1 resolve pass: if any chat fails routing validation the command returns
// before publishing anything, so a retry never double-publishes the good chats.
func TestEnqueue_MultiChat_ValidationFailPublishesNothing(t *testing.T) {
	testFakeQueue.Reset()
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfake
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.Stdin = strings.NewReader("")
	deps.IsTerminal = true

	// First chat is a valid UUID, second is not — direct mode rejects the second.
	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", "00000000-0000-0000-0000-00000000000a,not-a-uuid",
		"--routing-mode", "direct",
		"should not publish",
	}, deps)
	if err == nil {
		t.Fatal("expected error for invalid second chat, got nil")
	}
	if msgs := testFakeQueue.WorkMessages(); len(msgs) != 0 {
		t.Fatalf("expected 0 published messages on validation failure, got %d", len(msgs))
	}
	if out := strings.TrimSpace(stdout.String()); out != "" {
		t.Errorf("expected no stdout on validation failure, got %q", out)
	}
}

// TestEnqueue_MultiChat_PartialPublishFail verifies best-effort phase-2 publish:
// a broker failure on one chat is recorded while the other chat still publishes,
// and the command exits zero (at least one chat succeeded).
func TestEnqueue_MultiChat_PartialPublishFail(t *testing.T) {
	chatA := "00000000-0000-0000-0000-00000000000a"
	chatB := "00000000-0000-0000-0000-00000000000b"
	testFailQueue.reset(chatB) // chatB publish fails, chatA succeeds
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfailpub
  url: fake://localhost
  name: test-work
`)
	deps, stdout, _ := testDeps()
	deps.Stdin = strings.NewReader("")
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", chatA + "," + chatB,
		"--routing-mode", "direct",
		"--format", "json",
		"partial",
	}, deps)
	if err != nil {
		t.Fatalf("expected nil error when one chat succeeds, got %v", err)
	}

	// Only chatA reached the broker; chatB was not re-attempted or dropped silently.
	if got := testFailQueue.publishedChats(); len(got) != 1 || got[0] != chatA {
		t.Fatalf("published chats = %v, want [%s] only", got, chatA)
	}

	var resp struct {
		OK      bool `json:"ok"`
		Results []struct {
			Chat      string `json:"chat"`
			RequestID string `json:"request_id"`
		} `json:"results"`
		Errors []struct {
			Chat  string `json:"chat"`
			Error string `json:"error"`
		} `json:"errors"`
	}
	if uerr := json.Unmarshal(stdout.Bytes(), &resp); uerr != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", uerr, stdout.String())
	}
	if !resp.OK {
		t.Errorf("ok = false, want true (chatA succeeded)")
	}
	if len(resp.Results) != 1 || resp.Results[0].Chat != chatA || resp.Results[0].RequestID == "" {
		t.Errorf("results = %+v, want single request_id for %s", resp.Results, chatA)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Chat != chatB {
		t.Errorf("errors = %+v, want single error for %s", resp.Errors, chatB)
	}
}

// TestEnqueue_MultiChat_AllPublishFail verifies the all-fail exit: when every
// chat's publish fails the command returns a non-nil error (non-zero exit).
func TestEnqueue_MultiChat_AllPublishFail(t *testing.T) {
	chatA := "00000000-0000-0000-0000-00000000000a"
	chatB := "00000000-0000-0000-0000-00000000000b"
	testFailQueue.reset(chatA, chatB)
	cfgPath := writeTestConfig(t, `
queue:
  driver: testfailpub
  url: fake://localhost
  name: test-work
`)
	deps, _, _ := testDeps()
	deps.Stdin = strings.NewReader("")
	deps.IsTerminal = true

	err := runEnqueue([]string{
		"--config", cfgPath,
		"--bot-id", "00000000-0000-0000-0000-000000000b01",
		"--chat-id", chatA + "," + chatB,
		"--routing-mode", "direct",
		"all fail",
	}, deps)
	if err == nil {
		t.Fatal("expected non-nil error when every chat fails, got nil")
	}
	if got := testFailQueue.publishedChats(); len(got) != 0 {
		t.Errorf("published chats = %v, want none", got)
	}
}
