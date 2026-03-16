package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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
	amCfg       *AlertmanagerConfig
	grCfg       *GrafanaConfig
	srv         *http.Server
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

// New creates a Server with the given configuration.
func New(cfg Config, sendFn SendFunc, chatResolver ChatResolver, opts ...Option) *Server {
	s := &Server{
		cfg:        cfg,
		send:       sendFn,
		chats:      chatResolver,
		keyMap:     make(map[string]string, len(cfg.Keys)),
		botNameSet: make(map[string]bool, len(cfg.BotNames)),
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
		chatInfo := s.amCfg.DefaultChatID
		if chatInfo == "" {
			chatInfo = cfg.DefaultChatAlias
		}
		if chatInfo == "" {
			chatInfo = s.amCfg.FallbackChatID
		}
		if chatInfo == "" {
			chatInfo = "from ?chat_id param"
		}
		vlog.Info("server: alertmanager endpoint enabled (chat: %s)", chatInfo)
	}

	if s.grCfg != nil {
		route("POST", "/grafana", s.handleGrafana)
		chatInfo := s.grCfg.DefaultChatID
		if chatInfo == "" {
			chatInfo = cfg.DefaultChatAlias
		}
		if chatInfo == "" {
			chatInfo = s.grCfg.FallbackChatID
		}
		if chatInfo == "" {
			chatInfo = "from ?chat_id param"
		}
		vlog.Info("server: grafana endpoint enabled (chat: %s)", chatInfo)
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
	return s.srv.Shutdown(shutdownCtx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}` + "\n"))
}
