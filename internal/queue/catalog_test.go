package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/config"
)

func TestBuildCatalogSnapshot_NoSecrets(t *testing.T) {
	cfg := &config.Config{
		Bots: map[string]config.BotConfig{
			"alerts": {
				Host:   "express.company.ru",
				ID:     "bot-uuid-001",
				Secret: "super-secret-value",
				Token:  "super-secret-token",
			},
			"monitoring": {
				Host:   "express2.company.ru",
				ID:     "bot-uuid-002",
				Secret: "another-secret",
			},
		},
		Chats: map[string]config.ChatConfig{
			"deploy": {ID: "chat-uuid-001", Bot: "alerts"},
			"general": {ID: "chat-uuid-002", Default: true},
		},
	}

	snap := BuildCatalogSnapshot(cfg)

	if snap.Type != "catalog.snapshot" {
		t.Errorf("Type = %q, want %q", snap.Type, "catalog.snapshot")
	}
	if snap.Revision == "" {
		t.Error("Revision should not be empty")
	}
	if snap.GeneratedAt.IsZero() {
		t.Error("GeneratedAt should not be zero")
	}

	// Verify bots contain only public fields
	if len(snap.Bots) != 2 {
		t.Fatalf("expected 2 bots, got %d", len(snap.Bots))
	}

	// Marshal to JSON and verify no secrets leak
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	jsonStr := string(data)

	secretValues := []string{"super-secret-value", "super-secret-token", "another-secret"}
	for _, secret := range secretValues {
		if contains(jsonStr, secret) {
			t.Errorf("snapshot JSON contains secret %q — secrets must not be included", secret)
		}
	}

	// Verify bot entries have expected public fields
	botMap := make(map[string]config.BotEntry)
	for _, b := range snap.Bots {
		botMap[b.Name] = b
	}
	if b, ok := botMap["alerts"]; !ok {
		t.Error("missing bot 'alerts'")
	} else {
		if b.Host != "express.company.ru" {
			t.Errorf("alerts.Host = %q", b.Host)
		}
		if b.ID != "bot-uuid-001" {
			t.Errorf("alerts.ID = %q", b.ID)
		}
	}

	// Verify chats
	if len(snap.Chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(snap.Chats))
	}
	chatMap := make(map[string]config.ChatEntry)
	for _, c := range snap.Chats {
		chatMap[c.Name] = c
	}
	if c, ok := chatMap["deploy"]; !ok {
		t.Error("missing chat 'deploy'")
	} else {
		if c.ID != "chat-uuid-001" {
			t.Errorf("deploy.ID = %q", c.ID)
		}
		if c.Bot != "alerts" {
			t.Errorf("deploy.Bot = %q", c.Bot)
		}
	}
	if c, ok := chatMap["general"]; !ok {
		t.Error("missing chat 'general'")
	} else if !c.Default {
		t.Error("general.Default should be true")
	}
}

func TestBuildCatalogSnapshot_EmptyConfig(t *testing.T) {
	cfg := &config.Config{}
	snap := BuildCatalogSnapshot(cfg)

	if snap.Type != "catalog.snapshot" {
		t.Errorf("Type = %q", snap.Type)
	}
	if len(snap.Bots) != 0 {
		t.Errorf("expected 0 bots, got %d", len(snap.Bots))
	}
	if len(snap.Chats) != 0 {
		t.Errorf("expected 0 chats, got %d", len(snap.Chats))
	}
}

func TestCatalogCache_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")

	// Create cache and store a snapshot
	cache := NewCatalogCache(path, 0)
	snap := &CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "test-rev-1",
		GeneratedAt: time.Now().UTC(),
		Bots: []config.BotEntry{
			{Name: "alerts", Host: "h.com", ID: "bot-1"},
		},
		Chats: []config.ChatEntry{
			{Name: "deploy", ID: "chat-1", Bot: "alerts"},
		},
	}
	cache.Update(snap)

	// Verify in-memory
	got := cache.Get()
	if got == nil {
		t.Fatal("expected snapshot in memory")
	}
	if got.Revision != "test-rev-1" {
		t.Errorf("Revision = %q", got.Revision)
	}

	// Verify file was written
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("cache file was not written to disk")
	}

	// Create a new cache from disk — should load the snapshot
	cache2 := NewCatalogCache(path, 0)
	got2 := cache2.Get()
	if got2 == nil {
		t.Fatal("expected snapshot loaded from disk")
	}
	if got2.Revision != "test-rev-1" {
		t.Errorf("loaded Revision = %q", got2.Revision)
	}
	if len(got2.Bots) != 1 || got2.Bots[0].Name != "alerts" {
		t.Errorf("loaded Bots = %+v", got2.Bots)
	}
	if len(got2.Chats) != 1 || got2.Chats[0].Name != "deploy" {
		t.Errorf("loaded Chats = %+v", got2.Chats)
	}
}

func TestCatalogCache_MaxAge(t *testing.T) {
	cache := NewCatalogCache("", 50*time.Millisecond)

	snap := &CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "rev-age",
		GeneratedAt: time.Now().UTC(),
	}
	cache.Update(snap)

	// Should be valid immediately
	if cache.Get() == nil {
		t.Fatal("expected valid snapshot immediately after update")
	}

	// Wait for expiry
	time.Sleep(60 * time.Millisecond)

	if cache.Get() != nil {
		t.Fatal("expected nil after max_age expiry")
	}
}

func TestCatalogCache_EmptyPath(t *testing.T) {
	// No disk path — in-memory only
	cache := NewCatalogCache("", 0)

	if cache.Get() != nil {
		t.Fatal("expected nil for empty cache")
	}

	snap := &CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "mem-only",
		GeneratedAt: time.Now().UTC(),
	}
	cache.Update(snap)

	got := cache.Get()
	if got == nil || got.Revision != "mem-only" {
		t.Errorf("expected in-memory snapshot, got %+v", got)
	}
}

func TestCatalogCache_LoadFromDisk_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	cache := NewCatalogCache(path, 0)
	if cache.Get() != nil {
		t.Fatal("expected nil when disk file doesn't exist")
	}
}

func TestCatalogCache_LoadFromDisk_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")
	os.WriteFile(path, []byte("not json at all"), 0o644)

	cache := NewCatalogCache(path, 0)
	if cache.Get() != nil {
		t.Fatal("expected nil when disk file is corrupt")
	}
}

func TestCatalogCache_HasValidSnapshot(t *testing.T) {
	cache := NewCatalogCache("", 0)
	if cache.HasValidSnapshot() {
		t.Fatal("expected false for empty cache")
	}

	cache.Update(&CatalogSnapshot{
		Type:        "catalog.snapshot",
		Revision:    "r",
		GeneratedAt: time.Now().UTC(),
	})
	if !cache.HasValidSnapshot() {
		t.Fatal("expected true after update")
	}
}

func testSnapshot() *CatalogSnapshot {
	return &CatalogSnapshot{
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

func TestCatalogSnapshot_ResolveBot(t *testing.T) {
	snap := testSnapshot()

	bot, err := snap.ResolveBot("alerts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bot.ID != "bot-uuid-alerts" {
		t.Errorf("ID = %q, want %q", bot.ID, "bot-uuid-alerts")
	}
	if bot.Host != "express.company.ru" {
		t.Errorf("Host = %q", bot.Host)
	}
}

func TestCatalogSnapshot_ResolveBot_Unknown(t *testing.T) {
	snap := testSnapshot()

	_, err := snap.ResolveBot("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !searchSubstring(err.Error(), "unknown bot") {
		t.Errorf("expected 'unknown bot' error, got: %v", err)
	}
	// Should list available bots
	if !searchSubstring(err.Error(), "alerts") {
		t.Errorf("expected error to mention available bots, got: %v", err)
	}
}

func TestCatalogSnapshot_ResolveBot_EmptyCatalog(t *testing.T) {
	snap := &CatalogSnapshot{Bots: nil}

	_, err := snap.ResolveBot("any")
	if err == nil {
		t.Fatal("expected error")
	}
	if !searchSubstring(err.Error(), "no bots") {
		t.Errorf("expected 'no bots' error, got: %v", err)
	}
}

func TestCatalogSnapshot_ResolveChat(t *testing.T) {
	snap := testSnapshot()

	chat, err := snap.ResolveChat("deploy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.ID != "chat-uuid-deploy" {
		t.Errorf("ID = %q, want %q", chat.ID, "chat-uuid-deploy")
	}
	if chat.Bot != "alerts" {
		t.Errorf("Bot = %q, want %q", chat.Bot, "alerts")
	}
}

func TestCatalogSnapshot_ResolveChat_Unknown(t *testing.T) {
	snap := testSnapshot()

	_, err := snap.ResolveChat("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !searchSubstring(err.Error(), "unknown chat alias") {
		t.Errorf("expected 'unknown chat alias' error, got: %v", err)
	}
}

func TestCatalogSnapshot_DefaultChat(t *testing.T) {
	snap := testSnapshot()

	chat, ok := snap.DefaultChat()
	if !ok {
		t.Fatal("expected default chat")
	}
	if chat.Name != "general" {
		t.Errorf("Name = %q, want %q", chat.Name, "general")
	}
	if chat.ID != "chat-uuid-general" {
		t.Errorf("ID = %q", chat.ID)
	}
}

func TestCatalogSnapshot_DefaultChat_None(t *testing.T) {
	snap := &CatalogSnapshot{
		Chats: []config.ChatEntry{
			{Name: "deploy", ID: "chat-1"},
		},
	}

	_, ok := snap.DefaultChat()
	if ok {
		t.Fatal("expected no default chat")
	}
}

func TestCatalogSnapshot_ResolveBotByID(t *testing.T) {
	snap := testSnapshot()

	bot, ok := snap.ResolveBotByID("bot-uuid-alerts")
	if !ok {
		t.Fatal("expected bot found")
	}
	if bot.Name != "alerts" {
		t.Errorf("Name = %q, want %q", bot.Name, "alerts")
	}
}

func TestCatalogSnapshot_ResolveBotByID_NotFound(t *testing.T) {
	snap := testSnapshot()

	_, ok := snap.ResolveBotByID("nonexistent-id")
	if ok {
		t.Fatal("expected bot not found")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
