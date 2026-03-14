package cmd

import (
	"context"
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

	// Docs: enabled by default
	srvCfg.EnableDocs = true
	if cfg.Server.Docs != nil && !*cfg.Server.Docs {
		srvCfg.EnableDocs = false
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

	// Bot secret auth
	if cfg.Server.AllowBotSecretAuth {
		srvCfg.BotSignatures = make(map[string]string)
		if cfg.IsMultiBot() {
			// Multi-bot: collect signatures from all bots, bind each to its name
			for name, bot := range cfg.Bots {
				secretKey, err := secret.Resolve(bot.Secret)
				if err != nil {
					return fmt.Errorf("resolving secret for bot %q: %w", name, err)
				}
				srvCfg.BotSignatures[auth.BuildSignature(bot.ID, secretKey)] = name
			}
		} else {
			secretKey, err := secret.Resolve(cfg.BotSecret)
			if err != nil {
				return fmt.Errorf("resolving bot secret for auth: %w", err)
			}
			// Single-bot: empty bot name — no binding needed
			srvCfg.BotSignatures[auth.BuildSignature(cfg.BotID, secretKey)] = ""
		}
		srvCfg.AllowBotSecretAuth = true
	}

	if len(keys) == 0 && !srvCfg.AllowBotSecretAuth {
		return fmt.Errorf("no API keys configured: use --api-key, EXPRESS_BOTX_SERVER_API_KEY, server.api_keys, or server.allow_bot_secret_auth in config")
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
	}

	// Build chat resolver
	chatResolver := func(chatID string) (string, error) {
		cfgCopy := *cfg
		cfgCopy.ChatID = chatID
		if err := cfgCopy.ResolveChatID(); err != nil {
			return "", err
		}
		return cfgCopy.ChatID, nil
	}

	// APM
	provider := apm.New()
	defer provider.Shutdown()

	var srvOpts []server.Option
	srvOpts = append(srvOpts, server.WithAPM(provider))

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
			client: botapi.NewClient(cfg.Host, ""),
			cache:  cache,
		}, nil
	}

	vlog.Info("serve: bot %s authenticated", name)
	return &botSender{
		cfg:    cfg,
		client: botapi.NewClient(cfg.Host, tok),
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

// sendResponseJSON is used for encoding sync_id from the BotX API response.
type sendResponseJSON struct {
	OK     bool   `json:"ok"`
	SyncID string `json:"sync_id,omitempty"`
}

func init() {
	// Ensure json package is used (metadata field).
	_ = json.RawMessage{}
}
