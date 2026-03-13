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

	"github.com/lavr/express-bot/internal/auth"
	"github.com/lavr/express-bot/internal/botapi"
	"github.com/lavr/express-bot/internal/config"
	vlog "github.com/lavr/express-bot/internal/log"
	"github.com/lavr/express-bot/internal/secret"
	"github.com/lavr/express-bot/internal/server"
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
		fmt.Fprintf(deps.Stderr, `Usage: express-bot serve [options]

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

	cfg, err := config.Load(flags)
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
		secretKey, err := secret.Resolve(cfg.BotSecret)
		if err != nil {
			return fmt.Errorf("resolving bot secret for auth: %w", err)
		}
		srvCfg.AllowBotSecretAuth = true
		srvCfg.BotSignature = auth.BuildSignature(cfg.BotID, secretKey)
	}

	if len(keys) == 0 && !srvCfg.AllowBotSecretAuth {
		return fmt.Errorf("no API keys configured: use --api-key, EXPRESS_SERVER_API_KEY, server.api_keys, or server.allow_bot_secret_auth in config")
	}
	srvCfg.Keys = keys

	// Authenticate with BotX API
	tok, cache, err := authenticate(cfg)
	if err != nil {
		return err
	}
	client := botapi.NewClient(cfg.Host, tok)

	// Build send function with token refresh
	sendFn := func(ctx context.Context, p *server.SendPayload) (string, error) {
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

	// Build chat resolver
	chatResolver := func(chatID string) (string, error) {
		cfgCopy := *cfg
		cfgCopy.ChatID = chatID
		if err := cfgCopy.ResolveChatID(); err != nil {
			return "", err
		}
		return cfgCopy.ChatID, nil
	}

	// Alertmanager endpoint
	var srvOpts []server.Option
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
	sr := &botapi.SendRequest{
		GroupChatID: p.ChatID,
	}

	if p.Message != "" {
		sr.Notification = &botapi.SendNotification{
			Status:   p.Status,
			Body:     p.Message,
			Metadata: p.Metadata,
		}
		if p.Opts != nil && p.Opts.Silent {
			sr.Notification.Opts = &botapi.NotificationMsgOpts{
				SilentResponse: true,
			}
		}
	}

	if p.File != nil {
		sr.File = botapi.BuildFileAttachmentFromBase64(p.File.Name, p.File.Data)
	}

	if p.Opts != nil && (p.Opts.Stealth || p.Opts.ForceDND || p.Opts.NoNotify) {
		sr.Opts = &botapi.SendOpts{
			StealthMode: p.Opts.Stealth,
		}
		if p.Opts.ForceDND || p.Opts.NoNotify {
			sr.Opts.NotificationOpts = &botapi.DeliveryOpts{
				ForceDND: p.Opts.ForceDND,
			}
			if p.Opts.NoNotify {
				f := false
				sr.Opts.NotificationOpts.Send = &f
			}
		}
	}

	// If only file (no message) but we have metadata, still create notification
	if sr.Notification == nil && len(p.Metadata) > 0 {
		sr.Notification = &botapi.SendNotification{
			Status:   p.Status,
			Metadata: p.Metadata,
		}
	}

	return sr
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

// sendResponseJSON is used for encoding sync_id from the BotX API response.
type sendResponseJSON struct {
	OK     bool   `json:"ok"`
	SyncID string `json:"sync_id,omitempty"`
}

func init() {
	// Ensure json package is used (metadata field).
	_ = json.RawMessage{}
}
