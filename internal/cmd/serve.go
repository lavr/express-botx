package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lavr/express-botx/internal/apm"
	"github.com/lavr/express-botx/internal/auth"
	"github.com/lavr/express-botx/internal/errtrack"
	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/queue"
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
	var enqueueMode bool

	globalFlags(fs, &flags)
	fs.StringVar(&listenFlag, "listen", "", "address to listen on (overrides config)")
	fs.StringVar(&apiKeyFlag, "api-key", "", "API key for quick start (overrides config)")
	fs.BoolVar(&failFast, "fail-fast", false, "exit if bot authentication fails at startup")
	fs.BoolVar(&enqueueMode, "enqueue", false, "async mode: publish to queue instead of sending directly")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, `Usage: express-botx serve [options]

Start an HTTP server for sending messages via API.

Options:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if enqueueMode {
		return runServeEnqueue(flags, listenFlag, apiKeyFlag, deps)
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

	// Alertmanager endpoint (always enabled)
	am := cfg.Server.Alertmanager
	if am == nil {
		am = &config.AlertmanagerYAMLConfig{}
	}
	amCfg, err := buildAlertmanagerConfig(am, cfg.ConfigPath())
	if err != nil {
		return err
	}
	if amCfg.DefaultChatID == "" && len(cfg.Chats) == 1 {
		for alias := range cfg.Chats {
			amCfg.FallbackChatID = alias
			vlog.V1("alertmanager: using single chat alias %q as fallback", alias)
		}
	}
	srvOpts = append(srvOpts, server.WithAlertmanager(amCfg))

	// Grafana endpoint (always enabled)
	gr := cfg.Server.Grafana
	if gr == nil {
		gr = &config.GrafanaYAMLConfig{}
	}
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

	// Callback endpoints
	if cb := cfg.Server.Callbacks; cb != nil && len(cb.Rules) > 0 {
		if err := cb.Validate(); err != nil {
			return fmt.Errorf("callbacks config: %w", err)
		}
		// Validate handler types supported by CLI (library users may register custom types).
		for i, rule := range cb.Rules {
			switch rule.Handler.Type {
			case "exec", "webhook":
				// ok
			default:
				return fmt.Errorf("callbacks rule #%d: unsupported handler type %q (supported: exec, webhook)", i+1, rule.Handler.Type)
			}
		}
		// Verify that bot secrets are available when JWT verification is enabled.
		verifyJWT := cb.VerifyJWT == nil || *cb.VerifyJWT
		if verifyJWT {
			hasSecret := false
			if cfg.IsMultiBot() {
				for name, bot := range cfg.Bots {
					if bot.Secret != "" {
						hasSecret = true
					} else {
						vlog.V1("callbacks: bot %q has no secret configured; JWT verification will fail for callbacks from this bot", name)
					}
				}
			} else {
				hasSecret = cfg.BotSecret != ""
			}
			if !hasSecret {
				return fmt.Errorf("callbacks config: verify_jwt is enabled but no bot secret configured; set verify_jwt: false or configure bot secrets")
			}
		}
		var cbOpts []server.CallbackOption
		if verifyJWT {
			secretLookup, err := buildBotSecretLookup(cfg)
			if err != nil {
				return fmt.Errorf("callbacks secret lookup: %w", err)
			}
			cbOpts = append(cbOpts, server.WithCallbackSecretLookup(secretLookup))
		}
		srvOpts = append(srvOpts, server.WithCallbacks(*cb, cbOpts...))
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


// buildBotSecretLookup resolves bot secrets at startup and returns a lookup function.
// Secrets are cached to avoid repeated Vault/env lookups on every JWT verification.
func buildBotSecretLookup(cfg *config.Config) (func(botID string) (string, error), error) {
	cache := make(map[string]string)

	if cfg.IsMultiBot() {
		for _, bot := range cfg.Bots {
			if bot.ID == "" || bot.Secret == "" {
				continue
			}
			resolved, err := secret.Resolve(bot.Secret)
			if err != nil {
				return nil, fmt.Errorf("resolving secret for bot %s: %w", bot.ID, err)
			}
			cache[bot.ID] = resolved
		}
		return func(botID string) (string, error) {
			s, ok := cache[botID]
			if !ok {
				return "", fmt.Errorf("unknown bot_id %s", botID)
			}
			return s, nil
		}, nil
	}

	// Single-bot mode
	if cfg.BotSecret == "" {
		return func(botID string) (string, error) {
			return "", fmt.Errorf("bot %s has no secret configured", botID)
		}, nil
	}
	resolved, err := secret.Resolve(cfg.BotSecret)
	if err != nil {
		return nil, fmt.Errorf("resolving bot secret: %w", err)
	}
	return func(botID string) (string, error) {
		if cfg.BotID != botID {
			return "", fmt.Errorf("unknown bot_id %s", botID)
		}
		return resolved, nil
	}, nil
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

// runServeEnqueue starts the HTTP server in async/enqueue mode.
// Instead of sending directly to BotX API, requests are published to a work queue.
func runServeEnqueue(flags config.Flags, listenFlag, apiKeyFlag string, deps Deps) error {
	cfg, err := config.LoadForServeEnqueue(flags)
	if err != nil {
		return err
	}

	// Parse max file size
	maxFileSize, err := config.ParseFileSize(cfg.Queue.MaxFileSize)
	if err != nil {
		return fmt.Errorf("invalid queue.max_file_size %q: %w", cfg.Queue.MaxFileSize, err)
	}

	// Build server config
	srvCfg := server.Config{
		Listen:             cfg.Server.Listen,
		BasePath:           cfg.Server.BasePath,
		AsyncMode:          true,
		DefaultRoutingMode: cfg.Producer.RoutingMode,
		MaxFileSize:        maxFileSize,
	}
	if srvCfg.DefaultRoutingMode == "" {
		srvCfg.DefaultRoutingMode = string(config.RoutingMixed)
	}

	if v := os.Getenv("EXPRESS_BOTX_SERVER_LISTEN"); v != "" {
		srvCfg.Listen = v
	}
	if v := os.Getenv("EXPRESS_BOTX_SERVER_BASE_PATH"); v != "" {
		srvCfg.BasePath = v
	}
	if listenFlag != "" {
		srvCfg.Listen = listenFlag
	}
	if srvCfg.Listen == "" {
		srvCfg.Listen = ":8080"
	}
	if srvCfg.BasePath == "" {
		srvCfg.BasePath = "/api/v1"
	}

	srvCfg.ExternalURL = cfg.Server.ExternalURL
	if v := os.Getenv("EXPRESS_BOTX_SERVER_EXTERNAL_URL"); v != "" {
		srvCfg.ExternalURL = v
	}

	srvCfg.EnableDocs = true
	if cfg.Server.Docs != nil && !*cfg.Server.Docs {
		srvCfg.EnableDocs = false
	}
	srvCfg.AppVersion = Version

	// Resolve API keys
	keys, err := resolveAPIKeys(cfg.Server.APIKeys)
	if err != nil {
		return fmt.Errorf("resolving api keys: %w", err)
	}

	if apiKeyFlag != "" {
		resolved, err := secret.Resolve(apiKeyFlag)
		if err != nil {
			return fmt.Errorf("resolving --api-key: %w", err)
		}
		keys = append(keys, server.ResolvedKey{Name: "cli", Key: resolved})
	}

	if v := os.Getenv("EXPRESS_BOTX_SERVER_API_KEY"); v != "" {
		resolved, err := secret.Resolve(v)
		if err != nil {
			return fmt.Errorf("resolving EXPRESS_BOTX_SERVER_API_KEY: %w", err)
		}
		keys = append(keys, server.ResolvedKey{Name: "env", Key: resolved})
	}

	if len(keys) == 0 {
		key, err := generateAPIKey()
		if err != nil {
			return fmt.Errorf("generating api key: %w", err)
		}
		keys = append(keys, server.ResolvedKey{Name: "auto", Key: key})
		vlog.Info("serve: no API keys configured, generated key: %s", key)
	}
	srvCfg.Keys = keys

	// Create queue publisher
	pub, err := queue.NewPublisher(cfg.Queue.Driver, cfg.Queue.URL, cfg.Queue.Name)
	if err != nil {
		return fmt.Errorf("creating queue publisher: %w", err)
	}
	defer pub.Close()

	// Catalog cache for alias resolution in catalog/mixed mode
	var catalogCache *queue.CatalogCache
	var catalogConsumer queue.Consumer
	routingMode := cfg.Producer.RoutingMode
	if routingMode == "" {
		routingMode = string(config.RoutingMixed)
	}
	if cfg.Catalog.CacheFile != "" {
		var maxAge time.Duration
		if cfg.Catalog.MaxAge != "" {
			maxAge, err = time.ParseDuration(cfg.Catalog.MaxAge)
			if err != nil {
				return fmt.Errorf("invalid catalog.max_age %q: %w", cfg.Catalog.MaxAge, err)
			}
		}
		catalogCache = queue.NewCatalogCache(cfg.Catalog.CacheFile, maxAge)

		// Create catalog consumer to keep cache fresh from broker.
		// Pass empty work queue name — this consumer is only used for ConsumeCatalog.
		if cfg.Catalog.QueueName != "" {
			cons, err := queue.NewConsumer(cfg.Queue.Driver, cfg.Queue.URL, "", cfg.Queue.Group)
			if err != nil {
				vlog.Info("serve: could not create catalog consumer: %v (catalog will rely on disk cache)", err)
			} else {
				catalogConsumer = cons
				defer cons.Close()
			}
		}
	}

	// Send function: enqueue instead of sending directly
	sendFn := func(ctx context.Context, p *server.SendPayload) (string, error) {
		requestID := newRequestID()
		botID := p.BotID
		chatID := p.ChatID
		var routeHost, routeBotName, routeChatAlias, routeCatalogRevision string

		// Determine routing based on mode
		effectiveMode := p.RoutingMode
		if effectiveMode == "" {
			effectiveMode = routingMode
		}

		needsCatalog := false
		switch config.RoutingMode(effectiveMode) {
		case config.RoutingDirect:
			// Direct: require UUIDs
			if !config.IsUUID(botID) {
				return "", fmt.Errorf("bot_id must be a valid UUID for direct routing mode")
			}
			if !config.IsUUID(chatID) {
				return "", fmt.Errorf("chat_id must be a valid UUID for direct routing mode; use catalog or mixed mode for alias resolution")
			}
		case config.RoutingMixed:
			// Mixed: if both botID and chatID are provided and both are UUIDs,
			// treat as direct. Otherwise need catalog for alias resolution.
			if botID == "" || chatID == "" || !config.IsUUID(botID) || !config.IsUUID(chatID) {
				needsCatalog = true
			}
		case config.RoutingCatalog:
			needsCatalog = true
		default:
			return "", fmt.Errorf("invalid routing_mode %q: must be direct, catalog, or mixed", effectiveMode)
		}

		if needsCatalog {
			if catalogCache == nil {
				return "", fmt.Errorf("catalog routing requires catalog configuration (catalog.cache_file)")
			}
			snap := catalogCache.Get()
			if snap == nil {
				return "", fmt.Errorf("no valid catalog snapshot available; use direct routing or wait for catalog update")
			}
			routeCatalogRevision = snap.Revision

			// Resolve chat alias
			if chatID != "" && !config.IsUUID(chatID) {
				chat, err := snap.ResolveChat(chatID)
				if err != nil {
					return "", err
				}
				routeChatAlias = chatID
				chatID = chat.ID
				// Chat-bound bot
				if p.Bot == "" && botID == "" && chat.Bot != "" {
					p.Bot = chat.Bot
				}
			}

			// Resolve bot
			if botID == "" && p.Bot != "" {
				bot, err := snap.ResolveBot(p.Bot)
				if err != nil {
					return "", err
				}
				botID = bot.ID
				routeBotName = bot.Name
				routeHost = bot.Host
			} else if botID != "" && config.IsUUID(botID) {
				if bot, ok := snap.ResolveBotByID(botID); ok {
					routeBotName = bot.Name
					routeHost = bot.Host
				}
			} else if botID != "" {
				// bot_id is not a UUID — treat as alias
				bot, err := snap.ResolveBot(botID)
				if err != nil {
					return "", fmt.Errorf("bot_id %q is not a valid UUID and could not be resolved as alias: %w", botID, err)
				}
				botID = bot.ID
				routeBotName = bot.Name
				routeHost = bot.Host
			}
		}

		// After catalog resolution, bot_id and chat_id must be known for catalog/mixed modes.
		if needsCatalog && botID == "" {
			return "", fmt.Errorf("could not resolve bot: provide bot_id, bot alias, or use a chat with a catalog-bound bot")
		}
		if chatID == "" {
			return "", fmt.Errorf("chat_id is required")
		}

		msg := &queue.WorkMessage{
			RequestID: requestID,
			Routing: queue.Routing{
				Host:            routeHost,
				BotID:           botID,
				ChatID:          chatID,
				BotName:         routeBotName,
				ChatAlias:       routeChatAlias,
				CatalogRevision: routeCatalogRevision,
			},
			Payload: queue.Payload{
				Message: p.Message,
				Status:  p.Status,
			},
			ReplyTo:    cfg.Queue.ReplyQueue,
			EnqueuedAt: time.Now().UTC(),
		}

		if p.Opts != nil {
			msg.Payload.Opts = queue.DeliveryOpts{
				Silent:   p.Opts.Silent,
				Stealth:  p.Opts.Stealth,
				ForceDND: p.Opts.ForceDND,
				NoNotify: p.Opts.NoNotify,
			}
		}

		if p.Metadata != nil {
			msg.Payload.Metadata = p.Metadata
		}

		if p.File != nil {
			normalized := botapi.BuildFileAttachmentFromBase64(p.File.Name, p.File.Data)
			msg.Payload.File = &queue.FileAttachment{
				FileName: normalized.FileName,
				Data:     normalized.Data,
			}
		}

		if err := pub.PublishWork(ctx, msg); err != nil {
			return "", fmt.Errorf("publishing to queue: %w", err)
		}
		return requestID, nil
	}

	// Chat resolver: in async mode, pass through chat_id as-is
	// (alias resolution happens in sendFn for catalog/mixed modes)
	chatResolver := func(chatID string) (server.ChatResolveResult, error) {
		return server.ChatResolveResult{ChatID: chatID}, nil
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

	srv := server.New(srvCfg, sendFn, chatResolver, srvOpts...)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start background catalog consumer after ctx is available
	if catalogConsumer != nil && catalogCache != nil {
		go func() {
			err := catalogConsumer.ConsumeCatalog(ctx, cfg.Catalog.QueueName, func(_ context.Context, snap *queue.CatalogSnapshot) error {
				catalogCache.Update(snap)
				vlog.Info("serve: catalog updated (revision=%s, bots=%d, chats=%d)",
					snap.Revision, len(snap.Bots), len(snap.Chats))
				return nil
			})
			if err != nil && ctx.Err() == nil {
				vlog.Info("serve: catalog consumer stopped: %v", err)
			}
		}()
	}

	vlog.Info("serve: async mode (enqueue), driver=%s", cfg.Queue.Driver)
	return srv.Run(ctx)
}
