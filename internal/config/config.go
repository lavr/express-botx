package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	vlog "github.com/lavr/express-bot/internal/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Bots   map[string]BotConfig `yaml:"bots,omitempty"`
	Chats  map[string]string    `yaml:"chats,omitempty"`
	Cache  CacheConfig          `yaml:"cache"`
	Server ServerConfig         `yaml:"server,omitempty"`

	// Resolved at runtime (not persisted).
	Host       string `yaml:"-"`
	BotID      string `yaml:"-"`
	BotSecret  string `yaml:"-"`
	BotName    string `yaml:"-"`
	ChatID     string `yaml:"-"`
	Format     string `yaml:"-"`
	configPath string
}

// ServerConfig holds HTTP server settings for the "serve" subcommand.
type ServerConfig struct {
	Listen             string                `yaml:"listen,omitempty"`
	BasePath           string                `yaml:"base_path,omitempty"`
	APIKeys            []APIKeyConfig        `yaml:"api_keys,omitempty"`
	AllowBotSecretAuth bool                  `yaml:"allow_bot_secret_auth,omitempty"`
	Alertmanager       *AlertmanagerYAMLConfig `yaml:"alertmanager,omitempty"`
}

// AlertmanagerYAMLConfig holds YAML settings for the alertmanager webhook endpoint.
type AlertmanagerYAMLConfig struct {
	DefaultChatID   string   `yaml:"default_chat_id,omitempty"`
	ErrorSeverities []string `yaml:"error_severities,omitempty"`
	Template        string   `yaml:"template,omitempty"`
	TemplateFile    string   `yaml:"template_file,omitempty"`
}

// APIKeyConfig defines a single API key for server authentication.
type APIKeyConfig struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"` // literal, env:VAR, or vault:path#key
}

type BotConfig struct {
	Host   string `yaml:"host"`
	ID     string `yaml:"id"`
	Secret string `yaml:"secret"`
}

type CacheConfig struct {
	Type      string `yaml:"type"`       // "none"|"file"|"vault"
	FilePath  string `yaml:"file_path"`
	VaultURL  string `yaml:"vault_url"`
	VaultPath string `yaml:"vault_path"`
	TTL       int    `yaml:"ttl"` // seconds, default 3600
}

// Flags holds CLI flag values for layering on top of file/env config.
type Flags struct {
	ConfigPath string
	Bot        string
	Host       string
	BotID      string
	Secret     string
	ChatID     string
	NoCache    bool
	Format     string
	Verbose    int
}

// Load reads configuration with layering: YAML file → resolve bot → env → CLI flags.
func Load(flags Flags) (*Config, error) {
	cfg := &Config{
		Cache: CacheConfig{
			Type: "none",
			TTL:  3600,
		},
	}

	// Layer 1: YAML file
	configPath := flags.ConfigPath
	explicit := configPath != ""
	if !explicit {
		configPath = findConfigFile()
	}
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

	// Layer 2: resolve bot from config
	if err := cfg.resolveBot(flags.Bot); err != nil {
		return nil, err
	}
	if cfg.BotName != "" {
		vlog.V1("config: using bot %q (%s)", cfg.BotName, cfg.Host)
	}

	// Layer 3: environment variables (override resolved bot)
	applyEnv(cfg)

	// Layer 4: CLI flags (highest priority)
	applyFlags(cfg, flags)

	vlog.V2("config: host=%s bot_id=%s cache=%s", cfg.Host, cfg.BotID, cfg.Cache.Type)

	// Validate required fields
	if cfg.Host == "" {
		return nil, fmt.Errorf("host is required (--host, EXPRESS_HOST, or config file)")
	}
	if cfg.BotID == "" {
		return nil, fmt.Errorf("bot id is required (--bot-uuid, EXPRESS_BOT_ID, or config file)")
	}
	if cfg.BotSecret == "" {
		return nil, fmt.Errorf("bot secret is required (--secret, EXPRESS_SECRET, or config file)")
	}

	return cfg, nil
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
		c.BotName = botFlag
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
			c.BotName = name
		}
	default:
		return fmt.Errorf("multiple bots configured, specify one with --bot: %s", c.botNames())
	}
	return nil
}

func (c *Config) botNames() string {
	names := make([]string, 0, len(c.Bots))
	for k := range c.Bots {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
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
	if c.ChatID == "" || uuidRe.MatchString(c.ChatID) {
		return nil
	}
	if uuid, ok := c.Chats[c.ChatID]; ok {
		c.ChatID = uuid
		return nil
	}
	names := make([]string, 0, len(c.Chats))
	for k := range c.Chats {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return fmt.Errorf("unknown chat %q (no aliases configured)", c.ChatID)
	}
	return fmt.Errorf("unknown chat alias %q, available: %s", c.ChatID, strings.Join(names, ", "))
}

// RequireChatID resolves aliases and returns an error if ChatID is empty.
// If ChatID is not set and there is exactly one alias, it is used automatically.
func (c *Config) RequireChatID() error {
	if err := c.ResolveChatID(); err != nil {
		return err
	}
	if c.ChatID != "" {
		return nil
	}
	switch len(c.Chats) {
	case 0:
		return fmt.Errorf("chat is required: use --chat-id or configure aliases in config (chats section)")
	case 1:
		for _, uuid := range c.Chats {
			c.ChatID = uuid
		}
		return nil
	default:
		names := make([]string, 0, len(c.Chats))
		for k := range c.Chats {
			names = append(names, k)
		}
		sort.Strings(names)
		return fmt.Errorf("multiple chats configured, specify one with --chat-id: %s", strings.Join(names, ", "))
	}
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
			Type: "none",
			TTL:  3600,
		},
	}

	configPath := flags.ConfigPath
	explicit := configPath != ""
	if !explicit {
		configPath = findConfigFile()
	}
	if configPath == "" {
		configPath = "express-bot.yaml"
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

// findConfigFile searches for a config file in standard locations:
// 1. ./express-bot.yaml or ./express-bot.yml (current directory)
// 2. <UserConfigDir>/express-bot/config.yaml (platform-specific)
//    - macOS: ~/Library/Application Support/express-bot/config.yaml
//    - Linux: ~/.config/express-bot/config.yaml
//    - Windows: %AppData%/express-bot/config.yaml
// Returns empty string if no config file is found.
func findConfigFile() string {
	// 1. Current directory
	for _, name := range []string{"express-bot.yaml", "express-bot.yml"} {
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
		p := filepath.Join(dir, "express-bot", "config.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("EXPRESS_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("EXPRESS_BOT_ID"); v != "" {
		cfg.BotID = v
	}
	if v := os.Getenv("EXPRESS_SECRET"); v != "" {
		cfg.BotSecret = v
	}
	if v := os.Getenv("EXPRESS_CACHE_TYPE"); v != "" {
		cfg.Cache.Type = v
	}
	if v := os.Getenv("EXPRESS_CACHE_FILE_PATH"); v != "" {
		cfg.Cache.FilePath = v
	}
	if v := os.Getenv("EXPRESS_CACHE_VAULT_URL"); v != "" {
		cfg.Cache.VaultURL = v
	}
	if v := os.Getenv("EXPRESS_CACHE_VAULT_PATH"); v != "" {
		cfg.Cache.VaultPath = v
	}
	if v := os.Getenv("EXPRESS_CACHE_TTL"); v != "" {
		if ttl, err := strconv.Atoi(v); err == nil {
			cfg.Cache.TTL = ttl
		}
	}
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
