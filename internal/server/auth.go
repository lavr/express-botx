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
	keyNameKey     ctxKey = iota
	authBotKey            // bot name bound by X-Bot-Signature auth
	keyScopeKey           // chat scope of the API key that authenticated the request
	queryKeyCtxKey        // API key lifted out of the query string before logging
	keyDefaultChatKey
)

func withQueryKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, queryKeyCtxKey, key)
}

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

// chatAllowed reports whether the API key that authenticated this request may
// address the given chat. resolvedUUID must be the final delivery target —
// after alias resolution and after any default or fallback substitution.
// Checking the value as it arrived in the request would let a bare UUID walk
// straight past the scope, which is the hole this exists to close.
//
// A key with no configured scope is unrestricted, as is a request authenticated
// by bot signature rather than by an API key.
func (s *Server) chatAllowed(ctx context.Context, resolvedUUID string) bool {
	return ChatAllowed(ctx, resolvedUUID)
}

// scopedKey reports whether this request's API key carries a chat scope. Used
// to withhold catalog details (chat lists, alias suggestions) from callers that
// are not allowed to see the whole installation.
func (s *Server) scopedKey(ctx context.Context) bool {
	return Scoped(ctx)
}

// Scoped reports whether the API key behind this request is restricted to a set
// of chats. Exported for send pipelines that resolve the final address
// themselves and must apply the scope at that point.
func Scoped(ctx context.Context) bool {
	return len(keyScope(ctx)) > 0
}

// ChatAllowed reports whether the API key behind this request may address the
// given chat UUID.
//
// Async delivery resolves aliases against the bot catalog, which can disagree
// with the local configuration: the same alias may name a different chat there.
// A check made against the local list would therefore authorize one chat while
// the message went to another, so the send pipeline must call this with the
// UUID it is actually about to deliver to, immediately before delivering.
func ChatAllowed(ctx context.Context, resolvedUUID string) bool {
	scope := keyScope(ctx)
	if len(scope) == 0 {
		return true
	}
	target := strings.ToLower(resolvedUUID)
	for _, allowed := range scope {
		if allowed == target {
			return true
		}
	}
	return false
}

// keyScope returns the chat scope of the API key that authenticated this
// request. The scope travels with the request rather than being looked up by
// key name: names are not guaranteed unique, and a name-keyed lookup would
// hand one key the permissions of another that happens to share its name.
func keyScope(ctx context.Context) []string {
	if v, ok := ctx.Value(keyScopeKey).([]string); ok {
		return v
	}
	return nil
}

// authorizeTargets applies the key's chat scope to every chat a fan-out request
// names, before any of them is delivered to. Checking inside the fan-out would
// not be enough: delivery is best-effort and per target, so an unauthorized
// chat listed alongside an authorized one would be refused while the rest of
// the message still went out. A request naming any chat outside the scope is
// refused whole, and nothing is sent.
//
// Only the sync path can resolve here: in async mode the chat resolver is a
// pass-through and the real address comes from the bot catalog later, so a
// non-UUID target is left to the send pipeline (see ChatAllowed). Returns false
// when it has already written the response.
func (s *Server) authorizeTargets(w http.ResponseWriter, r *http.Request, targets []string, resolve bool) bool {
	if !s.scopedKey(r.Context()) {
		return true
	}
	for _, target := range targets {
		if !resolve {
			if isUUID(target) && !ChatAllowed(r.Context(), target) {
				writeError(w, http.StatusForbidden, ErrChatNotAllowed.Error())
				return false
			}
			continue
		}
		chat, err := s.chats(target)
		if err != nil {
			s.denyChat(w, r, err, "")
			return false
		}
		if !ChatAllowed(r.Context(), chat.ChatID) {
			writeError(w, http.StatusForbidden, ErrChatNotAllowed.Error())
			return false
		}
	}
	return true
}

// sanitizeErrors rewrites per-chat failures for a scoped caller. Fan-out is
// best-effort and reports each target's error verbatim: those strings quote the
// chat catalog and distinguish "no such chat" from "not your chat", which is
// exactly the pair a scoped key must not be able to tell apart.
func (s *Server) sanitizeErrors(ctx context.Context, errs []SendError) []SendError {
	if !Scoped(ctx) || len(errs) == 0 {
		return errs
	}
	out := make([]SendError, len(errs))
	for i, e := range errs {
		out[i] = SendError{Chat: e.Chat, Error: ErrChatNotAllowed.Error()}
	}
	return out
}

// denyChat writes the refusal a scoped caller gets for any chat it may not use.
// Unknown and forbidden are answered identically on purpose: distinguishable
// responses turn the endpoint into an oracle, and a scoped key could enumerate
// other teams' alias names by guessing and watching the status code. Callers
// without a scope keep the diagnostic distinction, since nothing is hidden from
// them anyway.
func (s *Server) denyChat(w http.ResponseWriter, r *http.Request, err error, stage string) {
	if s.scopedKey(r.Context()) {
		writeError(w, http.StatusForbidden, "chat not allowed for this key")
		return
	}
	msg := err.Error()
	if stage != "" {
		msg = stage + ": " + msg
	}
	writeError(w, http.StatusBadRequest, msg)
}

// deliveryError renders a delivery failure for the client. Errors raised while
// resolving and sending quote the chat catalog — the async path resolves
// against the bot catalog, whose "available" list names chats belonging to
// other teams — so a scoped key is told only that the step failed. The full
// error is logged either way.
func (s *Server) deliveryError(ctx context.Context, stage string, err error) string {
	if s.scopedKey(ctx) {
		return stage
	}
	return stage + ": " + err.Error()
}

func keyDefaultChat(ctx context.Context) string {
	if v, ok := ctx.Value(keyDefaultChatKey).(string); ok {
		return v
	}
	return ""
}

func (s *Server) defaultSendChat(ctx context.Context) string {
	own := keyDefaultChat(ctx)
	if own == "" {
		return s.cfg.DefaultChatAlias
	}
	if s.cfg.DefaultChatAlias == "" {
		return own
	}
	if s.cfg.AsyncMode {
		return s.cfg.DefaultChatAlias
	}
	if chat, err := s.chats(s.cfg.DefaultChatAlias); err == nil && ChatAllowed(ctx, chat.ChatID) {
		return s.cfg.DefaultChatAlias
	}
	return own
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Try API key (Bearer or X-API-Key)
		if key, viaQuery := s.requestKey(r); key != "" {
			if rk, ok := s.keyMap[key]; ok && (!viaQuery || rk.AllowQueryAuth) {
				vlog.V1("server: %s %s [key: %s]", r.Method, r.URL.Path, rk.Name)
				ctx := context.WithValue(r.Context(), keyNameKey, rk.Name)
				if len(rk.Chats) > 0 {
					ctx = context.WithValue(ctx, keyScopeKey, rk.Chats)
				}
				if rk.DefaultChat != "" {
					ctx = context.WithValue(ctx, keyDefaultChatKey, rk.DefaultChat)
				}
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

func (s *Server) requestKey(r *http.Request) (string, bool) {
	if key := extractKey(r); key != "" {
		return key, false
	}
	if hasAuthHeader(r) {
		return "", false
	}
	return queryKey(r), true
}

func hasAuthHeader(r *http.Request) bool {
	for _, name := range []string{"Authorization", "X-API-Key"} {
		if _, ok := r.Header[http.CanonicalHeaderKey(name)]; ok {
			return true
		}
	}
	return false
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
