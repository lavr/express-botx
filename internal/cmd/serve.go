package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/lavr/express-botx/internal/apm"
	"github.com/lavr/express-botx/internal/auth"
	"github.com/lavr/express-botx/internal/errtrack"
	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/secret"
	"github.com/lavr/express-botx/internal/server"
	"github.com/lavr/express-botx/internal/token"
)

func runServe(args []string, deps Deps) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var listenFlag string
	var apiKeyFlag string
	var failFast bool

	globalFlags(fs, &flags)
	fs.StringVar(&listenFlag, "listen", "", "address to listen on (overrides config)")
	fs.StringVar(&apiKeyFlag, "api-key", "", "API key for quick start (overrides config)")
	fs.BoolVar(&failFast, "fail-fast", false, "exit if bot authentication fails at startup")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, `Usage: express-botx serve [options]

Start an HTTP server for sending messages via API.

Options:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if flags.Secret != "" && flags.Token != "" {
		return fmt.Errorf("--secret and --token are mutually exclusive")
	}

	cfg, err := config.LoadForServe(flags)
	if err != nil {
		return err
	}

	// Build server config
	srvCfg := server.Config{
		Listen:   cfg.Server.Listen,
		BasePath: cfg.Server.BasePath,
	}

	// Env overrides
	if v := os.Getenv("EXPRESS_BOTX_SERVER_LISTEN"); v != "" {
		srvCfg.Listen = v
	}
	if v := os.Getenv("EXPRESS_BOTX_SERVER_BASE_PATH"); v != "" {
		srvCfg.BasePath = v
	}

	// CLI flag overrides
	if listenFlag != "" {
		srvCfg.Listen = listenFlag
	}

	// Defaults
	if srvCfg.Listen == "" {
		srvCfg.Listen = ":8080"
	}
	if srvCfg.BasePath == "" {
		srvCfg.BasePath = "/api/v1"
	}

	// External URL for OpenAPI docs
	srvCfg.ExternalURL = cfg.Server.ExternalURL
	if v := os.Getenv("EXPRESS_BOTX_SERVER_EXTERNAL_URL"); v != "" {
		srvCfg.ExternalURL = v
	}

	// Docs: enabled by default
	srvCfg.EnableDocs = true
	if cfg.Server.Docs != nil && !*cfg.Server.Docs {
		srvCfg.EnableDocs = false
	}
	srvCfg.AppVersion = Version

	// Default chat alias for /send when chat_id is omitted
	if alias, _ := cfg.DefaultChat(); alias != "" {
		srvCfg.DefaultChatAlias = alias
	}

	// Resolve API keys
	keys, err := resolveAPIKeys(cfg.Server.APIKeys)
	if err != nil {
		return fmt.Errorf("resolving api keys: %w", err)
	}

	// CLI --api-key flag adds/overrides
	if apiKeyFlag != "" {
		resolved, err := secret.Resolve(apiKeyFlag)
		if err != nil {
			return fmt.Errorf("resolving --api-key: %w", err)
		}
		keys = append(keys, server.ResolvedKey{Name: "cli", Key: resolved})
	}

	// Env single key
	if v := os.Getenv("EXPRESS_BOTX_SERVER_API_KEY"); v != "" {
		resolved, err := secret.Resolve(v)
		if err != nil {
			return fmt.Errorf("resolving EXPRESS_BOTX_SERVER_API_KEY: %w", err)
		}
		keys = append(keys, server.ResolvedKey{Name: "env", Key: resolved})
	}

	// Bot secret auth (only for bots with secret, not token-only)
	if cfg.Server.AllowBotSecretAuth {
		srvCfg.BotSignatures = make(map[string]string)
		if cfg.IsMultiBot() {
			for name, bot := range cfg.Bots {
				if bot.Secret == "" {
					continue // token-only bot — skip
				}
				secretKey, err := secret.Resolve(bot.Secret)
				if err != nil {
					return fmt.Errorf("resolving secret for bot %q: %w", name, err)
				}
				srvCfg.BotSignatures[auth.BuildSignature(bot.ID, secretKey)] = name
			}
		} else if cfg.BotSecret != "" {
			secretKey, err := secret.Resolve(cfg.BotSecret)
			if err != nil {
				return fmt.Errorf("resolving bot secret for auth: %w", err)
			}
			srvCfg.BotSignatures[auth.BuildSignature(cfg.BotID, secretKey)] = ""
		}
		if len(srvCfg.BotSignatures) > 0 {
			srvCfg.AllowBotSecretAuth = true
		}
	}

	if len(keys) == 0 && !srvCfg.AllowBotSecretAuth {
		key, err := generateAPIKey()
		if err != nil {
			return fmt.Errorf("generating api key: %w", err)
		}
		keys = append(keys, server.ResolvedKey{Name: "auto", Key: key})
		vlog.Info("serve: no API keys configured, generated key: %s", key)
	}
	srvCfg.Keys = keys

	// Build send function and authenticate.
	// If eXpress is unavailable at startup, the server still starts —
	// bots that failed auth are retried in the background every 10 seconds.
	// Requests to unavailable bots return 503 until auth succeeds.
	var sendFn server.SendFunc

	if cfg.IsMultiBot() {
		senders := make(map[string]*botSender, len(cfg.Bots))
		botNames := cfg.BotNames()
		for _, name := range botNames {
			bot := cfg.Bots[name]
			botCfg := *cfg
			botCfg.Host = bot.Host
			botCfg.BotID = bot.ID
			botCfg.BotSecret = bot.Secret
			botCfg.BotToken = bot.Token
			botCfg.BotName = name
			sender, err := newBotSender(&botCfg, failFast)
			if err != nil {
				return err
			}
			senders[name] = sender
		}

		srvCfg.BotNames = botNames

		sendFn = func(ctx context.Context, p *server.SendPayload) (string, error) {
			return senders[p.Bot].Send(ctx, p)
		}
	} else {
		sender, err := newBotSender(cfg, failFast)
		if err != nil {
			return err
		}
		sendFn = sender.Send
		srvCfg.SingleBotName = cfg.BotName
	}

	// Validate chat-bot bindings
	if err := cfg.ValidateChatBots(failFast); err != nil {
		return err
	}

	// Build chat resolver
	chatResolver := func(chatID string) (server.ChatResolveResult, error) {
		cfgCopy := *cfg
		cfgCopy.ChatID = chatID
		botName, err := cfgCopy.ResolveChatIDWithBot()
		if err != nil {
			return server.ChatResolveResult{}, err
		}
		return server.ChatResolveResult{ChatID: cfgCopy.ChatID, Bot: botName}, nil
	}

	// APM
	provider := apm.New()
	defer provider.Shutdown()

	// Error tracking
	tracker := errtrack.New()
	defer tracker.Flush()

	var srvOpts []server.Option
	srvOpts = append(srvOpts, server.WithAPM(provider))
	srvOpts = append(srvOpts, server.WithErrTracker(tracker))
	srvOpts = append(srvOpts, server.WithConfigInfo(runtimeBotEntries(cfg), runtimeChatEntries(cfg)))

	// Alertmanager endpoint
	if am := cfg.Server.Alertmanager; am != nil {
		amCfg, err := buildAlertmanagerConfig(am, cfg.ConfigPath())
		if err != nil {
			return err
		}
		// If no default_chat_id, use single chat alias as fallback
		if amCfg.DefaultChatID == "" && len(cfg.Chats) == 1 {
			for alias := range cfg.Chats {
				amCfg.FallbackChatID = alias
				vlog.V1("alertmanager: using single chat alias %q as fallback", alias)
			}
		}
		srvOpts = append(srvOpts, server.WithAlertmanager(amCfg))
	}

	// Grafana endpoint
	if gr := cfg.Server.Grafana; gr != nil {
		grCfg, err := buildGrafanaConfig(gr, cfg.ConfigPath())
		if err != nil {
			return err
		}
		if grCfg.DefaultChatID == "" && len(cfg.Chats) == 1 {
			for alias := range cfg.Chats {
				grCfg.FallbackChatID = alias
				vlog.V1("grafana: using single chat alias %q as fallback", alias)
			}
		}
		srvOpts = append(srvOpts, server.WithGrafana(grCfg))
	}

	srv := server.New(srvCfg, sendFn, chatResolver, srvOpts...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return srv.Run(ctx)
}

func resolveAPIKeys(keys []config.APIKeyConfig) ([]server.ResolvedKey, error) {
	resolved := make([]server.ResolvedKey, 0, len(keys))
	for _, k := range keys {
		val, err := secret.Resolve(k.Key)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k.Name, err)
		}
		resolved = append(resolved, server.ResolvedKey{Name: k.Name, Key: val})
	}
	return resolved, nil
}

func buildSendRequest(p *server.SendPayload) *botapi.SendRequest {
	params := &botapi.SendParams{
		ChatID:   p.ChatID,
		Message:  p.Message,
		Status:   p.Status,
		Metadata: p.Metadata,
	}
	if p.File != nil {
		params.File = botapi.BuildFileAttachmentFromBase64(p.File.Name, p.File.Data)
	}
	if p.Opts != nil {
		params.Silent = p.Opts.Silent
		params.Stealth = p.Opts.Stealth
		params.ForceDND = p.Opts.ForceDND
		params.NoNotify = p.Opts.NoNotify
	}
	return botapi.BuildSendRequest(params)
}

func buildAlertmanagerConfig(am *config.AlertmanagerYAMLConfig, configPath string) (*server.AlertmanagerConfig, error) {
	severities := am.ErrorSeverities
	if len(severities) == 0 {
		severities = []string{"critical", "warning"}
	}

	// Determine template source: template_file > template > default
	var tmplStr string
	switch {
	case am.TemplateFile != "":
		path := am.TemplateFile
		if !filepath.IsAbs(path) && configPath != "" {
			path = filepath.Join(filepath.Dir(configPath), path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading alertmanager template %s: %w", path, err)
		}
		tmplStr = string(data)
		vlog.V1("alertmanager: loaded template from %s", path)
	case am.Template != "":
		tmplStr = am.Template
		vlog.V1("alertmanager: using inline template")
	default:
		tmplStr = server.DefaultAlertmanagerTemplate
		vlog.V1("alertmanager: using default template")
	}

	tmpl, err := server.ParseAlertmanagerTemplate(tmplStr)
	if err != nil {
		return nil, err
	}

	return &server.AlertmanagerConfig{
		DefaultChatID:   am.DefaultChatID,
		ErrorSeverities: severities,
		Template:        tmpl,
	}, nil
}

func buildGrafanaConfig(gr *config.GrafanaYAMLConfig, configPath string) (*server.GrafanaConfig, error) {
	states := gr.ErrorStates
	if len(states) == 0 {
		states = []string{"alerting"}
	}

	var tmplStr string
	switch {
	case gr.TemplateFile != "":
		path := gr.TemplateFile
		if !filepath.IsAbs(path) && configPath != "" {
			path = filepath.Join(filepath.Dir(configPath), path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading grafana template %s: %w", path, err)
		}
		tmplStr = string(data)
		vlog.V1("grafana: loaded template from %s", path)
	case gr.Template != "":
		tmplStr = gr.Template
		vlog.V1("grafana: using inline template")
	default:
		tmplStr = server.DefaultGrafanaTemplate
		vlog.V1("grafana: using default template")
	}

	tmpl, err := server.ParseGrafanaTemplate(tmplStr)
	if err != nil {
		return nil, err
	}

	return &server.GrafanaConfig{
		DefaultChatID: gr.DefaultChatID,
		ErrorStates:   states,
		Template:      tmpl,
	}, nil
}

// botSender wraps a bot client with lazy authentication: if auth fails at
// startup, the first request triggers authentication on the fly.
type botSender struct {
	cfg    *config.Config
	client *botapi.Client
	cache  token.Cache
}

func newBotSender(cfg *config.Config, failFast bool) (*botSender, error) {
	name := cfg.BotName
	if name == "" {
		name = cfg.Host
	}

	tok, cache, err := authenticate(cfg)
	if err != nil {
		if failFast {
			return nil, fmt.Errorf("authenticating bot %s: %w", name, err)
		}
		vlog.Info("serve: bot %s auth failed at startup, will authenticate on first request: %v", name, err)
		return &botSender{
			cfg:    cfg,
			client: botapi.NewClient(cfg.Host, "", cfg.HTTPTimeout()),
			cache:  cache,
		}, nil
	}

	vlog.Info("serve: bot %s authenticated", name)
	return &botSender{
		cfg:    cfg,
		client: botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout()),
		cache:  cache,
	}, nil
}

func (bs *botSender) Send(ctx context.Context, p *server.SendPayload) (string, error) {
	// Lazy auth: if token is empty, authenticate now
	if bs.client.Token == "" {
		tok, err := refreshToken(bs.cfg, bs.cache)
		if err != nil {
			return "", fmt.Errorf("authenticating bot: %w", err)
		}
		bs.client.Token = tok
	}

	sr := buildSendRequest(p)
	syncID, err := bs.client.SendWithSyncID(ctx, sr)
	if err != nil {
		if errors.Is(err, botapi.ErrUnauthorized) {
			if bs.cfg.BotToken != "" {
				return "", fmt.Errorf("bot token rejected (401), re-configure token for bot %q", bs.cfg.BotName)
			}
			newTok, refreshErr := refreshToken(bs.cfg, bs.cache)
			if refreshErr != nil {
				return "", fmt.Errorf("refreshing token: %w", refreshErr)
			}
			bs.client.Token = newTok
			return bs.client.SendWithSyncID(ctx, sr)
		}
		return "", err
	}
	return syncID, nil
}

func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sendResponseJSON is used for encoding sync_id from the BotX API response.
type sendResponseJSON struct {
	OK     bool   `json:"ok"`
	SyncID string `json:"sync_id,omitempty"`
}

func init() {
	// Ensure json package is used (metadata field).
	_ = json.RawMessage{}
}

// runtimeBotEntries returns bot entries reflecting the actual runtime state.
// In multi-bot mode, returns all configured bots.
// In single-bot mode with a named bot, returns only that bot.
// In single-bot mode from env/flags (unnamed), returns empty list.
func runtimeBotEntries(cfg *config.Config) []config.BotEntry {
	if cfg.IsMultiBot() {
		return cfg.BotEntries()
	}
	if cfg.BotName == "" {
		return nil
	}
	return []config.BotEntry{{Name: cfg.BotName, Host: cfg.Host, ID: cfg.BotID}}
}

// runtimeChatEntries returns chat entries reflecting the actual runtime state.
// Bot bindings that reference bots not available at runtime are cleared.
func runtimeChatEntries(cfg *config.Config) []config.ChatEntry {
	entries := cfg.ChatEntries()
	if cfg.IsMultiBot() {
		return entries
	}
	// Single-bot mode: clear bot bindings that don't match the running bot
	for i := range entries {
		if entries[i].Bot != "" && entries[i].Bot != cfg.BotName {
			entries[i].Bot = ""
		}
	}
	return entries
}
