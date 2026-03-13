package server

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
)

type ctxKey int

const keyNameKey ctxKey = iota

// KeyName returns the API key name from the request context.
func KeyName(ctx context.Context) string {
	if v, ok := ctx.Value(keyNameKey).(string); ok {
		return v
	}
	return ""
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Try API key (Bearer or X-API-Key)
		if key := extractKey(r); key != "" {
			if name, ok := s.keyMap[key]; ok {
				log.Printf("%s %s [key: %s]", r.Method, r.URL.Path, name)
				ctx := context.WithValue(r.Context(), keyNameKey, name)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// 2. Try bot signature (if enabled)
		if s.cfg.AllowBotSecretAuth && s.cfg.BotSignature != "" {
			if sig := r.Header.Get("X-Bot-Signature"); sig != "" {
				if subtle.ConstantTimeCompare([]byte(sig), []byte(s.cfg.BotSignature)) == 1 {
					log.Printf("%s %s [key: bot_secret]", r.Method, r.URL.Path)
					ctx := context.WithValue(r.Context(), keyNameKey, "bot_secret")
					next.ServeHTTP(w, r.WithContext(ctx))
					return
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
