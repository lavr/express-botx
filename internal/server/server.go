package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	vlog "github.com/lavr/express-bot/internal/log"
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
	BotSignature       string // expected HMAC-SHA256 signature for bot secret auth
}

// Server is the HTTP server for express-bot.
type Server struct {
	cfg    Config
	send   SendFunc
	chats  ChatResolver
	keyMap map[string]string // key -> name
	srv    *http.Server
}

// SendFunc sends a message via the BotX API. The server calls this for each request.
type SendFunc func(ctx context.Context, req *SendPayload) (syncID string, err error)

// ChatResolver resolves a chat alias to a UUID. Returns the input unchanged if it is already a UUID.
type ChatResolver func(chatID string) (string, error)

// New creates a Server with the given configuration.
func New(cfg Config, sendFn SendFunc, chatResolver ChatResolver) *Server {
	s := &Server{
		cfg:    cfg,
		send:   sendFn,
		chats:  chatResolver,
		keyMap: make(map[string]string, len(cfg.Keys)),
	}
	for _, k := range cfg.Keys {
		s.keyMap[k.Key] = k.Name
	}

	mux := http.NewServeMux()

	// healthz is always at the root, no auth
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// API routes under base path, with auth
	base := strings.TrimRight(cfg.BasePath, "/")
	mux.Handle(fmt.Sprintf("POST %s/send", base), s.authMiddleware(http.HandlerFunc(s.handleSend)))

	s.srv = &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.srv.SetKeepAlivesEnabled(false)

	return s
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
