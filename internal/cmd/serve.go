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
)

func runServe(args []string, deps Deps) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var listenFlag string
	var apiKeyFlag string

	globalFlags(fs, &flags)
	fs.StringVar(&listenFlag, "listen", "", "address to listen on (overrides config)")
	fs.StringVar(&apiKeyFlag, "api-key", "", "API key for quick start (overrides config)")
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
	if v := os.Getenv("EXPRESS_SERVER_LISTEN"); v != "" {
		srvCfg.Listen = v
	}
	if v := os.Getenv("EXPRESS_SERVER_BASE_PATH"); v != "" {
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
	if v := os.Getenv("EXPRESS_SERVER_API_KEY"); v != "" {
		resolved, err := secret.Resolve(v)
		if err != nil {
			return fmt.Errorf("resolving EXPRESS_SERVER_API_KEY: %w", err)
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
		return fmt.Errorf("no API keys configured: use --api-key, EXPRESS_SERVER_API_KEY, server.api_keys, or server.allow_bot_secret_auth in config")
	}
	srvCfg.Keys = keys

	// Build send function and authenticate
	var sendFn server.SendFunc

	if cfg.IsMultiBot() {
		// Multi-bot mode: authenticate all bots, build dispatcher
		senders := make(map[string]server.SendFunc, len(cfg.Bots))
		botNames := cfg.BotNames()
		for _, name := range botNames {
			bot := cfg.Bots[name]
			botCfg := *cfg
			botCfg.Host = bot.Host
			botCfg.BotID = bot.ID
			botCfg.BotSecret = bot.Secret
			botCfg.BotName = name

			tok, cache, err := authenticate(&botCfg)
			if err != nil {
				return fmt.Errorf("authenticating bot %q: %w", name, err)
			}
			client := botapi.NewClient(botCfg.Host, tok)
			localCfg := botCfg
			localCache := cache

			senders[name] = func(ctx context.Context, p *server.SendPayload) (string, error) {
				sr := buildSendRequest(p)
				syncID, err := client.SendWithSyncID(ctx, sr)
				if err != nil {
					if errors.Is(err, botapi.ErrUnauthorized) {
						newTok, refreshErr := refreshToken(&localCfg, localCache)
						if refreshErr != nil {
							return "", fmt.Errorf("refreshing token: %w", refreshErr)
						}
						client.Token = newTok
						return client.SendWithSyncID(ctx, sr)
					}
					return "", err
				}
				return syncID, nil
			}

			vlog.V1("serve: bot %q authenticated (%s)", name, bot.Host)
		}

		srvCfg.BotNames = botNames

		sendFn = func(ctx context.Context, p *server.SendPayload) (string, error) {
			fn := senders[p.Bot]
			return fn(ctx, p)
		}
	} else {
		// Single-bot mode
		tok, cache, err := authenticate(cfg)
		if err != nil {
			return err
		}
		client := botapi.NewClient(cfg.Host, tok)

		sendFn = func(ctx context.Context, p *server.SendPayload) (string, error) {
			sr := buildSendRequest(p)
			syncID, err := client.SendWithSyncID(ctx, sr)
			if err != nil {
				if errors.Is(err, botapi.ErrUnauthorized) {
					newTok, refreshErr := refreshToken(cfg, cache)
					if refreshErr != nil {
						return "", fmt.Errorf("refreshing token: %w", refreshErr)
					}
					client.Token = newTok
					return client.SendWithSyncID(ctx, sr)
				}
				return "", err
			}
			return syncID, nil
		}
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

// sendResponseJSON is used for encoding sync_id from the BotX API response.
type sendResponseJSON struct {
	OK     bool   `json:"ok"`
	SyncID string `json:"sync_id,omitempty"`
}

func init() {
	// Ensure json package is used (metadata field).
	_ = json.RawMessage{}
}
