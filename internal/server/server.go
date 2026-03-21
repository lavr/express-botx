package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lavr/express-botx/internal/apm"
	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/errtrack"
	vlog "github.com/lavr/express-botx/internal/log"
)

// ResolvedKey is an API key with its secret resolved.
type ResolvedKey struct {
	Name string
	Key  string
}

// Config holds the server runtime configuration.
type Config struct {
	Listen             string
	BasePath           string
	Keys               []ResolvedKey
	AllowBotSecretAuth bool
	BotSignatures      map[string]string // signature -> bot name (multi-bot) or "" (single-bot)
	BotNames           []string          // available bot names; if len > 1, bot is required in requests
	SingleBotName      string            // name of the single bot (when not multi-bot); used to reject mismatched chat bindings
	DefaultChatAlias   string            // alias of the chat marked as default; used when chat_id is omitted
	EnableDocs         bool              // serve /docs (Swagger UI) and /docs/openapi.yaml
	ExternalURL        string            // public URL for OpenAPI docs server variable
	AppVersion         string            // application version (from -ldflags), replaces version in OpenAPI spec
	AsyncMode          bool              // when true, /send enqueues instead of sending directly
	DefaultRoutingMode string            // default routing mode for async: direct, catalog, mixed
	MaxFileSize        int64             // max file size in bytes for async mode (0 = default 1MB)
}

// Server is the HTTP server for express-botx.
type Server struct {
	cfg        Config
	send       SendFunc
	chats      ChatResolver
	keyMap     map[string]string // key -> name
	botNameSet map[string]bool   // valid bot names for multi-bot mode
	apm        apm.Provider
	errTracker errtrack.Tracker
	botEntries  []config.BotEntry  // for GET /bot/list
	chatEntries []config.ChatEntry // for GET /chats/alias/list
	amCfg          *AlertmanagerConfig
	grCfg          *GrafanaConfig
	callbackRouter       *CallbackRouter
	callbacksCfg         *config.CallbacksConfig
	callbackSecretLookup func(botID string) (string, error)
	callbackWG           sync.WaitGroup    // tracks in-flight async callback handlers
	callbackCtx          context.Context    // cancelled on shutdown to signal async handlers
	callbackCancel       context.CancelFunc // cancels callbackCtx
	srv                  *http.Server
}

// SendFunc sends a message via the BotX API. The server calls this for each request.
type SendFunc func(ctx context.Context, req *SendPayload) (syncID string, err error)

// ChatResolveResult holds the resolved chat UUID and optional bound bot name.
type ChatResolveResult struct {
	ChatID string
	Bot    string // from chat config, may be empty
}

// ChatResolver resolves a chat alias to a UUID and optional bound bot.
type ChatResolver func(chatID string) (ChatResolveResult, error)

// Option configures optional server features.
type Option func(*Server)

// WithAlertmanager enables the alertmanager webhook endpoint.
func WithAlertmanager(cfg *AlertmanagerConfig) Option {
	return func(s *Server) {
		s.amCfg = cfg
	}
}

// WithGrafana enables the Grafana webhook endpoint.
func WithGrafana(cfg *GrafanaConfig) Option {
	return func(s *Server) {
		s.grCfg = cfg
	}
}

// WithAPM sets the APM provider for request tracing.
func WithAPM(p apm.Provider) Option {
	return func(s *Server) {
		s.apm = p
	}
}

// WithErrTracker sets the error tracker for panic/error capture.
func WithErrTracker(t errtrack.Tracker) Option {
	return func(s *Server) {
		s.errTracker = t
	}
}

// CallbackOption configures callback handling.
type CallbackOption func(*callbackOptions)

type callbackOptions struct {
	customHandlers map[string]CallbackHandler
	secretLookup   func(botID string) (string, error)
}

// WithCallbackHandler registers a custom CallbackHandler that can be referenced
// by its Type() name in callback rules. Custom handlers take precedence over
// the built-in exec and webhook handlers.
func WithCallbackHandler(handler CallbackHandler) CallbackOption {
	return func(o *callbackOptions) {
		if o.customHandlers == nil {
			o.customHandlers = make(map[string]CallbackHandler)
		}
		o.customHandlers[handler.Type()] = handler
	}
}

// WithCallbackSecretLookup sets the function used to look up bot secrets for JWT
// verification in callback endpoints. Required when verify_jwt is enabled.
func WithCallbackSecretLookup(fn func(botID string) (string, error)) CallbackOption {
	return func(o *callbackOptions) {
		o.secretLookup = fn
	}
}

// WithCallbacks enables callback endpoints (POST /command and POST /notification/callback).
// It creates a CallbackRouter from the config rules and registers handlers.
func WithCallbacks(cfg config.CallbacksConfig, opts ...CallbackOption) Option {
	return func(s *Server) {
		co := &callbackOptions{}
		for _, o := range opts {
			o(co)
		}

		handlers, err := buildHandlers(cfg.Rules, co.customHandlers)
		if err != nil {
			vlog.Info("server: failed to build callback handlers: %v", err)
			return
		}

		events := make([][]string, len(cfg.Rules))
		asyncFlags := make([]bool, len(cfg.Rules))
		for i, rule := range cfg.Rules {
			events[i] = rule.Events
			asyncFlags[i] = rule.Async
		}

		router, err := NewCallbackRouter(events, asyncFlags, handlers)
		if err != nil {
			vlog.Info("server: failed to create callback router: %v", err)
			return
		}

		s.callbackRouter = router
		s.callbacksCfg = &cfg
		s.callbackSecretLookup = co.secretLookup
	}
}

// New creates a Server with the given configuration.
func New(cfg Config, sendFn SendFunc, chatResolver ChatResolver, opts ...Option) *Server {
	cbCtx, cbCancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:            cfg,
		send:           sendFn,
		chats:          chatResolver,
		keyMap:         make(map[string]string, len(cfg.Keys)),
		botNameSet:     make(map[string]bool, len(cfg.BotNames)),
		callbackCtx:    cbCtx,
		callbackCancel: cbCancel,
	}
	for _, k := range cfg.Keys {
		s.keyMap[k.Key] = k.Name
	}
	for _, name := range cfg.BotNames {
		s.botNameSet[name] = true
	}
	for _, o := range opts {
		o(s)
	}
	if s.apm == nil {
		s.apm = apm.New()
	}
	if s.errTracker == nil {
		s.errTracker = errtrack.New()
	}

	mux := http.NewServeMux()
	base := strings.TrimRight(cfg.BasePath, "/")

	// route registers an authenticated API endpoint with APM tracing.
	route := func(method, path string, h http.HandlerFunc) {
		pattern := fmt.Sprintf("%s %s%s", method, base, path)
		mux.Handle(pattern, s.apm.WrapHandler(method+" "+path, s.authMiddleware(h)))
	}

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	if cfg.EnableDocs {
		mux.Handle("/docs/", http.StripPrefix("/docs", docsHandler(cfg.ExternalURL, cfg.AppVersion)))
		mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/docs/", http.StatusMovedPermanently)
		})
		vlog.Info("server: docs endpoint enabled at /docs/")
	}

	route("POST", "/send", s.handleSend)
	route("GET", "/bot/list", s.handleBotList)
	route("GET", "/chats/alias/list", s.handleChatsAliasList)

	if s.amCfg != nil {
		route("POST", "/alertmanager", s.handleAlertmanager)
		chatInfo := "from ?chat_id param"
		if s.amCfg.DefaultChatID != "" {
			chatInfo = s.amCfg.DefaultChatID
		} else if cfg.DefaultChatAlias != "" {
			chatInfo = cfg.DefaultChatAlias
		} else if s.amCfg.FallbackChatID != "" {
			chatInfo = s.amCfg.FallbackChatID
		}
		vlog.Info("server: alertmanager endpoint enabled (chat: %s)", chatInfo)
	}

	if s.grCfg != nil {
		route("POST", "/grafana", s.handleGrafana)
		chatInfo := "from ?chat_id param"
		if s.grCfg.DefaultChatID != "" {
			chatInfo = s.grCfg.DefaultChatID
		} else if cfg.DefaultChatAlias != "" {
			chatInfo = cfg.DefaultChatAlias
		} else if s.grCfg.FallbackChatID != "" {
			chatInfo = s.grCfg.FallbackChatID
		}
		vlog.Info("server: grafana endpoint enabled (chat: %s)", chatInfo)
	}

	if s.callbackRouter != nil && s.callbacksCfg != nil {
		cbBase := strings.TrimRight(s.callbacksCfg.BasePath, "/")
		if cbBase == "" {
			cbBase = base
		}

		verifyJWT := true
		if s.callbacksCfg.VerifyJWT != nil {
			verifyJWT = *s.callbacksCfg.VerifyJWT
		}

		cbCommand := http.Handler(http.HandlerFunc(s.handleCommand))
		cbNotification := http.Handler(http.HandlerFunc(s.handleNotificationCallback))

		cbCommand = callbackJWTMiddleware(cbCommand, s.callbackSecretLookup, verifyJWT)
		cbNotification = callbackJWTMiddleware(cbNotification, s.callbackSecretLookup, verifyJWT)

		mux.Handle(fmt.Sprintf("POST %s/command", cbBase), s.apm.WrapHandler("POST /command", cbCommand))
		mux.Handle(fmt.Sprintf("POST %s/notification/callback", cbBase), s.apm.WrapHandler("POST /notification/callback", cbNotification))

		vlog.Info("server: callback endpoints enabled (base_path: %s, verify_jwt: %v, rules: %d)", cbBase, verifyJWT, len(s.callbacksCfg.Rules))
	}

	var handler http.Handler = mux
	handler = s.errTracker.Middleware(handler)

	s.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.srv.SetKeepAlivesEnabled(false)

	return s
}

// isMultiBot returns true if the server is configured with multiple bots.
func (s *Server) isMultiBot() bool {
	return len(s.cfg.BotNames) > 1
}

// resolveRequestBot validates the bot name in multi-bot mode.
// Priority: explicit request bot > chat-bound bot > auth-bound bot > single bot > error.
// Returns the resolved bot name and an error message (empty on success).
func (s *Server) resolveRequestBot(ctx context.Context, requestBot, chatBot string) (string, string) {
	if !s.isMultiBot() {
		// Single-bot mode: only one sender exists.
		// If a specific bot was requested or bound to the chat, validate it.
		wanted := requestBot
		if wanted == "" {
			wanted = chatBot
		}
		if wanted != "" {
			if s.cfg.SingleBotName == "" {
				// Server started via env/flags without a named bot —
				// cannot serve named bot requests.
				return "", fmt.Sprintf("bot %q is not available (server started without named bot config)", wanted)
			}
			if wanted != s.cfg.SingleBotName {
				return "", fmt.Sprintf("bot %q is not available, server is running as %q", wanted, s.cfg.SingleBotName)
			}
		}
		return "", ""
	}

	// If authenticated via bot-secret, bind to that bot
	if authBot := AuthBot(ctx); authBot != "" {
		bot := requestBot
		if bot == "" {
			bot = chatBot
		}
		if bot != "" && bot != authBot {
			return "", fmt.Sprintf("bot %q does not match authenticated bot %q", bot, authBot)
		}
		return authBot, ""
	}

	// Explicit request bot takes priority
	bot := requestBot
	if bot == "" {
		bot = chatBot
	}
	if bot == "" {
		return "", fmt.Sprintf("bot is required, available: %s", strings.Join(s.cfg.BotNames, ", "))
	}
	if !s.botNameSet[bot] {
		return "", fmt.Sprintf("unknown bot %q, available: %s", bot, strings.Join(s.cfg.BotNames, ", "))
	}
	return bot, ""
}

// Run starts the server and blocks until ctx is cancelled. It performs graceful shutdown.
func (s *Server) Run(ctx context.Context) error {
	defer s.callbackCancel()

	errCh := make(chan error, 1)
	go func() {
		vlog.Info("server: listening on %s (base_path: %s)", s.cfg.Listen, s.cfg.BasePath)
		if len(s.keyMap) > 0 {
			vlog.Info("server: %d API keys loaded", len(s.keyMap))
		}
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	vlog.Info("server: shutting down...")

	// Stop accepting new connections first.
	var shutdownErr error
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		vlog.V1("server: HTTP shutdown error: %v", err)
		shutdownErr = err
	}

	// Signal async callback handlers to stop.
	s.callbackCancel()

	// Wait for in-flight async callback handlers to finish (bounded by shutdownCtx).
	done := make(chan struct{})
	go func() {
		s.callbackWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		vlog.V2("server: all async callback handlers finished")
	case <-shutdownCtx.Done():
		vlog.V1("server: timeout waiting for async callback handlers")
		if shutdownErr == nil {
			shutdownErr = fmt.Errorf("server: timeout waiting for async callback handlers")
		}
	}

	return shutdownErr
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}` + "\n"))
}
