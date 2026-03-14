package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	vlog "github.com/lavr/express-botx/internal/log"
)

type ctxKey int

const (
	keyNameKey ctxKey = iota
	authBotKey        // bot name bound by X-Bot-Signature auth
)

// KeyName returns the API key name from the request context.
func KeyName(ctx context.Context) string {
	if v, ok := ctx.Value(keyNameKey).(string); ok {
		return v
	}
	return ""
}

// AuthBot returns the bot name bound by X-Bot-Signature authentication.
// Empty string means no bot was bound (API-key auth or single-bot mode).
func AuthBot(ctx context.Context) string {
	if v, ok := ctx.Value(authBotKey).(string); ok {
		return v
	}
	return ""
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Try API key (Bearer or X-API-Key)
		if key := extractKey(r); key != "" {
			if name, ok := s.keyMap[key]; ok {
				vlog.V1("server: %s %s [key: %s]", r.Method, r.URL.Path, name)
				ctx := context.WithValue(r.Context(), keyNameKey, name)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// 2. Try bot signature (if enabled)
		if s.cfg.AllowBotSecretAuth && len(s.cfg.BotSignatures) > 0 {
			if sig := r.Header.Get("X-Bot-Signature"); sig != "" {
				for expected, botName := range s.cfg.BotSignatures {
					if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) == 1 {
						vlog.V1("server: %s %s [key: bot_secret, bot: %s]", r.Method, r.URL.Path, botName)
						ctx := context.WithValue(r.Context(), keyNameKey, "bot_secret")
						if botName != "" {
							ctx = context.WithValue(ctx, authBotKey, botName)
						}
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
		}

		// No valid credentials found
		if extractKey(r) == "" && r.Header.Get("X-Bot-Signature") == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized")
		} else {
			writeError(w, http.StatusForbidden, "forbidden")
		}
	})
}

func extractKey(r *http.Request) string {
	// Try Authorization: Bearer <key>
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}
	// Try X-API-Key header
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	return ""
}
