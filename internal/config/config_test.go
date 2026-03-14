package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_FromYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: express.example.com
    id: bot-123
    secret: my-secret
chats:
  deploy: chat-456
cache:
  type: file
  file_path: /tmp/token.cache
  ttl: 1800
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Host != "express.example.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "express.example.com")
	}
	if cfg.BotID != "bot-123" {
		t.Errorf("BotID = %q, want %q", cfg.BotID, "bot-123")
	}
	if cfg.BotSecret != "my-secret" {
		t.Errorf("BotSecret = %q, want %q", cfg.BotSecret, "my-secret")
	}
	if cfg.BotName != "main" {
		t.Errorf("BotName = %q, want %q", cfg.BotName, "main")
	}
	if cfg.Cache.Type != "file" {
		t.Errorf("Cache.Type = %q, want %q", cfg.Cache.Type, "file")
	}
	if cfg.Cache.TTL != 1800 {
		t.Errorf("Cache.TTL = %d, want %d", cfg.Cache.TTL, 1800)
	}
}

func TestLoad_EnvOverridesBot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: from-yaml.com
    id: yaml-bot
    secret: yaml-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EXPRESS_HOST", "from-env.com")
	t.Setenv("EXPRESS_BOT_ID", "env-bot")

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Host != "from-env.com" {
		t.Errorf("Host = %q, want %q (env should override bot)", cfg.Host, "from-env.com")
	}
	if cfg.BotID != "env-bot" {
		t.Errorf("BotID = %q, want %q (env should override bot)", cfg.BotID, "env-bot")
	}
	if cfg.BotSecret != "yaml-secret" {
		t.Errorf("BotSecret = %q, want %q", cfg.BotSecret, "yaml-secret")
	}
}

func TestLoad_FlagsOverrideAll(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: from-yaml.com
    id: yaml-bot
    secret: yaml-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EXPRESS_HOST", "from-env.com")

	cfg, err := Load(Flags{
		ConfigPath: cfgPath,
		Host:       "from-flag.com",
	})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Host != "from-flag.com" {
		t.Errorf("Host = %q, want %q (flag should override all)", cfg.Host, "from-flag.com")
	}
}

func TestLoad_MultipleBots_RequiresFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for multiple bots without --bot")
	}
	if !strings.Contains(err.Error(), "multiple bots") {
		t.Errorf("error = %q, should mention multiple bots", err.Error())
	}
}

func TestLoad_MultipleBots_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EXPRESS_HOST", "env.com")
	t.Setenv("EXPRESS_BOT_ID", "env-bot")
	t.Setenv("EXPRESS_SECRET", "env-secret")

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() should succeed when env provides full credentials: %v", err)
	}
	if cfg.Host != "env.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "env.com")
	}
	if cfg.BotID != "env-bot" {
		t.Errorf("BotID = %q, want %q", cfg.BotID, "env-bot")
	}
}

func TestLoad_MultipleBots_PartialEnv_StillErrors(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Only partial env — should still error
	t.Setenv("EXPRESS_HOST", "env.com")

	_, err := Load(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for multiple bots with partial env")
	}
	if !strings.Contains(err.Error(), "multiple bots") {
		t.Errorf("error = %q, should mention multiple bots", err.Error())
	}
}

func TestLoad_MultipleBots_WithFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: cfgPath, Bot: "prod"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Host != "prod.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "prod.com")
	}
	if cfg.BotID != "prod-bot" {
		t.Errorf("BotID = %q, want %q", cfg.BotID, "prod-bot")
	}
	if cfg.BotName != "prod" {
		t.Errorf("BotName = %q, want %q", cfg.BotName, "prod")
	}
}

func TestLoad_UnknownBot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(Flags{ConfigPath: cfgPath, Bot: "staging"})
	if err == nil {
		t.Fatal("expected error for unknown bot")
	}
	if !strings.Contains(err.Error(), "staging") {
		t.Errorf("error = %q, should mention staging", err.Error())
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	tests := []struct {
		name  string
		flags Flags
		want  string
	}{
		{"no host", Flags{BotID: "b", Secret: "s"}, "host is required"},
		{"no bot_id", Flags{Host: "h", Secret: "s"}, "bot id is required"},
		{"no secret", Flags{Host: "h", BotID: "b"}, "bot secret is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(tt.flags)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := err.Error(); !strings.Contains(got, tt.want) {
				t.Errorf("error = %q, should contain %q", got, tt.want)
			}
		})
	}
}

func TestCacheKey(t *testing.T) {
	cfg := &Config{Host: "express.example.com", BotID: "bot-123"}
	want := "express.example.com:bot-123"
	if got := cfg.CacheKey(); got != want {
		t.Errorf("CacheKey() = %q, want %q", got, want)
	}
}

func TestRequireChatID(t *testing.T) {
	cfg := &Config{ChatID: ""}
	if err := cfg.RequireChatID(); err == nil {
		t.Fatal("expected error for empty ChatID")
	}

	cfg.ChatID = "some-chat"
	cfg.Chats = map[string]string{"some-chat": "uuid-123"}
	if err := cfg.RequireChatID(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ChatID != "uuid-123" {
		t.Errorf("ChatID = %q, want resolved %q", cfg.ChatID, "uuid-123")
	}
}

func TestRequireChatID_SingleAlias(t *testing.T) {
	cfg := &Config{Chats: map[string]string{"deploy": "uuid-456"}}
	if err := cfg.RequireChatID(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ChatID != "uuid-456" {
		t.Errorf("ChatID = %q, want %q", cfg.ChatID, "uuid-456")
	}
}

func TestLoad_NoCacheFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: s
cache:
  type: file
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: cfgPath, NoCache: true})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cache.Type != "none" {
		t.Errorf("Cache.Type = %q, want %q (--no-cache should disable)", cfg.Cache.Type, "none")
	}
}

func TestLoad_MissingConfigFile_NotExplicit(t *testing.T) {
	cfg, err := Load(Flags{
		Host:   "h",
		BotID:  "b",
		Secret: "s",
	})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Host != "h" {
		t.Errorf("Host = %q, want %q", cfg.Host, "h")
	}
}

func TestLoad_MissingConfigFile_Explicit(t *testing.T) {
	_, err := Load(Flags{ConfigPath: "/nonexistent/config.yaml"})
	if err == nil {
		t.Fatal("expected error for explicitly specified missing config")
	}
}

func TestLoad_DefaultCacheTTL(t *testing.T) {
	cfg, err := Load(Flags{
		Host:   "h",
		BotID:  "b",
		Secret: "s",
	})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cache.TTL != 31536000 {
		t.Errorf("Cache.TTL = %d, want 31536000 (default)", cfg.Cache.TTL)
	}
}

// --- ServerConfig ---

func TestLoad_ServerConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: s
server:
  listen: ":9090"
  base_path: "/custom/api"
  allow_bot_secret_auth: true
  api_keys:
    - name: monitoring
      key: mon-key
    - name: ci
      key: ci-key
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Server.Listen != ":9090" {
		t.Errorf("Server.Listen = %q, want %q", cfg.Server.Listen, ":9090")
	}
	if cfg.Server.BasePath != "/custom/api" {
		t.Errorf("Server.BasePath = %q, want %q", cfg.Server.BasePath, "/custom/api")
	}
	if !cfg.Server.AllowBotSecretAuth {
		t.Error("Server.AllowBotSecretAuth = false, want true")
	}
	if len(cfg.Server.APIKeys) != 2 {
		t.Fatalf("Server.APIKeys length = %d, want 2", len(cfg.Server.APIKeys))
	}
	if cfg.Server.APIKeys[0].Name != "monitoring" || cfg.Server.APIKeys[0].Key != "mon-key" {
		t.Errorf("APIKeys[0] = %+v, want {monitoring, mon-key}", cfg.Server.APIKeys[0])
	}
	if cfg.Server.APIKeys[1].Name != "ci" || cfg.Server.APIKeys[1].Key != "ci-key" {
		t.Errorf("APIKeys[1] = %+v, want {ci, ci-key}", cfg.Server.APIKeys[1])
	}
}

func TestLoad_ServerConfig_Empty(t *testing.T) {
	cfg, err := Load(Flags{Host: "h", BotID: "b", Secret: "s"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Listen != "" {
		t.Errorf("Server.Listen = %q, want empty", cfg.Server.Listen)
	}
	if len(cfg.Server.APIKeys) != 0 {
		t.Errorf("Server.APIKeys = %v, want empty", cfg.Server.APIKeys)
	}
}

// --- ValidateFormat ---

func TestValidateFormat(t *testing.T) {
	tests := []struct {
		format string
		ok     bool
	}{
		{"text", true},
		{"json", true},
		{"xml", false},
		{"", false},
	}
	for _, tt := range tests {
		cfg := &Config{Format: tt.format}
		err := cfg.ValidateFormat()
		if tt.ok && err != nil {
			t.Errorf("ValidateFormat(%q) = %v, want nil", tt.format, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateFormat(%q) = nil, want error", tt.format)
		}
	}
}

// --- ResolveChatID ---

func TestResolveChatID_UUID(t *testing.T) {
	cfg := &Config{ChatID: "a1b2c3d4-e5f6-7890-abcd-ef1234567890"}
	if err := cfg.ResolveChatID(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ChatID != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("ChatID changed unexpectedly: %s", cfg.ChatID)
	}
}

func TestResolveChatID_Empty(t *testing.T) {
	cfg := &Config{}
	if err := cfg.ResolveChatID(); err != nil {
		t.Fatalf("unexpected error for empty ChatID: %v", err)
	}
}

func TestResolveChatID_UnknownAlias_NoAliases(t *testing.T) {
	cfg := &Config{ChatID: "unknown"}
	err := cfg.ResolveChatID()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no aliases configured") {
		t.Errorf("expected 'no aliases configured', got: %v", err)
	}
}

func TestResolveChatID_UnknownAlias_WithAliases(t *testing.T) {
	cfg := &Config{
		ChatID: "unknown",
		Chats:  map[string]string{"deploy": "uuid-1", "alerts": "uuid-2"},
	}
	err := cfg.ResolveChatID()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "alerts") || !strings.Contains(err.Error(), "deploy") {
		t.Errorf("expected available aliases in error, got: %v", err)
	}
}

// --- RequireChatID multiple aliases ---

func TestRequireChatID_MultipleAliases_NoChatID(t *testing.T) {
	cfg := &Config{
		Chats: map[string]string{"a": "uuid-1", "b": "uuid-2"},
	}
	err := cfg.RequireChatID()
	if err == nil {
		t.Fatal("expected error for multiple aliases without ChatID")
	}
	if !strings.Contains(err.Error(), "multiple chats") {
		t.Errorf("expected 'multiple chats', got: %v", err)
	}
}

// --- SaveConfig round-trip ---

func TestSaveConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Bots: map[string]BotConfig{
			"prod": {Host: "prod.com", ID: "prod-id", Secret: "prod-s"},
		},
		Chats: map[string]string{"deploy": "uuid-d"},
		Cache: CacheConfig{Type: "file", TTL: 1800},
		Server: ServerConfig{
			Listen:   ":9090",
			BasePath: "/v2",
			APIKeys:  []APIKeyConfig{{Name: "test", Key: "test-key"}},
		},
	}
	cfg.configPath = cfgPath

	if err := cfg.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	loaded, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.Host != "prod.com" {
		t.Errorf("Host = %q, want %q", loaded.Host, "prod.com")
	}
	if loaded.Server.Listen != ":9090" {
		t.Errorf("Server.Listen = %q, want %q", loaded.Server.Listen, ":9090")
	}
	if loaded.Server.BasePath != "/v2" {
		t.Errorf("Server.BasePath = %q, want %q", loaded.Server.BasePath, "/v2")
	}
	if len(loaded.Server.APIKeys) != 1 || loaded.Server.APIKeys[0].Key != "test-key" {
		t.Errorf("Server.APIKeys = %+v, want [{test test-key}]", loaded.Server.APIKeys)
	}
}

// --- LoadMinimal ---

func TestLoadMinimal_DefaultFormat(t *testing.T) {
	cfg, err := LoadMinimal(Flags{})
	if err != nil {
		t.Fatalf("LoadMinimal() error: %v", err)
	}
	if cfg.Format != "text" {
		t.Errorf("Format = %q, want %q", cfg.Format, "text")
	}
}

func TestLoadMinimal_FormatOverride(t *testing.T) {
	cfg, err := LoadMinimal(Flags{Format: "json"})
	if err != nil {
		t.Fatalf("LoadMinimal() error: %v", err)
	}
	if cfg.Format != "json" {
		t.Errorf("Format = %q, want %q", cfg.Format, "json")
	}
}

// --- Env overrides for cache ---

func TestLoad_EnvCacheTTL(t *testing.T) {
	t.Setenv("EXPRESS_CACHE_TTL", "600")

	cfg, err := Load(Flags{Host: "h", BotID: "b", Secret: "s"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cache.TTL != 600 {
		t.Errorf("Cache.TTL = %d, want 600", cfg.Cache.TTL)
	}
}

// --- LoadForServe ---

func TestLoadForServe_MultipleBots_NoFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForServe(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForServe() should not error for multi-bot: %v", err)
	}
	if !cfg.IsMultiBot() {
		t.Fatal("expected IsMultiBot() = true")
	}
	if len(cfg.Bots) != 2 {
		t.Errorf("expected 2 bots, got %d", len(cfg.Bots))
	}
	// Host/BotID/BotSecret should NOT be resolved
	if cfg.BotID != "" {
		t.Errorf("BotID should be empty in multi-bot mode, got %q", cfg.BotID)
	}
}

func TestLoadForServe_MultipleBots_WithFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForServe(Flags{ConfigPath: cfgPath, Bot: "prod"})
	if err != nil {
		t.Fatalf("LoadForServe() error: %v", err)
	}
	if cfg.IsMultiBot() {
		t.Fatal("expected IsMultiBot() = false when --bot specified")
	}
	if cfg.Host != "prod.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "prod.com")
	}
}

func TestLoadForServe_SingleBot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: s
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForServe(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForServe() error: %v", err)
	}
	if cfg.IsMultiBot() {
		t.Fatal("expected IsMultiBot() = false for single bot")
	}
	if cfg.Host != "h" {
		t.Errorf("Host = %q, want %q", cfg.Host, "h")
	}
}

func TestLoadForServe_EnvOverridesCollapsesToSingleBot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// If env provides full bot credentials, multi-bot should collapse to single-bot
	t.Setenv("EXPRESS_HOST", "env.com")
	t.Setenv("EXPRESS_BOT_ID", "env-bot")
	t.Setenv("EXPRESS_SECRET", "env-secret")

	cfg, err := LoadForServe(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForServe() error: %v", err)
	}
	if cfg.IsMultiBot() {
		t.Fatal("expected IsMultiBot() = false when env provides full credentials")
	}
	if cfg.Host != "env.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "env.com")
	}
}

func TestLoadForServe_PartialEnvStaysMultiBot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  prod:
    host: prod.com
    id: prod-bot
    secret: prod-secret
  test:
    host: test.com
    id: test-bot
    secret: test-secret
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Only partial env — should stay multi-bot
	t.Setenv("EXPRESS_HOST", "env.com")

	cfg, err := LoadForServe(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForServe() error: %v", err)
	}
	if !cfg.IsMultiBot() {
		t.Fatal("expected IsMultiBot() = true when env only partially overrides")
	}
}

func TestBotNames(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"prod": {Host: "p"},
			"test": {Host: "t"},
			"dev":  {Host: "d"},
		},
	}
	names := cfg.BotNames()
	want := []string{"dev", "prod", "test"}
	if len(names) != len(want) {
		t.Fatalf("BotNames() = %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("BotNames()[%d] = %q, want %q", i, n, want[i])
		}
	}
}

func TestLoad_EnvCacheType(t *testing.T) {
	t.Setenv("EXPRESS_CACHE_TYPE", "file")

	cfg, err := Load(Flags{Host: "h", BotID: "b", Secret: "s"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cache.Type != "file" {
		t.Errorf("Cache.Type = %q, want %q", cfg.Cache.Type, "file")
	}
}
