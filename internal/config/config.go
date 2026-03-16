package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Bots   map[string]BotConfig `yaml:"bots,omitempty"`
	Chats  map[string]ChatConfig `yaml:"chats,omitempty"`
	Cache  CacheConfig          `yaml:"cache"`
	Server ServerConfig         `yaml:"server,omitempty"`

	// Resolved at runtime (not persisted).
	Host       string `yaml:"-"`
	BotID      string `yaml:"-"`
	BotSecret  string `yaml:"-"`
	BotToken   string `yaml:"-"` // static token (alternative to secret)
	BotName    string `yaml:"-"`
	BotTimeout int    `yaml:"-"` // HTTP timeout in seconds (from bot config)
	ChatID     string `yaml:"-"`
	Format     string `yaml:"-"`
	multiBot   bool   // true when serve starts with multiple bots, no --bot
	configPath string
}

// ServerConfig holds HTTP server settings for the "serve" subcommand.
type ServerConfig struct {
	Listen             string                `yaml:"listen,omitempty"`
	BasePath           string                `yaml:"base_path,omitempty"`
	APIKeys            []APIKeyConfig        `yaml:"api_keys,omitempty"`
	AllowBotSecretAuth bool                  `yaml:"allow_bot_secret_auth,omitempty"`
	Alertmanager       *AlertmanagerYAMLConfig `yaml:"alertmanager,omitempty"`
	Grafana            *GrafanaYAMLConfig      `yaml:"grafana,omitempty"`
	Docs               *bool                   `yaml:"docs,omitempty"`         // enable /docs endpoint (default: true)
	ExternalURL        string                  `yaml:"external_url,omitempty"` // public URL for OpenAPI docs (e.g. http://express-botx.invitro-dev.k8s)
}

// AlertmanagerYAMLConfig holds YAML settings for the alertmanager webhook endpoint.
type AlertmanagerYAMLConfig struct {
	DefaultChatID   string   `yaml:"default_chat_id,omitempty"`
	ErrorSeverities []string `yaml:"error_severities,omitempty"`
	Template        string   `yaml:"template,omitempty"`
	TemplateFile    string   `yaml:"template_file,omitempty"`
}

// GrafanaYAMLConfig holds YAML settings for the Grafana webhook endpoint.
type GrafanaYAMLConfig struct {
	DefaultChatID string   `yaml:"default_chat_id,omitempty"`
	ErrorStates   []string `yaml:"error_states,omitempty"`
	Template      string   `yaml:"template,omitempty"`
	TemplateFile  string   `yaml:"template_file,omitempty"`
}

// APIKeyConfig defines a single API key for server authentication.
type APIKeyConfig struct {
	Name string `yaml:"name" json:"name"`
	Key  string `yaml:"key" json:"key"` // literal, env:VAR, or vault:path#key
}

type BotConfig struct {
	Host    string `yaml:"host"`
	ID      string `yaml:"id"`
	Secret  string `yaml:"secret,omitempty"`
	Token   string `yaml:"token,omitempty"`  // pre-obtained token (alternative to secret)
	Timeout int    `yaml:"timeout,omitempty"` // HTTP timeout in seconds (default: 10)
}

// ChatConfig represents a chat alias with an optional default bot.
// Supports both short form (just UUID string) and long form ({id, bot, default}) in YAML.
type ChatConfig struct {
	ID      string `yaml:"id"`
	Bot     string `yaml:"bot,omitempty"`
	Default bool   `yaml:"default,omitempty"`
}

// UnmarshalYAML supports both string ("UUID") and object ({id: "UUID", bot: "name"}) forms.
func (c *ChatConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		c.ID = value.Value
		return nil
	}
	type plain ChatConfig
	return value.Decode((*plain)(c))
}

// MarshalYAML preserves short form for chats without a bot binding or default flag.
func (c ChatConfig) MarshalYAML() (any, error) {
	if c.Bot == "" && !c.Default {
		return c.ID, nil
	}
	type plain ChatConfig
	return (plain)(c), nil
}

type CacheConfig struct {
	Type      string `yaml:"type"`       // "none"|"file"|"vault"
	FilePath  string `yaml:"file_path"`
	VaultURL  string `yaml:"vault_url"`
	VaultPath string `yaml:"vault_path"`
	TTL       int    `yaml:"ttl"` // seconds, default 31536000 (1 year)
}

// Flags holds CLI flag values for layering on top of file/env config.
type Flags struct {
	ConfigPath string
	Bot        string
	Host       string
	BotID      string
	Secret     string
	Token      string
	ChatID     string
	NoCache    bool
	Format     string
	Verbose    int
}

// Load reads configuration with layering: YAML file → resolve bot → env → CLI flags.
func Load(flags Flags) (*Config, error) {
	cfg := &Config{
		Cache: CacheConfig{
			Type: "file",
			TTL:  31536000,
		},
	}

	// Layer 1: YAML file
	configPath, explicit := resolveConfigPath(flags.ConfigPath)
	cfg.configPath = configPath
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			vlog.V1("config: loaded from %s", configPath)
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parsing config %s: %w", configPath, err)
			}
		} else if explicit {
			return nil, fmt.Errorf("reading config %s: %w", configPath, err)
		} else {
			vlog.V2("config: %s not found, skipping", configPath)
		}
	}

	// Validate: no bot has both secret and token in YAML
	if err := cfg.validateBotConfigs(); err != nil {
		return nil, err
	}

	// Validate: at most one chat marked as default
	if err := cfg.ValidateDefaultChat(); err != nil {
		return nil, err
	}

	// Layer 2: resolve bot from config (defer multi-bot error until after env/flags)
	if flags.Bot != "" || len(cfg.Bots) <= 1 {
		if err := cfg.resolveBot(flags.Bot); err != nil {
			return nil, err
		}
		if cfg.BotName != "" {
			vlog.V1("config: using bot %q (%s)", cfg.BotName, cfg.Host)
		}
	}

	// Layer 3: environment variables (override resolved bot)
	if err := applyEnv(cfg); err != nil {
		return nil, err
	}

	// Layer 4: CLI flags (highest priority)
	applyFlags(cfg, flags)

	// If env/flags replaced credentials, the resolved bot name is stale
	cfg.clearStaleBotName()

	vlog.V2("config: host=%s bot_id=%s cache=%s", cfg.Host, cfg.BotID, cfg.Cache.Type)

	// Multiple bots, no --bot: try chat-bound bot, then env/flags, then error
	if flags.Bot == "" && len(cfg.Bots) > 1 && cfg.BotName == "" {
		if cfg.hasCredentials() {
			vlog.V1("config: using bot from env/flags (%s)", cfg.Host)
		} else if chatBot := cfg.resolveChatBotFromFlags(flags.ChatID); chatBot != "" {
			if err := cfg.ApplyChatBot(chatBot); err != nil {
				return nil, err
			}
			vlog.V1("config: using bot %q from chat binding", chatBot)
		} else {
			return nil, fmt.Errorf("multiple bots configured, specify one with --bot: %s", cfg.botNames())
		}
	}

	// Validate required fields
	if cfg.Host == "" {
		return nil, fmt.Errorf("host is required (--host, EXPRESS_BOTX_HOST, or config file)")
	}
	if cfg.BotID == "" {
		return nil, fmt.Errorf("bot id is required (--bot-id, EXPRESS_BOTX_BOT_ID, or config file)")
	}
	if cfg.BotSecret == "" && cfg.BotToken == "" {
		return nil, fmt.Errorf("bot secret or token is required (--secret, --token, EXPRESS_BOTX_SECRET, EXPRESS_BOTX_TOKEN, or config file)")
	}

	return cfg, nil
}

// LoadForServe reads configuration for the serve command.
// Unlike Load, it does not require a single bot to be resolved when multiple bots are configured.
// In multi-bot mode, the bot is selected per-request.
func LoadForServe(flags Flags) (*Config, error) {
	cfg := &Config{
		Cache: CacheConfig{
			Type: "file",
			TTL:  31536000,
		},
	}

	// Layer 1: YAML file
	configPath, explicit := resolveConfigPath(flags.ConfigPath)
	cfg.configPath = configPath
	if configPath != "" {
		if data, err := os.ReadFile(configPath); err == nil {
			vlog.V1("config: loaded from %s", configPath)
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parsing config %s: %w", configPath, err)
			}
		} else if explicit {
			return nil, fmt.Errorf("reading config %s: %w", configPath, err)
		} else {
			vlog.V2("config: %s not found, skipping", configPath)
		}
	}

	// Validate: no bot has both secret and token in YAML
	if err := cfg.validateBotConfigs(); err != nil {
		return nil, err
	}

	// Validate: at most one chat marked as default
	if err := cfg.ValidateDefaultChat(); err != nil {
		return nil, err
	}

	// Layer 2: resolve bot — only if --bot is specified or there is exactly one bot
	if flags.Bot != "" || len(cfg.Bots) <= 1 {
		if err := cfg.resolveBot(flags.Bot); err != nil {
			return nil, err
		}
		if cfg.BotName != "" {
			vlog.V1("config: using bot %q (%s)", cfg.BotName, cfg.Host)
		}
	}

	// Layer 3: environment variables
	if err := applyEnv(cfg); err != nil {
		return nil, err
	}

	// Layer 4: CLI flags
	applyFlags(cfg, flags)

	// If env/flags replaced credentials, the resolved bot name is stale
	cfg.clearStaleBotName()

	// Determine multi-bot mode AFTER all overrides are applied.
	if flags.Bot == "" && len(cfg.Bots) > 1 && !cfg.hasCredentials() {
		cfg.multiBot = true
		vlog.V1("config: multi-bot mode (%d bots)", len(cfg.Bots))
	}

	// Validate required fields only in single-bot mode
	if !cfg.multiBot {
		if cfg.Host == "" {
			return nil, fmt.Errorf("host is required (--host, EXPRESS_BOTX_HOST, or config file)")
		}
		if cfg.BotID == "" {
			return nil, fmt.Errorf("bot id is required (--bot-id, EXPRESS_BOTX_BOT_ID, or config file)")
		}
		if cfg.BotSecret == "" && cfg.BotToken == "" {
			return nil, fmt.Errorf("bot secret or token is required (--secret, --token, EXPRESS_BOTX_SECRET, EXPRESS_BOTX_TOKEN, or config file)")
		}
	}

	return cfg, nil
}

// IsMultiBot returns true if the config was loaded in multi-bot serve mode.
func (c *Config) IsMultiBot() bool {
	return c.multiBot
}

// HTTPTimeout returns the HTTP client timeout for the bot.
// Defaults to 10 seconds if not configured.
func (c *Config) HTTPTimeout() time.Duration {
	if c.BotTimeout > 0 {
		return time.Duration(c.BotTimeout) * time.Second
	}
	return 10 * time.Second
}

// CacheKey returns the composite cache key for token storage.
func (c *Config) CacheKey() string {
	return c.Host + ":" + c.BotID
}

func (c *Config) resolveBot(botFlag string) error {
	if botFlag != "" {
		bot, ok := c.Bots[botFlag]
		if !ok {
			return fmt.Errorf("unknown bot %q, available: %s", botFlag, c.botNames())
		}
		c.Host = bot.Host
		c.BotID = bot.ID
		c.BotSecret = bot.Secret
		c.BotToken = bot.Token
		c.BotName = botFlag
		c.BotTimeout = bot.Timeout
		return nil
	}

	switch len(c.Bots) {
	case 0:
		// no bots in config — rely on env/flags
	case 1:
		for name, bot := range c.Bots {
			c.Host = bot.Host
			c.BotID = bot.ID
			c.BotSecret = bot.Secret
			c.BotToken = bot.Token
			c.BotName = name
			c.BotTimeout = bot.Timeout
		}
	default:
		return fmt.Errorf("multiple bots configured, specify one with --bot: %s", c.botNames())
	}
	return nil
}

func (c *Config) botNames() string {
	return strings.Join(c.BotNames(), ", ")
}

// BotNames returns sorted bot names from the config.
func (c *Config) BotNames() []string {
	names := make([]string, 0, len(c.Bots))
	for k := range c.Bots {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// BotEntry is a bot summary for display (no secrets).
type BotEntry struct {
	Name string `json:"name"`
	Host string `json:"host"`
	ID   string `json:"id"`
}

// BotEntries returns sorted bot entries for display.
func (c *Config) BotEntries() []BotEntry {
	names := c.BotNames()
	entries := make([]BotEntry, 0, len(names))
	for _, name := range names {
		b := c.Bots[name]
		entries = append(entries, BotEntry{Name: name, Host: b.Host, ID: b.ID})
	}
	return entries
}

// ChatEntry is a chat alias summary for display.
type ChatEntry struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Bot     string `json:"bot,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// ChatEntries returns sorted chat entries for display.
func (c *Config) ChatEntries() []ChatEntry {
	names := make([]string, 0, len(c.Chats))
	for k := range c.Chats {
		names = append(names, k)
	}
	sort.Strings(names)
	entries := make([]ChatEntry, 0, len(names))
	for _, name := range names {
		chat := c.Chats[name]
		entries = append(entries, ChatEntry{Name: name, ID: chat.ID, Bot: chat.Bot, Default: chat.Default})
	}
	return entries
}

// ValidateFormat returns an error if Format is not "text" or "json".
func (c *Config) ValidateFormat() error {
	if c.Format != "text" && c.Format != "json" {
		return fmt.Errorf("invalid format %q: must be \"text\" or \"json\"", c.Format)
	}
	return nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ResolveChatID resolves ChatID: if it looks like a UUID, use as-is;
// otherwise look it up in the Chats alias map.
func (c *Config) ResolveChatID() error {
	_, err := c.ResolveChatIDWithBot()
	return err
}

// ResolveChatIDWithBot resolves ChatID and returns the bound bot name (if any).
func (c *Config) ResolveChatIDWithBot() (botName string, err error) {
	if c.ChatID == "" || uuidRe.MatchString(c.ChatID) {
		return "", nil
	}
	if chat, ok := c.Chats[c.ChatID]; ok {
		c.ChatID = chat.ID
		return chat.Bot, nil
	}
	names := make([]string, 0, len(c.Chats))
	for k := range c.Chats {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", fmt.Errorf("unknown chat %q (no aliases configured)", c.ChatID)
	}
	return "", fmt.Errorf("unknown chat alias %q, available: %s", c.ChatID, strings.Join(names, ", "))
}

// RequireChatID resolves aliases and returns an error if ChatID is empty.
// If ChatID is not set and there is exactly one alias, it is used automatically.
func (c *Config) RequireChatID() error {
	_, err := c.RequireChatIDWithBot()
	return err
}

// RequireChatIDWithBot resolves aliases, requires ChatID, and returns the bound bot name.
func (c *Config) RequireChatIDWithBot() (botName string, err error) {
	botName, err = c.ResolveChatIDWithBot()
	if err != nil {
		return "", err
	}
	if c.ChatID != "" {
		return botName, nil
	}
	switch len(c.Chats) {
	case 0:
		return "", fmt.Errorf("chat is required: use --chat-id or configure aliases in config (chats section)")
	case 1:
		for _, chat := range c.Chats {
			c.ChatID = chat.ID
			return chat.Bot, nil
		}
	default:
		if alias, chat := c.DefaultChat(); alias != "" {
			c.ChatID = chat.ID
			return chat.Bot, nil
		}
		names := make([]string, 0, len(c.Chats))
		for k := range c.Chats {
			names = append(names, k)
		}
		sort.Strings(names)
		return "", fmt.Errorf("multiple chats configured, specify one with --chat-id: %s", strings.Join(names, ", "))
	}
	return "", nil // unreachable
}

// ResolveChatBot returns the bot name bound to a chat alias. Empty if not bound.
func (c *Config) ResolveChatBot(chatAlias string) string {
	if chat, ok := c.Chats[chatAlias]; ok {
		return chat.Bot
	}
	return ""
}

// ValidateChatBots checks that all bot references in chats point to existing bots.
// If strict is true (CLI send, serve --fail-fast), returns an error.
// If strict is false (serve without --fail-fast), logs a warning and clears invalid bindings.
func (c *Config) ValidateChatBots(strict bool) error {
	for name, chat := range c.Chats {
		if chat.Bot == "" {
			continue
		}
		if _, ok := c.Bots[chat.Bot]; !ok {
			if strict {
				return fmt.Errorf("chat %q references unknown bot %q, available: %s", name, chat.Bot, c.botNames())
			}
			vlog.V1("config: chat %q references unknown bot %q, ignoring bot binding", name, chat.Bot)
			chat.Bot = ""
			c.Chats[name] = chat
		}
	}
	return nil
}

// ValidateDefaultChat checks that at most one chat is marked as default.
func (c *Config) ValidateDefaultChat() error {
	var defaults []string
	for name, chat := range c.Chats {
		if chat.Default {
			defaults = append(defaults, name)
		}
	}
	if len(defaults) > 1 {
		sort.Strings(defaults)
		return fmt.Errorf("multiple chats marked as default: %s — only one allowed",
			strings.Join(defaults, ", "))
	}
	return nil
}

// DefaultChat returns the alias and config of the chat marked as default.
// Returns empty alias if no default is configured.
func (c *Config) DefaultChat() (alias string, chat ChatConfig) {
	for name, ch := range c.Chats {
		if ch.Default {
			return name, ch
		}
	}
	return "", ChatConfig{}
}

// resolveChatBotFromFlags looks up the chat-bound bot from a ChatID flag value
// without mutating config state. If chatID is empty, returns a bot from the single
// chat alias or from the default chat's bot binding (mirrors RequireChatID auto-select).
func (c *Config) resolveChatBotFromFlags(chatID string) string {
	if chatID != "" {
		if chat, ok := c.Chats[chatID]; ok {
			return chat.Bot
		}
		return ""
	}
	// No explicit chat — if exactly one alias has a bot, use it
	if len(c.Chats) == 1 {
		for _, chat := range c.Chats {
			return chat.Bot
		}
	}
	// Multiple chats — check if the default chat has a bot binding
	if _, chat := c.DefaultChat(); chat.Bot != "" {
		return chat.Bot
	}
	return ""
}

// ApplyChatBot sets the resolved bot from a chat binding.
// Used when --bot is not specified but the chat has a default bot.
func (c *Config) ApplyChatBot(botName string) error {
	bot, ok := c.Bots[botName]
	if !ok {
		return fmt.Errorf("unknown bot %q, available: %s", botName, c.botNames())
	}
	c.Host = bot.Host
	c.BotID = bot.ID
	c.BotSecret = bot.Secret
	c.BotToken = bot.Token
	c.BotName = botName
	c.BotTimeout = bot.Timeout
	return nil
}

// ConfigPath returns the resolved config file path.
func (c *Config) ConfigPath() string {
	return c.configPath
}

// SaveConfig writes the config back to its file.
func (c *Config) SaveConfig() error {
	if c.configPath == "" {
		return fmt.Errorf("config path is not set")
	}
	if err := os.MkdirAll(filepath.Dir(c.configPath), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(c.configPath, data, 0o644); err != nil {
		return fmt.Errorf("writing config %s: %w", c.configPath, err)
	}
	return nil
}

// LoadMinimal reads the config file without resolving bot or validating fields.
// Used for alias/bot management commands.
func LoadMinimal(flags Flags) (*Config, error) {
	cfg := &Config{
		Cache: CacheConfig{
			Type: "file",
			TTL:  31536000,
		},
	}

	configPath, explicit := resolveConfigPath(flags.ConfigPath)
	if configPath == "" {
		configPath = "express-botx.yaml"
	}
	cfg.configPath = configPath
	if data, err := os.ReadFile(configPath); err == nil {
		vlog.V1("config: loaded from %s", configPath)
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config %s: %w", configPath, err)
		}
	} else if explicit {
		return nil, fmt.Errorf("reading config %s: %w", configPath, err)
	} else {
		vlog.V2("config: %s not found, using defaults", configPath)
	}

	// Apply only format flag for LoadMinimal
	if flags.Format != "" {
		cfg.Format = flags.Format
	}
	if cfg.Format == "" {
		cfg.Format = "text"
	}

	return cfg, nil
}

// resolveConfigPath determines the config file path from: CLI flag → env → auto-discovery.
// Returns the path and whether it was explicitly specified (flag or env).
func resolveConfigPath(flagPath string) (path string, explicit bool) {
	if flagPath != "" {
		return flagPath, true
	}
	if envPath := os.Getenv("EXPRESS_BOTX_CONFIG"); envPath != "" {
		return envPath, true
	}
	if found := findConfigFile(); found != "" {
		return found, false
	}
	return "", false
}

// findConfigFile searches for a config file in standard locations:
// 1. ./express-botx.yaml or ./express-botx.yml (current directory)
// 2. <UserConfigDir>/express-botx/config.yaml (platform-specific)
//    - macOS: ~/Library/Application Support/express-botx/config.yaml
//    - Linux: ~/.config/express-botx/config.yaml
//    - Windows: %AppData%/express-botx/config.yaml
// Returns empty string if no config file is found.
func findConfigFile() string {
	// 1. Current directory
	for _, name := range []string{"express-botx.yaml", "express-botx.yml"} {
		if _, err := os.Stat(name); err == nil {
			abs, err := filepath.Abs(name)
			if err == nil {
				return abs
			}
			return name
		}
	}

	// 2. Platform config directories
	var configDirs []string
	if dir, err := os.UserConfigDir(); err == nil {
		configDirs = append(configDirs, dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		dotConfig := filepath.Join(home, ".config")
		if len(configDirs) == 0 || configDirs[0] != dotConfig {
			configDirs = append(configDirs, dotConfig)
		}
	}
	for _, dir := range configDirs {
		p := filepath.Join(dir, "express-botx", "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

// validateBotConfigs checks that no bot in YAML has both secret and token.
func (c *Config) validateBotConfigs() error {
	for name, bot := range c.Bots {
		if bot.Secret != "" && bot.Token != "" {
			return fmt.Errorf("bot %q has both secret and token, use one", name)
		}
	}
	return nil
}

// hasCredentials returns true if the config has enough to authenticate.
func (c *Config) hasCredentials() bool {
	return c.Host != "" && c.BotID != "" && (c.BotSecret != "" || c.BotToken != "")
}

// clearStaleBotName resets BotName if env/flags overrode the credentials
// so they no longer match the named config bot.
func (c *Config) clearStaleBotName() {
	if c.BotName == "" {
		return
	}
	bot, ok := c.Bots[c.BotName]
	if !ok {
		return
	}
	if c.Host != bot.Host || c.BotID != bot.ID {
		vlog.V1("config: credentials overridden, clearing bot name %q", c.BotName)
		c.BotName = ""
		return
	}
	if bot.Secret != "" && c.BotSecret != bot.Secret {
		vlog.V1("config: credentials overridden, clearing bot name %q", c.BotName)
		c.BotName = ""
	} else if bot.Token != "" && c.BotToken != bot.Token {
		vlog.V1("config: credentials overridden, clearing bot name %q", c.BotName)
		c.BotName = ""
	}
}

func applyEnv(cfg *Config) error {
	if v := os.Getenv("EXPRESS_BOTX_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("EXPRESS_BOTX_BOT_ID"); v != "" {
		cfg.BotID = v
	}

	envSecret := os.Getenv("EXPRESS_BOTX_SECRET")
	envToken := os.Getenv("EXPRESS_BOTX_TOKEN")
	if envSecret != "" && envToken != "" {
		return fmt.Errorf("both EXPRESS_BOTX_SECRET and EXPRESS_BOTX_TOKEN are set, use one")
	}
	if envSecret != "" {
		cfg.BotSecret = envSecret
		cfg.BotToken = "" // env secret wins over config token
	}
	if envToken != "" {
		cfg.BotToken = envToken
		cfg.BotSecret = "" // env token wins over config secret
	}

	if v := os.Getenv("EXPRESS_BOTX_CACHE_TYPE"); v != "" {
		cfg.Cache.Type = v
	}
	if v := os.Getenv("EXPRESS_BOTX_CACHE_FILE_PATH"); v != "" {
		cfg.Cache.FilePath = v
	}
	if v := os.Getenv("EXPRESS_BOTX_CACHE_VAULT_URL"); v != "" {
		cfg.Cache.VaultURL = v
	}
	if v := os.Getenv("EXPRESS_BOTX_CACHE_VAULT_PATH"); v != "" {
		cfg.Cache.VaultPath = v
	}
	if v := os.Getenv("EXPRESS_BOTX_CACHE_TTL"); v != "" {
		if ttl, err := strconv.Atoi(v); err == nil {
			cfg.Cache.TTL = ttl
		}
	}
	return nil
}

func applyFlags(cfg *Config, flags Flags) {
	if flags.Host != "" {
		cfg.Host = flags.Host
	}
	if flags.BotID != "" {
		cfg.BotID = flags.BotID
	}
	if flags.Secret != "" {
		cfg.BotSecret = flags.Secret
		cfg.BotToken = "" // flag secret wins over env/config token
	}
	if flags.Token != "" {
		cfg.BotToken = flags.Token
		cfg.BotSecret = "" // flag token wins over env/config secret
	}
	if flags.ChatID != "" {
		cfg.ChatID = flags.ChatID
	}
	if flags.NoCache {
		cfg.Cache.Type = "none"
	}
	if flags.Format != "" {
		cfg.Format = flags.Format
	}
	if cfg.Format == "" {
		cfg.Format = "text"
	}
}
