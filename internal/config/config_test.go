package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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

// Auto-selecting an alias with no id must error like the explicit path, not
// silently set ChatID to "" and fail later with an opaque API error.
func TestRequireChatID_AutoSelectEmptyID(t *testing.T) {
	// Single-alias branch.
	single := &Config{Chats: map[string]ChatConfig{"ops": {Bot: "main"}}}
	if err := single.RequireChatID(); err == nil || !strings.Contains(err.Error(), `chat alias "ops" has no id`) {
		t.Fatalf("single-alias error = %v, want no-id error", err)
	}

	// Default-chat branch (multiple aliases, one marked default).
	def := &Config{Chats: map[string]ChatConfig{
		"ops":   {Bot: "main", Default: true},
		"other": {ID: "00000000-0000-0000-0000-000000000002"},
	}}
	if err := def.RequireChatID(); err == nil || !strings.Contains(err.Error(), `chat alias "ops" has no id`) {
		t.Fatalf("default-chat error = %v, want no-id error", err)
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

func TestLoad_ServerTLSConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("bots:\n  main:\n    host: h\n    id: b\n    secret: s\nserver:\n  tls:\n    cert_file: /tls/tls.crt\n    key_file: /tls/tls.key\n    reload_interval: 45s\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	want := TLSYAMLConfig{CertFile: "/tls/tls.crt", KeyFile: "/tls/tls.key", ReloadInterval: "45s"}
	if cfg.Server.TLS == nil || *cfg.Server.TLS != want {
		t.Fatalf("TLS = %#v, want %#v", cfg.Server.TLS, want)
	}
}

func TestValidate_ServerTLSKeysAreKnown(t *testing.T) {
	raw := []byte("server:\n  tls:\n    cert_file: /tls/tls.crt\n    key_file: /tls/tls.key\n    reload_interval: 60s\n")
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, result := range cfg.Validate(raw) {
		if result.Level == ValidationWarning && strings.Contains(result.Message, "unknown key") {
			t.Fatalf("TLS key reported unknown: %+v", result)
		}
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

// --- ValidateRoutingMode ---

func TestValidateRoutingMode(t *testing.T) {
	tests := []struct {
		mode string
		ok   bool
	}{
		{"direct", true},
		{"catalog", true},
		{"mixed", true},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		err := ValidateRoutingMode(tt.mode)
		if tt.ok && err != nil {
			t.Errorf("ValidateRoutingMode(%q) = %v, want nil", tt.mode, err)
		}
		if !tt.ok && err == nil {
			t.Errorf("ValidateRoutingMode(%q) = nil, want error", tt.mode)
		}
	}
}

// --- LoadForEnqueue ---

func TestLoadForEnqueue_MinimalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  driver: rabbitmq
  url: amqp://localhost
  name: express-botx
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := LoadForEnqueue(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForEnqueue() error: %v", err)
	}
	if cfg.Queue.Driver != "rabbitmq" {
		t.Errorf("Queue.Driver = %q, want %q", cfg.Queue.Driver, "rabbitmq")
	}
	if cfg.Producer.RoutingMode != "mixed" {
		t.Errorf("Producer.RoutingMode = %q, want %q (default)", cfg.Producer.RoutingMode, "mixed")
	}
}

func TestLoadForEnqueue_NoDriver_Error(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  url: amqp://localhost
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	_, err := LoadForEnqueue(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for missing queue driver")
	}
	if !strings.Contains(err.Error(), "queue driver is required") {
		t.Errorf("error = %q, should mention queue driver", err.Error())
	}
}

func TestLoadForEnqueue_InvalidRoutingMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  driver: kafka
  url: broker:9092
producer:
  routing_mode: invalid
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	_, err := LoadForEnqueue(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for invalid routing mode")
	}
	if !strings.Contains(err.Error(), "invalid routing mode") {
		t.Errorf("error = %q, should mention invalid routing mode", err.Error())
	}
}

func TestLoadForEnqueue_DirectMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  driver: kafka
  url: broker:9092
  name: express-botx
producer:
  routing_mode: direct
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := LoadForEnqueue(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForEnqueue() error: %v", err)
	}
	if cfg.Producer.RoutingMode != "direct" {
		t.Errorf("Producer.RoutingMode = %q, want %q", cfg.Producer.RoutingMode, "direct")
	}
}

func TestLoadForEnqueue_NoSecretRequired(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  driver: rabbitmq
  url: amqp://localhost
  name: express-botx
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	// Should succeed without any bot credentials
	_, err := LoadForEnqueue(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForEnqueue() should not require credentials: %v", err)
	}
}

// --- LoadForWorker ---

func TestLoadForWorker_MinimalConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  driver: kafka
  url: broker:9092
  name: express-botx
bots:
  alerts:
    host: express.company.ru
    id: bot-uuid
    secret: my-secret
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := LoadForWorker(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForWorker() error: %v", err)
	}
	if cfg.Queue.Driver != "kafka" {
		t.Errorf("Queue.Driver = %q, want %q", cfg.Queue.Driver, "kafka")
	}
	if len(cfg.Bots) != 1 {
		t.Errorf("expected 1 bot, got %d", len(cfg.Bots))
	}
}

func TestLoadForWorker_NoDriver_Error(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  alerts:
    host: h
    id: b
    secret: s
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	_, err := LoadForWorker(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for missing queue driver")
	}
	if !strings.Contains(err.Error(), "queue driver is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestLoadForWorker_NoBots_Error(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  driver: rabbitmq
  url: amqp://localhost
  name: express-botx
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	_, err := LoadForWorker(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for no bots")
	}
	if !strings.Contains(err.Error(), "at least one bot is required") {
		t.Errorf("error = %q", err.Error())
	}
}

// --- ValidateBotIDs ---

func TestValidateBotIDs_UniqueIDs(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"a": {Host: "h1", ID: "id-1", Secret: "s1"},
			"b": {Host: "h2", ID: "id-2", Secret: "s2"},
		},
	}
	if err := cfg.ValidateBotIDs(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateBotIDs_SameID_SameConfig_OK(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"alerts":   {Host: "express.com", ID: "shared-id", Secret: "shared-secret"},
			"warnings": {Host: "express.com", ID: "shared-id", Secret: "shared-secret"},
		},
	}
	if err := cfg.ValidateBotIDs(); err != nil {
		t.Fatalf("expected success for same bot_id with identical config, got: %v", err)
	}
}

func TestValidateBotIDs_SameID_DifferentSecret_Error(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"a": {Host: "h", ID: "shared-id", Secret: "secret-1"},
			"b": {Host: "h", ID: "shared-id", Secret: "secret-2"},
		},
	}
	err := cfg.ValidateBotIDs()
	if err == nil {
		t.Fatal("expected error for same bot_id with different secret")
	}
	if !strings.Contains(err.Error(), "different secret") {
		t.Errorf("error = %q, should mention different secret", err.Error())
	}
}

func TestValidateBotIDs_SameID_DifferentHost_Error(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"a": {Host: "host1.com", ID: "shared-id", Secret: "s"},
			"b": {Host: "host2.com", ID: "shared-id", Secret: "s"},
		},
	}
	err := cfg.ValidateBotIDs()
	if err == nil {
		t.Fatal("expected error for same bot_id with different host")
	}
	if !strings.Contains(err.Error(), "different host") {
		t.Errorf("error = %q, should mention different host", err.Error())
	}
}

// --- BotByID ---

func TestBotByID_Found(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"alerts": {Host: "h", ID: "bot-uuid", Secret: "s"},
			"other":  {Host: "h2", ID: "other-uuid", Secret: "s2"},
		},
	}
	name, bot, err := cfg.BotByID("bot-uuid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "alerts" {
		t.Errorf("name = %q, want %q", name, "alerts")
	}
	if bot.Host != "h" {
		t.Errorf("bot.Host = %q, want %q", bot.Host, "h")
	}
}

func TestBotByID_NotFound(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"alerts": {Host: "h", ID: "bot-uuid", Secret: "s"},
		},
	}
	_, _, err := cfg.BotByID("nonexistent-uuid")
	if err == nil {
		t.Fatal("expected error for unknown bot_id")
	}
	if !strings.Contains(err.Error(), "unknown bot_id") {
		t.Errorf("error = %q, should mention unknown bot_id", err.Error())
	}
}

func TestBotByID_DuplicateAliases_ReturnsOne(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"alias1": {Host: "h", ID: "shared-id", Secret: "s"},
			"alias2": {Host: "h", ID: "shared-id", Secret: "s"},
		},
	}
	name, _, err := cfg.BotByID("shared-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return one of the aliases
	if name != "alias1" && name != "alias2" {
		t.Errorf("name = %q, expected alias1 or alias2", name)
	}
}

// --- Queue/Worker/Catalog config parsing ---

func TestLoad_QueueConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  main:
    host: h
    id: b
    secret: s
queue:
  driver: kafka
  url: broker:9092
  name: express-botx
  reply_queue: express-botx-replies
  group: my-group
  max_file_size: 2MB
worker:
  retry_count: 3
  retry_backoff: 1s
  shutdown_timeout: 30s
  health_listen: ":8081"
catalog:
  queue_name: express-botx-catalog
  cache_file: /var/lib/catalog.json
  max_age: 10m
  publish_interval: 30s
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Queue.Driver != "kafka" {
		t.Errorf("Queue.Driver = %q, want %q", cfg.Queue.Driver, "kafka")
	}
	if cfg.Queue.URL != "broker:9092" {
		t.Errorf("Queue.URL = %q, want %q", cfg.Queue.URL, "broker:9092")
	}
	if cfg.Queue.Name != "express-botx" {
		t.Errorf("Queue.Name = %q, want %q", cfg.Queue.Name, "express-botx")
	}
	if cfg.Queue.ReplyQueue != "express-botx-replies" {
		t.Errorf("Queue.ReplyQueue = %q, want %q", cfg.Queue.ReplyQueue, "express-botx-replies")
	}
	if cfg.Queue.Group != "my-group" {
		t.Errorf("Queue.Group = %q, want %q", cfg.Queue.Group, "my-group")
	}
	if cfg.Queue.MaxFileSize != "2MB" {
		t.Errorf("Queue.MaxFileSize = %q, want %q", cfg.Queue.MaxFileSize, "2MB")
	}
	if cfg.Worker.RetryCount != 3 {
		t.Errorf("Worker.RetryCount = %d, want 3", cfg.Worker.RetryCount)
	}
	if cfg.Worker.RetryBackoff != "1s" {
		t.Errorf("Worker.RetryBackoff = %q, want %q", cfg.Worker.RetryBackoff, "1s")
	}
	if cfg.Worker.ShutdownTimeout != "30s" {
		t.Errorf("Worker.ShutdownTimeout = %q, want %q", cfg.Worker.ShutdownTimeout, "30s")
	}
	if cfg.Worker.HealthListen != ":8081" {
		t.Errorf("Worker.HealthListen = %q, want %q", cfg.Worker.HealthListen, ":8081")
	}
	if cfg.Catalog.QueueName != "express-botx-catalog" {
		t.Errorf("Catalog.QueueName = %q, want %q", cfg.Catalog.QueueName, "express-botx-catalog")
	}
	if cfg.Catalog.CacheFile != "/var/lib/catalog.json" {
		t.Errorf("Catalog.CacheFile = %q, want %q", cfg.Catalog.CacheFile, "/var/lib/catalog.json")
	}
	if cfg.Catalog.MaxAge != "10m" {
		t.Errorf("Catalog.MaxAge = %q, want %q", cfg.Catalog.MaxAge, "10m")
	}
	if cfg.Catalog.PublishInterval != "30s" {
		t.Errorf("Catalog.PublishInterval = %q, want %q", cfg.Catalog.PublishInterval, "30s")
	}
}

// --- Chat bot binding with duplicate bot_id aliases ---

// --- resolveSecrets ---

func TestResolveSecrets_EnvInBotFields(t *testing.T) {
	t.Setenv("TEST_BOT_HOST", "express.example.com")
	t.Setenv("TEST_BOT_ID", "bot-uuid-123")
	t.Setenv("TEST_BOT_TOKEN", "my-token")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  mybot:
    host: "env:TEST_BOT_HOST"
    id: "env:TEST_BOT_ID"
    token: "env:TEST_BOT_TOKEN"
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Host != "express.example.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "express.example.com")
	}
	if cfg.BotID != "bot-uuid-123" {
		t.Errorf("BotID = %q, want %q", cfg.BotID, "bot-uuid-123")
	}
	if cfg.BotToken != "my-token" {
		t.Errorf("BotToken = %q, want %q", cfg.BotToken, "my-token")
	}
}

func TestResolveSecrets_EnvInChatID(t *testing.T) {
	t.Setenv("TEST_CHAT_ID", "chat-uuid-456")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  mybot:
    host: h
    id: b
    secret: s
chats:
  alerts:
    id: "env:TEST_CHAT_ID"
    bot: mybot
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Chats["alerts"].ID != "chat-uuid-456" {
		t.Errorf("chat.ID = %q, want %q", cfg.Chats["alerts"].ID, "chat-uuid-456")
	}
}

func TestResolveSecrets_EnvMissing_Error(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  mybot:
    host: "env:NONEXISTENT_VAR_12345"
    id: b
    secret: s
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	_, err := Load(Flags{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
	if !strings.Contains(err.Error(), "NONEXISTENT_VAR_12345") {
		t.Errorf("error = %q, should mention env var name", err.Error())
	}
}

func TestResolveSecrets_LiteralValues_Unchanged(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
bots:
  mybot:
    host: literal-host.com
    id: literal-id
    secret: literal-secret
chats:
  deploy: literal-chat-id
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := Load(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Host != "literal-host.com" {
		t.Errorf("Host = %q, want %q", cfg.Host, "literal-host.com")
	}
	if cfg.BotID != "literal-id" {
		t.Errorf("BotID = %q, want %q", cfg.BotID, "literal-id")
	}
	if cfg.Chats["deploy"].ID != "literal-chat-id" {
		t.Errorf("chat.ID = %q, want %q", cfg.Chats["deploy"].ID, "literal-chat-id")
	}
}

func TestResolveSecrets_LoadForWorker_EnvFields(t *testing.T) {
	t.Setenv("TEST_W_HOST", "worker-host.com")
	t.Setenv("TEST_W_ID", "worker-bot-id")
	t.Setenv("TEST_W_TOKEN", "worker-token")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
queue:
  driver: rabbitmq
  url: amqp://localhost
  name: work
bots:
  mybot:
    host: "env:TEST_W_HOST"
    id: "env:TEST_W_ID"
    token: "env:TEST_W_TOKEN"
`
	os.WriteFile(cfgPath, []byte(content), 0644)

	cfg, err := LoadForWorker(Flags{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("LoadForWorker() error: %v", err)
	}
	bot := cfg.Bots["mybot"]
	if bot.Host != "worker-host.com" {
		t.Errorf("bot.Host = %q, want %q", bot.Host, "worker-host.com")
	}
	if bot.ID != "worker-bot-id" {
		t.Errorf("bot.ID = %q, want %q", bot.ID, "worker-bot-id")
	}
	if bot.Token != "worker-token" {
		t.Errorf("bot.Token = %q, want %q", bot.Token, "worker-token")
	}
}

func TestValidateConfig_Valid(t *testing.T) {
	data := []byte(`
bots:
  main:
    host: express.example.com
    id: bot-123
    secret: my-secret
chats:
  deploy:
    id: chat-456
    bot: main
`)
	if err := ValidateConfig(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateConfig_InvalidYAML(t *testing.T) {
	data := []byte(`bots: [invalid yaml`)
	err := ValidateConfig(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "invalid YAML") {
		t.Errorf("error = %q, want it to contain 'invalid YAML'", err)
	}
}

func TestValidateConfig_BotWithSecretAndToken(t *testing.T) {
	data := []byte(`
bots:
  main:
    host: express.example.com
    id: bot-123
    secret: my-secret
    token: my-token
`)
	err := ValidateConfig(data)
	if err == nil {
		t.Fatal("expected error for bot with both secret and token")
	}
	if !strings.Contains(err.Error(), "both secret and token") {
		t.Errorf("error = %q, want it to contain 'both secret and token'", err)
	}
}

func TestValidateConfig_DuplicateDefaultChats(t *testing.T) {
	data := []byte(`
chats:
  deploy:
    id: chat-1
    default: true
  alerts:
    id: chat-2
    default: true
`)
	err := ValidateConfig(data)
	if err == nil {
		t.Fatal("expected error for multiple default chats")
	}
	if !strings.Contains(err.Error(), "multiple chats marked as default") {
		t.Errorf("error = %q, want it to contain 'multiple chats marked as default'", err)
	}
}

func TestValidateConfig_InvalidCallbackRules(t *testing.T) {
	data := []byte(`
server:
  callbacks:
    rules:
      - events: []
        handler:
          type: exec
          command: /bin/true
`)
	err := ValidateConfig(data)
	if err == nil {
		t.Fatal("expected error for empty events in callback rule")
	}
	if !strings.Contains(err.Error(), "events must not be empty") {
		t.Errorf("error = %q, want it to contain 'events must not be empty'", err)
	}
}

func TestValidateConfig_EmptyData(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", []byte("")},
		{"whitespace", []byte("  \n\t\n  ")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfig(tc.data)
			if err == nil {
				t.Fatal("expected error for empty config")
			}
			if !strings.Contains(err.Error(), "empty") {
				t.Errorf("error = %q, want it to contain 'empty'", err)
			}
		})
	}
}

func TestValidateChatBots_WithDuplicateBotIDAlias(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"alerts":   {Host: "h", ID: "shared", Secret: "s"},
			"warnings": {Host: "h", ID: "shared", Secret: "s"},
		},
		Chats: map[string]ChatConfig{
			"deploy": {ID: "uuid", Bot: "warnings"},
		},
	}
	// chats.*.bot can reference any alias, even when aliases share bot_id
	if err := cfg.ValidateChatBots(true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigForBot(t *testing.T) {
	cfg := &Config{
		Bots: map[string]BotConfig{
			"alpha": {Host: "alpha.example.com", ID: "a-id", Secret: "a-secret", Timeout: 30},
			"beta":  {Host: "beta.example.com", ID: "b-id", Token: "b-token"},
		},
		Chats: map[string]ChatConfig{
			"deploy": {ID: "chat-uuid"},
		},
		Cache: CacheConfig{Type: "file", FilePath: "/tmp/cache", TTL: 3600},
	}

	t.Run("resolves known bot", func(t *testing.T) {
		got, err := cfg.ConfigForBot("alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Host != "alpha.example.com" {
			t.Errorf("Host = %q, want %q", got.Host, "alpha.example.com")
		}
		if got.BotID != "a-id" {
			t.Errorf("BotID = %q, want %q", got.BotID, "a-id")
		}
		if got.BotSecret != "a-secret" {
			t.Errorf("BotSecret = %q, want %q", got.BotSecret, "a-secret")
		}
		if got.BotName != "alpha" {
			t.Errorf("BotName = %q, want %q", got.BotName, "alpha")
		}
		if got.BotTimeout != 30 {
			t.Errorf("BotTimeout = %d, want %d", got.BotTimeout, 30)
		}
	})

	t.Run("resolves bot with token", func(t *testing.T) {
		got, err := cfg.ConfigForBot("beta")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.BotToken != "b-token" {
			t.Errorf("BotToken = %q, want %q", got.BotToken, "b-token")
		}
		if got.BotSecret != "" {
			t.Errorf("BotSecret = %q, want empty", got.BotSecret)
		}
	})

	t.Run("inherits cache", func(t *testing.T) {
		got, err := cfg.ConfigForBot("alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Cache.Type != "file" {
			t.Errorf("Cache.Type = %q, want %q", got.Cache.Type, "file")
		}
		if got.Cache.TTL != 3600 {
			t.Errorf("Cache.TTL = %d, want %d", got.Cache.TTL, 3600)
		}
	})

	t.Run("inherits chats", func(t *testing.T) {
		got, err := cfg.ConfigForBot("alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := got.Chats["deploy"]; !ok {
			t.Error("expected Chats to include 'deploy'")
		}
	})

	t.Run("unknown bot returns error", func(t *testing.T) {
		_, err := cfg.ConfigForBot("unknown")
		if err == nil {
			t.Fatal("expected error for unknown bot")
		}
		if !strings.Contains(err.Error(), "unknown bot") {
			t.Errorf("error = %q, want it to contain 'unknown bot'", err.Error())
		}
	})

	t.Run("preserves no-cache flag override despite cache env", func(t *testing.T) {
		noCacheCfg := &Config{
			Bots: map[string]BotConfig{
				"alpha": {Host: "alpha.example.com", ID: "a-id", Secret: "a-secret"},
			},
			Cache:   CacheConfig{Type: "none"},
			noCache: true, // set by LoadMinimal/applyFlags when --no-cache is passed
		}
		t.Setenv("EXPRESS_BOTX_CACHE_TYPE", "file")
		got, err := noCacheCfg.ConfigForBot("alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Cache.Type != "none" {
			t.Errorf("Cache.Type = %q, want %q (--no-cache flag must win over env)", got.Cache.Type, "none")
		}
	})

	t.Run("yaml cache none allows env override", func(t *testing.T) {
		// Config file has cache.type: none, but noCache flag is NOT set.
		// Env var EXPRESS_BOTX_CACHE_TYPE=file should win (env overrides YAML).
		yamlNoneCfg := &Config{
			Bots: map[string]BotConfig{
				"alpha": {Host: "alpha.example.com", ID: "a-id", Secret: "a-secret"},
			},
			Cache: CacheConfig{Type: "none"}, // from YAML, not from --no-cache flag
			// noCache is false — no flag was passed
		}
		t.Setenv("EXPRESS_BOTX_CACHE_TYPE", "file")
		got, err := yamlNoneCfg.ConfigForBot("alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Cache.Type != "file" {
			t.Errorf("Cache.Type = %q, want %q (env should override YAML cache.type: none)", got.Cache.Type, "file")
		}
	})
}

func TestValidate_ValidConfig(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: express.example.com
    id: 00000000-0000-0000-0000-000000000001
    secret: my-secret
chats:
  deploy:
    id: 00000000-0000-0000-0000-000000000002
    bot: main
cache:
  type: file
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)
	if len(results) != 0 {
		t.Errorf("expected no issues, got %d: %v", len(results), results)
	}
}

func TestValidate_UnknownKeys(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: express.example.com
    id: 00000000-0000-0000-0000-000000000001
    secret: my-secret
    unknown_field: value
unknown_top: true
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	warnings := 0
	unknownPaths := map[string]bool{}
	for _, r := range results {
		if r.Level == ValidationWarning && strings.Contains(r.Message, "unknown key") {
			warnings++
			unknownPaths[r.Path] = true
		}
	}
	if warnings < 2 {
		t.Errorf("expected at least 2 unknown key warnings, got %d", warnings)
	}
	if !unknownPaths["unknown_top"] {
		t.Error("expected warning for unknown_top")
	}
	if !unknownPaths["bots.main.unknown_field"] {
		t.Error("expected warning for bots.main.unknown_field")
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    timeout: 10
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	errorPaths := map[string]bool{}
	for _, r := range results {
		if r.Level == ValidationError {
			errorPaths[r.Path] = true
		}
	}
	if !errorPaths["bots.main.host"] {
		t.Error("expected error for missing bots.main.host")
	}
	if !errorPaths["bots.main.id"] {
		t.Error("expected error for missing bots.main.id")
	}
	if !errorPaths["bots.main"] {
		t.Error("expected error for missing secret/token in bots.main")
	}
}

func TestValidate_BotSecretAndToken(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: express.example.com
    id: 00000000-0000-0000-0000-000000000001
    secret: s
    token: t
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	found := false
	for _, r := range results {
		if r.Level == ValidationError && strings.Contains(r.Message, "both secret and token") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for bot with both secret and token")
	}
}

func TestValidate_InvalidFormats(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: express.example.com
    id: not-a-uuid
    secret: s
chats:
  deploy:
    id: also-not-uuid
producer:
  routing_mode: invalid
worker:
  retry_backoff: not-a-duration
cache:
  type: redis
queue:
  driver: nats
  max_file_size: invalid-size
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	errorPaths := map[string]bool{}
	for _, r := range results {
		if r.Level == ValidationError {
			errorPaths[r.Path] = true
		}
	}

	expected := []string{
		"bots.main.id",
		"chats.deploy.id",
		"producer.routing_mode",
		"worker.retry_backoff",
		"cache.type",
		"queue.driver",
		"queue.max_file_size",
	}
	for _, p := range expected {
		if !errorPaths[p] {
			t.Errorf("expected error for %s", p)
		}
	}
}

func TestValidate_CrossReferences(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: express.example.com
    id: 00000000-0000-0000-0000-000000000001
    secret: s
chats:
  deploy:
    id: 00000000-0000-0000-0000-000000000002
    bot: nonexistent
  alerts:
    id: 00000000-0000-0000-0000-000000000003
    default: true
  staging:
    id: 00000000-0000-0000-0000-000000000004
    default: true
server:
  alertmanager:
    default_chat_id: missing_chat
  grafana:
    default_chat_id: also_missing
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	errorPaths := map[string]bool{}
	for _, r := range results {
		if r.Level == ValidationError {
			errorPaths[r.Path] = true
		}
	}

	if !errorPaths["chats.deploy.bot"] {
		t.Error("expected error for chats.deploy.bot referencing nonexistent bot")
	}
	if !errorPaths["chats"] {
		t.Error("expected error for multiple default chats")
	}
	if !errorPaths["server.alertmanager.default_chat_id"] {
		t.Error("expected error for alertmanager.default_chat_id referencing unknown chat")
	}
	if !errorPaths["server.grafana.default_chat_id"] {
		t.Error("expected error for grafana.default_chat_id referencing unknown chat")
	}
}

func TestValidate_MixedErrorsAndWarnings(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: express.example.com
    id: 00000000-0000-0000-0000-000000000001
    secret: s
    extra_field: x
unknown_section: true
worker:
  retry_backoff: bad
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	var errors, warnings int
	for _, r := range results {
		switch r.Level {
		case ValidationError:
			errors++
		case ValidationWarning:
			warnings++
		}
	}
	if errors == 0 {
		t.Error("expected at least one error")
	}
	if warnings == 0 {
		t.Error("expected at least one warning")
	}
}

func TestValidate_SecretRefSkipsUUIDCheck(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: express.example.com
    id: "env:BOT_ID"
    secret: "vault:path#key"
chats:
  deploy:
    id: "env:CHAT_ID"
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	for _, r := range results {
		if r.Level == ValidationError && strings.Contains(r.Message, "UUID") {
			t.Errorf("unexpected UUID error for secret ref: %s: %s", r.Path, r.Message)
		}
	}
}

func TestValidate_DurationFields(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: h
    id: 00000000-0000-0000-0000-000000000001
    secret: s
worker:
  retry_backoff: 5s
  shutdown_timeout: 30s
catalog:
  max_age: 1h
  publish_interval: 10m
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	for _, r := range results {
		if r.Level == ValidationError {
			t.Errorf("unexpected error: %s: %s", r.Path, r.Message)
		}
	}
}

func TestValidate_UnknownCallbackEvent(t *testing.T) {
	rawYAML := []byte(`
bots:
  main:
    host: h
    id: 00000000-0000-0000-0000-000000000001
    secret: s
server:
  callbacks:
    rules:
      - events: ["messege"]
        handler:
          type: exec
          command: /bin/true
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	found := false
	for _, r := range results {
		if r.Level == ValidationWarning && strings.Contains(r.Message, "unknown event") && strings.Contains(r.Message, "messege") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning for unknown event 'messege', got results: %v", results)
	}
}

func TestValidate_AlertmanagerGrafanaWithUUID(t *testing.T) {
	// default_chat_id as UUID should not trigger cross-ref error
	rawYAML := []byte(`
bots:
  main:
    host: h
    id: 00000000-0000-0000-0000-000000000001
    secret: s
server:
  alertmanager:
    default_chat_id: 00000000-0000-0000-0000-000000000099
  grafana:
    default_chat_id: 00000000-0000-0000-0000-000000000098
`)
	var cfg Config
	if err := yaml.Unmarshal(rawYAML, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results := cfg.Validate(rawYAML)

	for _, r := range results {
		if strings.Contains(r.Path, "default_chat_id") && r.Level == ValidationError {
			t.Errorf("unexpected error for UUID default_chat_id: %s: %s", r.Path, r.Message)
		}
	}
}

func findValidationResult(t *testing.T, raw []byte, path string) ValidationResult {
	t.Helper()
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, result := range cfg.Validate(raw) {
		if result.Path == path {
			return result
		}
	}
	t.Fatalf("validation result %q not found", path)
	return ValidationResult{}
}

func TestValidate_GitlabSendersOnlySchema(t *testing.T) {
	raw := []byte(`
server:
  gitlab:
    senders:
      - name: dev
        secret: token
        chats: [alerts]
        events: {only: [push]}
        routes:
          - match: {event: [push]}
            chats: [alerts]
chats:
  alerts: 00000000-0000-0000-0000-000000000001
`)
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, result := range cfg.Validate(raw) {
		if result.Level == ValidationError || strings.Contains(result.Path, "server.gitlab.senders") {
			t.Fatalf("unexpected validation result: %+v", result)
		}
	}
}

func TestValidate_GitlabLegacyTopLevelMessages(t *testing.T) {
	for field, want := range map[string]string{
		"secret":          "moved inside senders[]",
		"events":          "moved inside senders[]",
		"templates":       "moved inside senders[]",
		"template_files":  "moved inside senders[]",
		"error_events":    "moved inside senders[]",
		"routes":          "moved inside senders[]",
		"default_chat_id": "removed",
	} {
		raw := []byte("server:\n  gitlab:\n    " + field + ": value\n")
		result := findValidationResult(t, raw, "server.gitlab."+field)
		if !strings.Contains(result.Message, want) {
			t.Fatalf("%s message = %q, want %q", field, result.Message, want)
		}
	}
}

func TestValidate_GitlabSecretTokenIsUnknown(t *testing.T) {
	raw := []byte("server:\n  gitlab:\n    senders:\n      - secret_token: old\n        chats: [alerts]\n")
	result := findValidationResult(t, raw, "server.gitlab.senders[0].secret_token")
	if result.Message != `unknown key "secret_token"` {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestValidate_GitlabPerSenderPaths(t *testing.T) {
	raw := []byte(`
server:
  gitlab:
    senders:
      - {name: ok, secret: one, chats: [alerts]}
      - name: broken
        secret: two
        chats: [alerts]
        events: {only: ["bad.key.extra"]}
chats:
  alerts: 00000000-0000-0000-0000-000000000001
`)
	result := findValidationResult(t, raw, "server.gitlab.senders[1].events.only")
	if !strings.Contains(result.Message, `sender "broken"`) {
		t.Fatalf("message lacks sender name: %q", result.Message)
	}
}

// A chat alias with no id is rejected once by the general required-fields rule,
// protecting every consumer (here: an alertmanager default_chat_id that only
// cross-references alias existence).
func TestValidate_ChatIDRequired(t *testing.T) {
	raw := []byte(`
server:
  alertmanager:
    default_chat_id: ops
chats:
  ops: {bot: main}
bots:
  main: {id: 00000000-0000-0000-0000-000000000001, host: h, secret: s}
`)
	result := findValidationResult(t, raw, "chats.ops.id")
	if result.Level != ValidationError || result.Message != "id is required" {
		t.Fatalf("result = %+v, want id-required error", result)
	}
}

// Byte-identical sender secrets are guaranteed duplicates and must be caught
// offline, before serve startup fails on the resolved values.
func TestValidate_GitlabDuplicateSecrets(t *testing.T) {
	raw := []byte(`
server:
  gitlab:
    senders:
      - {name: dev, secret: same, chats: [alerts]}
      - {name: ops, secret: same, chats: [alerts]}
chats:
  alerts: 00000000-0000-0000-0000-000000000001
`)
	result := findValidationResult(t, raw, "server.gitlab.senders[1].secret")
	if !strings.Contains(result.Message, "secret duplicates server.gitlab.senders[0]") {
		t.Fatalf("message = %q, want duplicate-secret error", result.Message)
	}
}

// Offline validation must mirror the serve-startup rule that route chats
// resolve into the sender's explicit chats scope, for the offline-decidable
// alias/UUID cases.
func TestValidate_GitlabRouteOutsideScope(t *testing.T) {
	const alertsID = "00000000-0000-0000-0000-000000000001"
	raw := []byte(`
server:
  gitlab:
    senders:
      - name: dev
        secret: token
        chats: [alerts]
        routes:
          - match: {event: [push]}
            chats: [ops]
chats:
  alerts: ` + alertsID + `
  ops: 00000000-0000-0000-0000-000000000002
`)
	result := findValidationResult(t, raw, "server.gitlab.senders[0].routes[0].chats")
	if !strings.Contains(result.Message, `delivery target "ops" is outside sender scope`) {
		t.Fatalf("message = %q, want out-of-scope error", result.Message)
	}

	// A route referencing the scope chat by raw UUID stays in scope.
	inScope := []byte(`
server:
  gitlab:
    senders:
      - name: dev
        secret: token
        chats: [alerts]
        routes:
          - match: {event: [push]}
            chats: [` + alertsID + `]
chats:
  alerts: ` + alertsID + `
`)
	var cfg Config
	if err := yaml.Unmarshal(inScope, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, r := range cfg.Validate(inScope) {
		if r.Level == ValidationError {
			t.Fatalf("unexpected validation error: %+v", r)
		}
	}
}

// Offline validation mirrors the serve-startup delivery-target rules: empty-id
// aliases, duplicate canonical UUIDs (both scope shapes), and route/target
// bot-binding conflicts are all caught by `config validate`.
func TestValidate_GitlabOfflineMirrorsServeRules(t *testing.T) {
	const (
		id1 = "00000000-0000-0000-0000-000000000001"
		id2 = "00000000-0000-0000-0000-000000000002"
	)
	tests := []struct {
		name     string
		raw      string
		wantPath string
		wantMsg  string
	}{
		{
			// An empty-id alias is flagged once by the general chats.<name>.id
			// required rule (validateRequiredFields), not per gitlab reference.
			name: "empty-id alias",
			raw: `
server:
  gitlab:
    senders:
      - {name: dev, secret: token, chats: [alerts]}
chats:
  alerts: {bot: main}
bots:
  main: {id: ` + id1 + `, host: h, secret: s}
`,
			wantPath: "chats.alerts.id",
			wantMsg:  "id is required",
		},
		{
			name: "alias and raw UUID resolve to one chat",
			raw: `
server:
  gitlab:
    senders:
      - {name: dev, secret: token, chats: [alerts, ` + id1 + `]}
chats:
  alerts: ` + id1 + `
`,
			wantPath: "server.gitlab.senders[0].chats[1]",
			wantMsg:  `delivery targets "alerts" and "` + id1 + `" resolve to the same chat UUID`,
		},
		{
			name: "routes-only duplicate canonical targets",
			raw: `
server:
  gitlab:
    senders:
      - name: dev
        secret: token
        routes:
          - {match: {event: [push]}, chats: [alerts]}
          - {match: {event: [tag_push]}, chats: [` + id1 + `]}
chats:
  alerts: ` + id1 + `
`,
			wantPath: "server.gitlab.senders[0].routes[1].chats",
			wantMsg:  "resolve to the same chat UUID",
		},
		{
			name: "route alias bot conflicts with scope target",
			raw: `
server:
  gitlab:
    senders:
      - name: dev
        secret: token
        chats: [alerts-dev]
        routes:
          - {match: {event: [push]}, chats: [alerts-ops]}
chats:
  alerts-dev: {id: ` + id1 + `, bot: dev}
  alerts-ops: {id: ` + id1 + `, bot: ops}
bots:
  dev: {id: ` + id2 + `, host: h, secret: s}
  ops: {id: 00000000-0000-0000-0000-000000000003, host: h, secret: s}
`,
			wantPath: "server.gitlab.senders[0].routes[0].chats",
			wantMsg:  `"alerts-ops" is bound to bot "ops" but scope target "alerts-dev" is bound to bot "dev"`,
		},
		{
			// The bot-conflict is deterministic against the decidable scope
			// entry (alerts-dev) even though a sibling scope target (ops) has an
			// undecidable secret-ref id, so validate must still catch it.
			name: "route bot conflict with undecidable sibling scope target",
			raw: `
server:
  gitlab:
    senders:
      - name: dev
        secret: token
        chats: [ops, alerts-dev]
        routes:
          - {match: {event: [push]}, chats: [alerts-ops]}
chats:
  ops:        {id: env:OPS_CHAT, bot: dev}
  alerts-dev: {id: ` + id1 + `, bot: dev}
  alerts-ops: {id: ` + id1 + `, bot: ops}
bots:
  dev: {id: ` + id2 + `, host: h, secret: s}
  ops: {id: 00000000-0000-0000-0000-000000000003, host: h, secret: s}
`,
			wantPath: "server.gitlab.senders[0].routes[0].chats",
			wantMsg:  `"alerts-ops" is bound to bot "ops" but scope target "alerts-dev" is bound to bot "dev"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findValidationResult(t, []byte(tt.raw), tt.wantPath)
			if !strings.Contains(result.Message, tt.wantMsg) {
				t.Fatalf("message = %q, want containing %q", result.Message, tt.wantMsg)
			}
		})
	}
}

// Multi-bot delivery rules cannot be offline errors (whether serve runs
// multi-bot depends on --bot and runtime credentials), so validation warns.
func TestValidate_GitlabMultiBotWarnings(t *testing.T) {
	const (
		id1 = "00000000-0000-0000-0000-000000000001"
		id2 = "00000000-0000-0000-0000-000000000002"
	)
	raw := []byte(`
server:
  gitlab:
    senders:
      - {name: dev, secret: token, chats: [unbound, ` + id1 + `]}
chats:
  unbound: ` + id2 + `
bots:
  a: {id: 00000000-0000-0000-0000-000000000003, host: h, secret: s}
  b: {id: 00000000-0000-0000-0000-000000000004, host: h, secret: s}
`)
	aliasWarn := findValidationResult(t, raw, "server.gitlab.senders[0].chats[0]")
	if aliasWarn.Level != ValidationWarning || !strings.Contains(aliasWarn.Message, "no bot binding") {
		t.Fatalf("alias warning = %+v", aliasWarn)
	}
	uuidWarn := findValidationResult(t, raw, "server.gitlab.senders[0].chats[1]")
	if uuidWarn.Level != ValidationWarning || !strings.Contains(uuidWarn.Message, "raw chat UUID") {
		t.Fatalf("uuid warning = %+v", uuidWarn)
	}
}

// The multi-bot binding warning is decidable from the alias bot binding alone,
// so it must fire even when the chat id is a secret reference (undecidable
// offline) — and must not duplicate when the same chat is listed twice.
func TestValidate_GitlabMultiBotWarningSecretRefAndDedup(t *testing.T) {
	countWarnings := func(raw []byte, path string) int {
		var cfg Config
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, r := range cfg.Validate(raw) {
			if r.Path == path && r.Level == ValidationWarning && strings.Contains(r.Message, "no bot binding") {
				n++
			}
		}
		return n
	}

	// Secret-ref id, no bot binding: warns despite the undecidable id.
	secretRef := []byte(`
server:
  gitlab:
    senders:
      - {name: dev, secret: token, chats: [team]}
chats:
  team: {id: env:TEAM_CHAT}
bots:
  a: {id: 00000000-0000-0000-0000-000000000003, host: h, secret: s}
  b: {id: 00000000-0000-0000-0000-000000000004, host: h, secret: s}
`)
	if n := countWarnings(secretRef, "server.gitlab.senders[0].chats[0]"); n != 1 {
		t.Fatalf("secret-ref warnings = %d, want 1", n)
	}

	// The same chat listed twice warns once, not per occurrence.
	dup := []byte(`
server:
  gitlab:
    senders:
      - name: dev
        secret: token
        routes:
          - {match: {event: [push]}, chats: [team, team]}
chats:
  team: 00000000-0000-0000-0000-000000000001
bots:
  a: {id: 00000000-0000-0000-0000-000000000003, host: h, secret: s}
  b: {id: 00000000-0000-0000-0000-000000000004, host: h, secret: s}
`)
	if n := countWarnings(dup, "server.gitlab.senders[0].routes[0].chats"); n != 1 {
		t.Fatalf("duplicate-reference warnings = %d, want 1", n)
	}
}

func TestValidate_GitlabPerSenderValidation(t *testing.T) {
	tests := []struct {
		name     string
		gitlab   string
		wantPath string
		wantMsg  string
	}{
		{
			name:     "empty senders",
			gitlab:   "    senders: []",
			wantPath: "server.gitlab.senders",
			wantMsg:  "senders must not be empty",
		},
		{
			name: "missing secret",
			gitlab: `    senders:
      - name: broken
        chats: [alerts]`,
			wantPath: "server.gitlab.senders[0].secret",
			wantMsg:  `sender "broken": secret is required`,
		},
		{
			name: "empty chats and routes",
			gitlab: `    senders:
      - name: broken
        secret: token
        chats: []
        routes: []`,
			wantPath: "server.gitlab.senders[0].chats",
			wantMsg:  "chats or routes must not be empty",
		},
		{
			name: "invalid second sender",
			gitlab: `    senders:
      - {name: ok, secret: one, chats: [alerts]}
      - name: broken
        secret: two
        chats: [alerts]
        events: {only: ["bad.key.extra"]}`,
			wantPath: "server.gitlab.senders[1].events.only",
			wantMsg:  `sender "broken"`,
		},
		{
			name: "duplicate names",
			gitlab: `    senders:
      - {name: same, secret: one, chats: [alerts]}
      - {name: same, secret: two, chats: [alerts]}`,
			wantPath: "server.gitlab.senders[1].name",
			wantMsg:  "duplicates server.gitlab.senders[0]",
		},
		{
			name: "template overlap",
			gitlab: `    senders:
      - name: broken
        secret: token
        chats: [alerts]
        templates: {push: inline}
        template_files: {push: ./push.tmpl}`,
			wantPath: "server.gitlab.senders[0].template_files.push",
			wantMsg:  "defined in both templates and template_files",
		},
		{
			name: "bad event key",
			gitlab: `    senders:
      - name: broken
        secret: token
        chats: [alerts]
        error_events: ["bad.key.extra"]`,
			wantPath: "server.gitlab.senders[0].error_events",
			wantMsg:  "invalid event key",
		},
		{
			name: "bad excluded event key",
			gitlab: `    senders:
      - secret: token
        chats: [alerts]
        events: {exclude: ["bad.key.extra"]}`,
			wantPath: "server.gitlab.senders[0].events.exclude",
			wantMsg:  "invalid event key",
		},
		{
			name: "bad template key",
			gitlab: `    senders:
      - secret: token
        chats: [alerts]
        templates: {"bad key": inline}`,
			wantPath: "server.gitlab.senders[0].templates.bad key",
			wantMsg:  "invalid event key",
		},
		{
			name: "bad template file key",
			gitlab: `    senders:
      - secret: token
        chats: [alerts]
        template_files: {"bad key": ./bad.tmpl}`,
			wantPath: "server.gitlab.senders[0].template_files.bad key",
			wantMsg:  "invalid event key",
		},
		{
			name: "equivalent template catch alls",
			gitlab: `    senders:
      - secret: token
        chats: [alerts]
        templates: {pipeline: inline}
        template_files: {"pipeline.*": ./pipeline.tmpl}`,
			wantPath: "server.gitlab.senders[0].templates",
			wantMsg:  "equivalent catch-alls",
		},
		{
			name: "empty route chats",
			gitlab: `    senders:
      - name: broken
        secret: token
        routes:
          - match: {event: [push]}
            chats: []`,
			wantPath: "server.gitlab.senders[0].routes[0].chats",
			wantMsg:  `sender "broken"`,
		},
		{
			name: "bad route regex",
			gitlab: `    senders:
      - name: broken
        secret: token
        routes:
          - match: {branch: ["/release-([/"]}
            chats: [alerts]`,
			wantPath: "server.gitlab.senders[0].routes[0].match.branch",
			wantMsg:  "invalid regex pattern",
		},
		{
			name: "bad route event key",
			gitlab: `    senders:
      - secret: token
        routes:
          - match: {event: ["bad.key.extra"]}
            chats: [alerts]`,
			wantPath: "server.gitlab.senders[0].routes[0].match.event",
			wantMsg:  "invalid event key",
		},
		{
			name: "unknown sender chat alias",
			gitlab: `    senders:
      - {secret: token, chats: [missing]}`,
			wantPath: "server.gitlab.senders[0].chats[0]",
			wantMsg:  "unknown chat alias",
		},
		{
			name: "unknown route chat alias",
			gitlab: `    senders:
      - secret: token
        routes:
          - chats: [missing]`,
			wantPath: "server.gitlab.senders[0].routes[0].chats",
			wantMsg:  "unknown chat alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte("chats:\n  alerts: 00000000-0000-0000-0000-000000000001\nserver:\n  gitlab:\n" + tt.gitlab + "\n")
			result := findValidationResult(t, raw, tt.wantPath)
			if result.Level != ValidationError {
				t.Fatalf("level = %q, want error: %+v", result.Level, result)
			}
			if !strings.Contains(result.Message, tt.wantMsg) {
				t.Fatalf("message = %q, want substring %q", result.Message, tt.wantMsg)
			}
		})
	}
}

func TestValidate_GitlabNestedUnknownKeysUseIndexedPaths(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  []byte
		path string
	}{
		{
			name: "events",
			raw:  []byte("server:\n  gitlab:\n    senders:\n      - secret: token\n        chats: [alerts]\n        events: {exclud: [push]}\n"),
			path: "server.gitlab.senders[0].events.exclud",
		},
		{
			name: "routes",
			raw:  []byte("server:\n  gitlab:\n    senders:\n      - secret: token\n        routes:\n          - chats: [alerts]\n            stopp: true\n"),
			path: "server.gitlab.senders[0].routes[0].stopp",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result := findValidationResult(t, tt.raw, tt.path)
			if result.Message == "" || !strings.Contains(result.Message, "unknown key") {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestValidate_GitlabRouteMatchSelectorsAreOpen(t *testing.T) {
	raw := []byte(`
server:
  gitlab:
    senders:
      - secret: token
        routes:
          - match:
              project: [group/backend]
              object_attributes.labels: [urgent]
            chats: [00000000-0000-0000-0000-000000000001]
`)
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, result := range cfg.Validate(raw) {
		if result.Level == ValidationWarning && strings.Contains(result.Path, ".routes[0].match.") {
			t.Fatalf("unexpected selector warning: %+v", result)
		}
	}
}

func TestGitlabYAMLConfig_ValidateForServe(t *testing.T) {
	tests := []struct {
		name    string
		config  GitlabYAMLConfig
		wantErr string
	}{
		{
			name:    "empty senders",
			config:  GitlabYAMLConfig{},
			wantErr: "server.gitlab.senders",
		},
		{
			name: "missing secret",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Name: "broken", Chats: []string{"alerts"}},
			}},
			wantErr: `server.gitlab.senders[0].secret: sender "broken"`,
		},
		{
			name: "empty chats and routes",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Secret: "token"},
			}},
			wantErr: "server.gitlab.senders[0].chats",
		},
		{
			name: "invalid second sender",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Name: "ok", Secret: "one", Chats: []string{"alerts"}},
				{Name: "broken", Secret: "two", Chats: []string{"alerts"}, Events: GitlabEventsConfig{Only: []string{"bad.key.extra"}}},
			}},
			wantErr: `server.gitlab.senders[1].events.only: sender "broken"`,
		},
		{
			name: "template overlap",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{
					Secret:        "token",
					Chats:         []string{"alerts"},
					Templates:     map[string]string{"push": "inline"},
					TemplateFiles: map[string]string{"push": "./push.tmpl"},
				},
			}},
			wantErr: "server.gitlab.senders[0].template_files.push",
		},
		{
			name: "equivalent template catch alls",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{
					Secret:        "token",
					Chats:         []string{"alerts"},
					Templates:     map[string]string{"pipeline": "inline"},
					TemplateFiles: map[string]string{"pipeline.*": "./pipeline.tmpl"},
				},
			}},
			wantErr: "server.gitlab.senders[0].templates",
		},
		{
			name: "empty route chats",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Secret: "token", Routes: []GitlabRouteYAMLConfig{{Match: map[string][]string{"event": {"push"}}}}},
			}},
			wantErr: "server.gitlab.senders[0].routes[0].chats",
		},
		{
			name: "invalid route event",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Secret: "token", Routes: []GitlabRouteYAMLConfig{{Match: map[string][]string{"event": {"bad.key.extra"}}, Chats: []string{"alerts"}}}},
			}},
			wantErr: "server.gitlab.senders[0].routes[0].match.event",
		},
		{
			name: "invalid route regex",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Secret: "token", Routes: []GitlabRouteYAMLConfig{{Match: map[string][]string{"branch": {"/release-([/"}}, Chats: []string{"alerts"}}}},
			}},
			wantErr: "server.gitlab.senders[0].routes[0].match.branch",
		},
		{
			name: "duplicate name",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Name: "same", Secret: "one", Chats: []string{"alerts"}},
				{Name: "same", Secret: "two", Chats: []string{"alerts"}},
			}},
			wantErr: "server.gitlab.senders[1].name",
		},
		{
			name: "valid route only sender",
			config: GitlabYAMLConfig{Senders: []GitlabSenderYAMLConfig{
				{Secret: "one", Routes: []GitlabRouteYAMLConfig{{Chats: []string{"alerts"}}}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.ValidateForServe()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
