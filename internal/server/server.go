package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lavr/express-botx/internal/apm"
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
}

// Server is the HTTP server for express-botx.
type Server struct {
	cfg        Config
	send       SendFunc
	chats      ChatResolver
	keyMap     map[string]string // key -> name
	botNameSet map[string]bool   // valid bot names for multi-bot mode
	apm        apm.Provider
	amCfg      *AlertmanagerConfig
	grCfg      *GrafanaConfig
	srv        *http.Server
}

// SendFunc sends a message via the BotX API. The server calls this for each request.
type SendFunc func(ctx context.Context, req *SendPayload) (syncID string, err error)

// ChatResolver resolves a chat alias to a UUID. Returns the input unchanged if it is already a UUID.
type ChatResolver func(chatID string) (string, error)

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

	mux := http.NewServeMux()
	base := strings.TrimRight(cfg.BasePath, "/")

	// route registers an authenticated API endpoint with APM tracing.
	route := func(method, path string, h http.HandlerFunc) {
		pattern := fmt.Sprintf("%s %s%s", method, base, path)
		mux.Handle(pattern, s.apm.WrapHandler(method+" "+path, s.authMiddleware(h)))
	}

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	route("POST", "/send", s.handleSend)

	if s.amCfg != nil {
		route("POST", "/alertmanager", s.handleAlertmanager)
		chatInfo := s.amCfg.DefaultChatID
		if chatInfo == "" {
			chatInfo = s.amCfg.FallbackChatID
		}
		if chatInfo == "" {
			chatInfo = "from ?chat_id param"
		}
		vlog.V1("server: alertmanager endpoint enabled (chat: %s)", chatInfo)
	}

	if s.grCfg != nil {
		route("POST", "/grafana", s.handleGrafana)
		chatInfo := s.grCfg.DefaultChatID
		if chatInfo == "" {
			chatInfo = s.grCfg.FallbackChatID
		}
		if chatInfo == "" {
			chatInfo = "from ?chat_id param"
		}
		vlog.V1("server: grafana endpoint enabled (chat: %s)", chatInfo)
	}

	s.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
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
// In bot-secret auth, the request is bound to the authenticated bot.
// Returns the resolved bot name and an error message (empty on success).
func (s *Server) resolveRequestBot(ctx context.Context, bot string) (string, string) {
	if !s.isMultiBot() {
		return bot, ""
	}

	// If authenticated via bot-secret, bind to that bot
	if authBot := AuthBot(ctx); authBot != "" {
		if bot != "" && bot != authBot {
			return "", fmt.Sprintf("bot %q does not match authenticated bot %q", bot, authBot)
		}
		return authBot, ""
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
		vlog.V1("server: listening on %s (base_path: %s)", s.cfg.Listen, s.cfg.BasePath)
		if len(s.keyMap) > 0 {
			vlog.V1("server: %d API keys loaded", len(s.keyMap))
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
	vlog.V1("server: shutting down...")
	return s.srv.Shutdown(shutdownCtx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}` + "\n"))
}
