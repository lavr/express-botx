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

	t.Setenv("EXPRESS_BOTX_HOST", "from-env.com")
	t.Setenv("EXPRESS_BOTX_BOT_ID", "env-bot")

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

	t.Setenv("EXPRESS_BOTX_HOST", "from-env.com")

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

func TestLoad_EnvOverridesClearsStaleBotName(t *testing.T) {
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

	// Env overrides credentials — BotName should be cleared
	t.Setenv("EXPRESS_BOTX_HOST", "other.com")
	t.Setenv("EXPRESS_BOTX_BOT_ID", "other-bot")
	t.Setenv("EXPRESS_BOTX_SECRET", "other-secret")

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BotName != "" {
		t.Errorf("BotName = %q, want empty (credentials overridden by env)", cfg.BotName)
	}
	if cfg.Host != "other.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "other.com")
	}
}

func TestLoad_EnvPartialOverrideKeepsBotName(t *testing.T) {
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

	// Env does NOT override any credential fields — BotName stays
	t.Setenv("EXPRESS_BOTX_CACHE_TYPE", "none")

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BotName != "prod" {
		t.Errorf("BotName = %q, want %q (no credential override)", cfg.BotName, "prod")
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

	t.Setenv("EXPRESS_BOTX_HOST", "env.com")
	t.Setenv("EXPRESS_BOTX_BOT_ID", "env-bot")
	t.Setenv("EXPRESS_BOTX_SECRET", "env-secret")

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
	t.Setenv("EXPRESS_BOTX_HOST", "env.com")

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
		{"no secret", Flags{Host: "h", BotID: "b"}, "bot secret or token is required"},
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
	cfg.Chats = map[string]ChatConfig{"some-chat": {ID: "uuid-123"}}
	if err := cfg.RequireChatID(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ChatID != "uuid-123" {
		t.Errorf("ChatID = %q, want resolved %q", cfg.ChatID, "uuid-123")
	}
}

func TestRequireChatID_SingleAlias(t *testing.T) {
	cfg := &Config{Chats: map[string]ChatConfig{"deploy": {ID: "uuid-456"}}}
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
		Chats:  map[string]ChatConfig{"deploy": {ID: "uuid-1"}, "alerts": {ID: "uuid-2"}},
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
		Chats: map[string]ChatConfig{"a": {ID: "uuid-1"}, "b": {ID: "uuid-2"}},
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
		Chats: map[string]ChatConfig{"deploy": {ID: "uuid-d"}},
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
	t.Setenv("EXPRESS_BOTX_CACHE_TTL", "600")

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
	t.Setenv("EXPRESS_BOTX_HOST", "env.com")
	t.Setenv("EXPRESS_BOTX_BOT_ID", "env-bot")
	t.Setenv("EXPRESS_BOTX_SECRET", "env-secret")

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
	t.Setenv("EXPRESS_BOTX_HOST", "env.com")

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
	t.Setenv("EXPRESS_BOTX_CACHE_TYPE", "file")

	cfg, err := Load(Flags{Host: "h", BotID: "b", Secret: "s"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cache.Type != "file" {
		t.Errorf("Cache.Type = %q, want %q", cfg.Cache.Type, "file")
	}
}

// --- ChatConfig YAML ---

func TestChatConfig_UnmarshalYAML_ShortForm(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: s
chats:
  deploy: a1b2c3d4-e5f6-7890-abcd-ef1234567890
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	chat := cfg.Chats["deploy"]
	if chat.ID != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("chat.ID = %q", chat.ID)
	}
	if chat.Bot != "" {
		t.Errorf("chat.Bot = %q, want empty", chat.Bot)
	}
}

func TestChatConfig_UnmarshalYAML_LongForm(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  deploy-bot:
    host: h
    id: b
    secret: s
chats:
  deploy:
    id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: deploy-bot
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	chat := cfg.Chats["deploy"]
	if chat.ID != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("chat.ID = %q", chat.ID)
	}
	if chat.Bot != "deploy-bot" {
		t.Errorf("chat.Bot = %q, want %q", chat.Bot, "deploy-bot")
	}
}

func TestChatConfig_MarshalYAML_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Bots: map[string]BotConfig{
			"main": {Host: "h", ID: "b", Secret: "s"},
		},
		Chats: map[string]ChatConfig{
			"deploy":  {ID: "uuid-deploy", Bot: "main"},
			"general": {ID: "uuid-general"},
		},
		Cache: CacheConfig{Type: "file", TTL: 3600},
	}
	cfg.configPath = cfgPath

	if err := cfg.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	loaded, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Chats["deploy"].ID != "uuid-deploy" {
		t.Errorf("deploy.ID = %q", loaded.Chats["deploy"].ID)
	}
	if loaded.Chats["deploy"].Bot != "main" {
		t.Errorf("deploy.Bot = %q, want %q", loaded.Chats["deploy"].Bot, "main")
	}
	if loaded.Chats["general"].ID != "uuid-general" {
		t.Errorf("general.ID = %q", loaded.Chats["general"].ID)
	}
	if loaded.Chats["general"].Bot != "" {
		t.Errorf("general.Bot = %q, want empty", loaded.Chats["general"].Bot)
	}
}

func TestResolveChatIDWithBot(t *testing.T) {
	cfg := &Config{
		ChatID: "deploy",
		Chats: map[string]ChatConfig{
			"deploy": {ID: "uuid-deploy", Bot: "deploy-bot"},
		},
	}
	botName, err := cfg.ResolveChatIDWithBot()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cfg.ChatID != "uuid-deploy" {
		t.Errorf("ChatID = %q, want %q", cfg.ChatID, "uuid-deploy")
	}
	if botName != "deploy-bot" {
		t.Errorf("botName = %q, want %q", botName, "deploy-bot")
	}
}

func TestResolveChatIDWithBot_NoBotBinding(t *testing.T) {
	cfg := &Config{
		ChatID: "general",
		Chats: map[string]ChatConfig{
			"general": {ID: "uuid-general"},
		},
	}
	botName, err := cfg.ResolveChatIDWithBot()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if botName != "" {
		t.Errorf("botName = %q, want empty", botName)
	}
}

func TestValidateChatBots_Valid(t *testing.T) {
	cfg := &Config{
		Bots:  map[string]BotConfig{"prod": {Host: "h"}},
		Chats: map[string]ChatConfig{"deploy": {ID: "uuid", Bot: "prod"}},
	}
	if err := cfg.ValidateChatBots(true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateChatBots_Invalid_Strict(t *testing.T) {
	cfg := &Config{
		Bots:  map[string]BotConfig{"prod": {Host: "h"}},
		Chats: map[string]ChatConfig{"deploy": {ID: "uuid", Bot: "nonexistent"}},
	}
	err := cfg.ValidateChatBots(true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error = %q, should mention nonexistent", err.Error())
	}
}

func TestValidateChatBots_Invalid_NonStrict(t *testing.T) {
	cfg := &Config{
		Bots:  map[string]BotConfig{"prod": {Host: "h"}},
		Chats: map[string]ChatConfig{"deploy": {ID: "uuid", Bot: "nonexistent"}},
	}
	if err := cfg.ValidateChatBots(false); err != nil {
		t.Fatalf("unexpected error in non-strict mode: %v", err)
	}
	// Bot binding should be cleared
	if cfg.Chats["deploy"].Bot != "" {
		t.Errorf("Bot should be cleared, got %q", cfg.Chats["deploy"].Bot)
	}
}

func TestApplyChatBot(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"prod": {Host: "prod.com", ID: "prod-id", Secret: "prod-s"},
		},
	}
	if err := cfg.ApplyChatBot("prod"); err != nil {
		t.Fatalf("error: %v", err)
	}
	if cfg.Host != "prod.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "prod.com")
	}
	if cfg.BotName != "prod" {
		t.Errorf("BotName = %q, want %q", cfg.BotName, "prod")
	}
}

func TestApplyChatBot_Unknown(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{"prod": {Host: "h"}},
	}
	err := cfg.ApplyChatBot("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoad_MultiBots_SingleChatWithBot_NoChatIDFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  deploy-bot:
    host: h
    id: b
    secret: s
  alert-bot:
    host: h2
    id: b2
    secret: s2
chats:
  deploy:
    id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: deploy-bot
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	// No --chat-id, no --bot — should auto-select from single chat alias binding
	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() should succeed with single chat-bound bot: %v", err)
	}
	if cfg.BotName != "deploy-bot" {
		t.Errorf("BotName = %q, want %q", cfg.BotName, "deploy-bot")
	}
	if cfg.Host != "h" {
		t.Errorf("Host = %q, want %q", cfg.Host, "h")
	}
}

func TestLoad_MultiBots_ChatIDFlagWithBot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  deploy-bot:
    host: h
    id: b
    secret: s
  alert-bot:
    host: h2
    id: b2
    secret: s2
chats:
  deploy:
    id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: deploy-bot
  alerts:
    id: b1b2c3d4-e5f6-7890-abcd-ef1234567890
    bot: alert-bot
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	// --chat-id=alerts → should pick alert-bot
	cfg, err := Load(Flags{ConfigPath: cfgPath, ChatID: "alerts"})
	if err != nil {
		t.Fatalf("Load() should succeed with chat-bound bot: %v", err)
	}
	if cfg.BotName != "alert-bot" {
		t.Errorf("BotName = %q, want %q", cfg.BotName, "alert-bot")
	}
}

// --- Static token ---

func TestLoad_TokenMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    token: my-static-token
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BotToken != "my-static-token" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "my-static-token")
	}
	if cfg.BotSecret != "" {
		t.Errorf("BotSecret = %q, want empty", cfg.BotSecret)
	}
}

func TestLoad_TokenAndSecret_Error(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: s
    token: t
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for bot with both secret and token")
	}
	if !strings.Contains(err.Error(), "both secret and token") {
		t.Errorf("error = %q, should mention both secret and token", err.Error())
	}
}

func TestLoad_CLITokenOverridesConfigSecret(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: config-secret
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(Flags{ConfigPath: cfgPath, Token: "cli-token"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BotToken != "cli-token" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "cli-token")
	}
	if cfg.BotSecret != "" {
		t.Errorf("BotSecret should be empty (overridden by CLI token), got %q", cfg.BotSecret)
	}
}

func TestLoad_EnvTokenOverridesConfigSecret(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: config-secret
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	t.Setenv("EXPRESS_BOTX_TOKEN", "env-token")

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BotToken != "env-token" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "env-token")
	}
	if cfg.BotSecret != "" {
		t.Errorf("BotSecret should be empty, got %q", cfg.BotSecret)
	}
}

func TestLoad_EnvSecretAndTokenConflict(t *testing.T) {
	t.Setenv("EXPRESS_BOTX_SECRET", "s")
	t.Setenv("EXPRESS_BOTX_TOKEN", "t")

	_, err := Load(Flags{Host: "h", BotID: "b"})
	if err == nil {
		t.Fatal("expected error for env secret+token conflict")
	}
	if !strings.Contains(err.Error(), "EXPRESS_BOTX_SECRET and EXPRESS_BOTX_TOKEN") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestLoad_MultiBotWithToken(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  secret-bot:
    host: h
    id: b1
    secret: s
  token-bot:
    host: h
    id: b2
    token: t
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	// --bot=token-bot should work
	cfg, err := Load(Flags{ConfigPath: cfgPath, Bot: "token-bot"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BotToken != "t" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "t")
	}
	if cfg.BotSecret != "" {
		t.Errorf("BotSecret = %q, want empty", cfg.BotSecret)
	}
}

func TestLoad_TokenViaFlags_MultiBot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  a:
    host: h1
    id: b1
    secret: s1
  b:
    host: h2
    id: b2
    secret: s2
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	// --host + --bot-id + --token should collapse to single-bot, not error
	cfg, err := Load(Flags{ConfigPath: cfgPath, Host: "h3", BotID: "b3", Token: "tok"})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.BotToken != "tok" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "tok")
	}
}

// --- Default chat ---

func TestChatConfig_UnmarshalYAML_DefaultFlag(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: s
chats:
  general:
    id: uuid-general
    default: true
  deploy: uuid-deploy
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.Chats["general"].Default {
		t.Error("general.Default = false, want true")
	}
	if cfg.Chats["deploy"].Default {
		t.Error("deploy.Default = true, want false")
	}
}

func TestChatConfig_MarshalYAML_DefaultUsesObjectForm(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &Config{
		Bots: map[string]BotConfig{
			"main": {Host: "h", ID: "b", Secret: "s"},
		},
		Chats: map[string]ChatConfig{
			"general": {ID: "uuid-general", Default: true},
			"deploy":  {ID: "uuid-deploy"},
		},
		Cache: CacheConfig{Type: "file", TTL: 3600},
	}
	cfg.configPath = cfgPath

	if err := cfg.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}

	loaded, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !loaded.Chats["general"].Default {
		t.Error("general.Default = false after round-trip, want true")
	}
	if loaded.Chats["deploy"].Default {
		t.Error("deploy.Default = true after round-trip, want false")
	}
}

func TestValidateDefaultChat_None(t *testing.T) {
	cfg := &Config{
		Chats: map[string]ChatConfig{
			"a": {ID: "uuid-a"},
			"b": {ID: "uuid-b"},
		},
	}
	if err := cfg.ValidateDefaultChat(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDefaultChat_One(t *testing.T) {
	cfg := &Config{
		Chats: map[string]ChatConfig{
			"a": {ID: "uuid-a", Default: true},
			"b": {ID: "uuid-b"},
		},
	}
	if err := cfg.ValidateDefaultChat(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDefaultChat_Multiple(t *testing.T) {
	cfg := &Config{
		Chats: map[string]ChatConfig{
			"a": {ID: "uuid-a", Default: true},
			"b": {ID: "uuid-b", Default: true},
		},
	}
	err := cfg.ValidateDefaultChat()
	if err == nil {
		t.Fatal("expected error for multiple defaults")
	}
	if !strings.Contains(err.Error(), "multiple chats marked as default") {
		t.Errorf("error = %q, should mention multiple defaults", err.Error())
	}
}

func TestDefaultChat_Found(t *testing.T) {
	cfg := &Config{
		Chats: map[string]ChatConfig{
			"a": {ID: "uuid-a"},
			"b": {ID: "uuid-b", Default: true},
		},
	}
	alias, chat := cfg.DefaultChat()
	if alias != "b" {
		t.Errorf("alias = %q, want %q", alias, "b")
	}
	if chat.ID != "uuid-b" {
		t.Errorf("chat.ID = %q, want %q", chat.ID, "uuid-b")
	}
}

func TestDefaultChat_NotFound(t *testing.T) {
	cfg := &Config{
		Chats: map[string]ChatConfig{
			"a": {ID: "uuid-a"},
			"b": {ID: "uuid-b"},
		},
	}
	alias, _ := cfg.DefaultChat()
	if alias != "" {
		t.Errorf("alias = %q, want empty", alias)
	}
}

func TestRequireChatIDWithBot_DefaultChat(t *testing.T) {
	cfg := &Config{
		Chats: map[string]ChatConfig{
			"a": {ID: "uuid-a"},
			"b": {ID: "uuid-b", Default: true, Bot: "some-bot"},
		},
	}
	botName, err := cfg.RequireChatIDWithBot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ChatID != "uuid-b" {
		t.Errorf("ChatID = %q, want %q", cfg.ChatID, "uuid-b")
	}
	if botName != "some-bot" {
		t.Errorf("botName = %q, want %q", botName, "some-bot")
	}
}

func TestRequireChatIDWithBot_NoDefault_MultipleChats(t *testing.T) {
	cfg := &Config{
		Chats: map[string]ChatConfig{
			"a": {ID: "uuid-a"},
			"b": {ID: "uuid-b"},
		},
	}
	_, err := cfg.RequireChatIDWithBot()
	if err == nil {
		t.Fatal("expected error for multiple chats without default")
	}
	if !strings.Contains(err.Error(), "multiple chats") {
		t.Errorf("error = %q, should mention multiple chats", err.Error())
	}
}

func TestValidateBotConfigs_MultiBotOneWithTokenAndSecret(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"good": {Host: "h", ID: "b", Token: "t"},
			"bad":  {Host: "h", ID: "b", Secret: "s", Token: "t"},
		},
	}
	err := cfg.validateBotConfigs()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should mention bad bot, got: %v", err)
	}
}

func TestLoad_MultipleDefaultChats_Error(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: express.example.com
    id: bot-123
    secret: my-secret
chats:
  alerts:
    id: chat-111
    default: true
  general:
    id: chat-222
    default: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for multiple default chats")
	}
	if !strings.Contains(err.Error(), "multiple chats marked as default") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_MultiBotDefaultChatBotBinding(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  deploy-bot:
    host: express.example.com
    id: bot-deploy
    secret: secret-deploy
  alert-bot:
    host: express.example.com
    id: bot-alert
    secret: secret-alert
chats:
  deploy:
    id: chat-deploy
    bot: deploy-bot
  alerts:
    id: chat-alerts
    bot: alert-bot
  general:
    id: chat-general
    bot: deploy-bot
    default: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() should succeed with default chat bot binding, got: %v", err)
	}
	if cfg.BotID != "bot-deploy" {
		t.Errorf("BotID = %q, want %q (from default chat's bot binding)", cfg.BotID, "bot-deploy")
	}
}

func TestLoadForServe_MultipleDefaultChats_Error(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: express.example.com
    id: bot-123
    secret: my-secret
chats:
  alerts:
    id: chat-111
    default: true
  general:
    id: chat-222
    default: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadForServe(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for multiple default chats")
	}
	if !strings.Contains(err.Error(), "multiple chats marked as default") {
		t.Errorf("unexpected error: %v", err)
	}
}
