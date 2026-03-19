package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/queue"
)

// testFakeQueue is a shared fake queue for enqueue tests.
// Registered as "testfake" driver so runEnqueue can create publishers via factory.
var testFakeQueue = queue.NewFake()

func init() {
	queue.Register("testfake", queue.DriverFactory{
		NewPublisher: func(url, name string) (queue.Publisher, error) {
			return testFakeQueue, nil
		},
		NewConsumer: func(url, name, group string) (queue.Consumer, error) {
			return testFakeQueue, nil
		},
	})
}

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
		OK        bool   `json:"ok"`
		Queued    bool   `json:"queued"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, stdout.String())
	}
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if !resp.Queued {
		t.Error("expected queued=true")
	}
	if resp.RequestID == "" {
		t.Error("expected non-empty request_id")
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
