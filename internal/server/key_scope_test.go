package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/config"
)

const (
	ownUUID   = "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"
	otherUUID = "7ee8aaa9-c6cb-5ee6-8445-7d654819b285"
)

// newScopeServer builds a server whose resolver maps two aliases onto two
// chats, so a scoped key can be aimed at its own chat and at a foreign one by
// either name form.
func newScopeServer(keys []ResolvedKey, srvOpts ...Option) *Server {
	aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: keys}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) { return "sync-id", nil }
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if id, ok := aliases[chatID]; ok {
			return ChatResolveResult{ChatID: id}, nil
		}
		if chatID == ownUUID || chatID == otherUUID {
			return ChatResolveResult{ChatID: chatID}, nil
		}
		return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q, available: other-chat, own-chat", chatID)
	}
	return New(cfg, sendFn, chatResolver, srvOpts...)
}

func sendAs(srv *Server, key, chatID string) *httptest.ResponseRecorder {
	body := strings.NewReader(fmt.Sprintf(`{"chat_id":%q,"message":"hi"}`, chatID))
	return doRequest(srv, "POST", "/api/v1/send", body, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + key,
	})
}

func TestKeyScope_Send(t *testing.T) {
	unscoped := ResolvedKey{Name: "any-app", Key: "open"}
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}

	tests := []struct {
		name   string
		key    string
		chatID string
		want   int
	}{
		{"unscoped key, own alias", "open", "own-chat", 200},
		{"unscoped key, foreign alias", "open", "other-chat", 200},
		{"unscoped key, foreign uuid", "open", otherUUID, 200},
		{"scoped key, own alias", "narrow", "own-chat", 200},
		{"scoped key, own uuid", "narrow", ownUUID, 200},
		{"scoped key, foreign alias", "narrow", "other-chat", 403},
		{"scoped key, foreign uuid", "narrow", otherUUID, 403},
	}

	srv := newScopeServer([]ResolvedKey{unscoped, scoped})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := sendAs(srv, tc.key, tc.chatID)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// The scope is stored and compared as UUIDs even when configured as an alias;
// a scope kept as an alias would not match a request using the bare UUID.
func TestKeyScope_UUIDCaseInsensitive(t *testing.T) {
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{strings.ToUpper(ownUUID)}}
	srv := newScopeServer([]ResolvedKey{scoped})
	if w := sendAs(srv, "narrow", ownUUID); w.Code != 200 {
		t.Errorf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
}

func TestKeyScope_NoAliasLeakInError(t *testing.T) {
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}
	unscoped := ResolvedKey{Name: "any-app", Key: "open"}
	srv := newScopeServer([]ResolvedKey{scoped, unscoped})

	w := sendAs(srv, "narrow", "no-such-alias")
	if strings.Contains(w.Body.String(), "own-chat") {
		t.Errorf("scoped key was told the alias catalog: %s", w.Body.String())
	}

	w = sendAs(srv, "open", "no-such-alias")
	if !strings.Contains(w.Body.String(), "own-chat") {
		t.Errorf("unscoped key lost the helpful alias list: %s", w.Body.String())
	}
}

// The async path resolves against the bot catalog, whose errors quote every
// alias it knows — chats of other teams included. A scoped key must not be
// handed that list through a delivery failure.
func TestKeyScope_NoCatalogLeakInAsyncError(t *testing.T) {
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}
	unscoped := ResolvedKey{Name: "any-app", Key: "open"}

	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "", fmt.Errorf("resolving chat %q: not found, available in catalog: own-chat, private-other-team", p.ChatID)
	}
	passthrough := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{scoped, unscoped}, AsyncMode: true, DefaultRoutingMode: "catalog"}
	srv := New(cfg, sendFn, passthrough)

	w := sendAs(srv, "narrow", "no-such-alias")
	if strings.Contains(w.Body.String(), "private-other-team") {
		t.Errorf("scoped key was handed the catalog: %s", w.Body.String())
	}

	w = sendAs(srv, "open", "no-such-alias")
	if !strings.Contains(w.Body.String(), "private-other-team") {
		t.Errorf("unscoped key lost the diagnostic detail: %s", w.Body.String())
	}
}

// Distinguishable answers for "no such chat" and "not your chat" let a scoped
// key enumerate other teams' alias names by guessing and reading the status.
func TestKeyScope_UnknownAndForbiddenAreIndistinguishable(t *testing.T) {
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}
	unscoped := ResolvedKey{Name: "any-app", Key: "open"}
	srv := newScopeServer([]ResolvedKey{scoped, unscoped})

	foreign := sendAs(srv, "narrow", "other-chat")
	unknown := sendAs(srv, "narrow", "no-such-alias")
	if foreign.Code != unknown.Code || foreign.Body.String() != unknown.Body.String() {
		t.Errorf("scoped key can tell the two apart:\n  foreign: %d %s\n  unknown: %d %s",
			foreign.Code, foreign.Body.String(), unknown.Code, unknown.Body.String())
	}
	if foreign.Code != 403 {
		t.Errorf("status = %d, want 403", foreign.Code)
	}

	// An unscoped key keeps the diagnostic difference.
	if a, b := sendAs(srv, "open", "no-such-alias"), sendAs(srv, "open", "other-chat"); a.Code == b.Code && a.Body.String() == b.Body.String() {
		t.Error("unscoped key lost the distinction between unknown and allowed")
	}
}

// The same must hold on the async path, where an unresolvable alias surfaces as
// a delivery failure rather than a resolution error.
func TestKeyScope_AsyncUnknownAndForbiddenMatch(t *testing.T) {
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}
	aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		id, ok := aliases[p.ChatID]
		if !ok {
			return "", fmt.Errorf("%w: not found, available in catalog: own-chat, private-other-team", ErrChatUnresolved)
		}
		if !ChatAllowed(ctx, id) {
			return "", ErrChatNotAllowed
		}
		return "req-id", nil
	}
	passthrough := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{scoped}, AsyncMode: true, DefaultRoutingMode: "catalog"}
	srv := New(cfg, sendFn, passthrough)

	foreign := sendAs(srv, "narrow", "other-chat")
	unknown := sendAs(srv, "narrow", "no-such-alias")
	if foreign.Code != unknown.Code || foreign.Body.String() != unknown.Body.String() {
		t.Errorf("scoped key can tell the two apart:\n  foreign: %d %s\n  unknown: %d %s",
			foreign.Code, foreign.Body.String(), unknown.Code, unknown.Body.String())
	}
	if foreign.Code != 403 {
		t.Errorf("status = %d, want 403", foreign.Code)
	}
}

func TestKeyScope_Alertmanager(t *testing.T) {
	amCfg := testAlertmanagerConfig(t)
	amCfg.DefaultChatID = ownUUID
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}
	srv := newScopeServer([]ResolvedKey{scoped}, WithAlertmanager(amCfg))

	body := alertmanagerPayload("firing", AlertItem{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})

	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer narrow"})
	if w.Code != 200 {
		t.Errorf("default chat in scope: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	w = doRequest(srv, "POST", "/api/v1/alertmanager?chat_id="+otherUUID, strings.NewReader(body),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer narrow"})
	if w.Code != 403 {
		t.Errorf("foreign chat via ?chat_id=: status = %d, want 403 (body: %s)", w.Code, w.Body.String())
	}
}

func TestKeyScope_AliasListFiltered(t *testing.T) {
	entries := []config.ChatEntry{
		{Name: "own-chat", ID: ownUUID},
		{Name: "other-chat", ID: otherUUID},
	}
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}
	unscoped := ResolvedKey{Name: "any-app", Key: "open"}
	srv := newScopeServer([]ResolvedKey{scoped, unscoped}, WithConfigInfo(nil, entries))

	for _, tc := range []struct {
		key  string
		want int
	}{{"narrow", 1}, {"open", 2}} {
		w := doRequest(srv, "GET", "/api/v1/chats/alias/list", nil,
			map[string]string{"Authorization": "Bearer " + tc.key})
		var got []config.ChatEntry
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != tc.want {
			t.Errorf("key %s: %d entries, want %d (%v)", tc.key, len(got), tc.want, got)
		}
	}
}

// --- regressions from review (2026-09-07) ---

// Two keys sharing a name must not share permissions: the scope has to follow
// the credential that actually authenticated, not a name that may collide.
func TestKeyScope_DuplicateNamesDoNotShareScope(t *testing.T) {
	first := ResolvedKey{Name: "team", Key: "first", Chats: []string{ownUUID}}
	second := ResolvedKey{Name: "team", Key: "second", Chats: []string{otherUUID}}
	srv := newScopeServer([]ResolvedKey{first, second})

	if w := sendAs(srv, "first", otherUUID); w.Code != 403 {
		t.Errorf("first key reached the second key's chat: status = %d, want 403", w.Code)
	}
	if w := sendAs(srv, "second", ownUUID); w.Code != 403 {
		t.Errorf("second key reached the first key's chat: status = %d, want 403", w.Code)
	}
	if w := sendAs(srv, "first", ownUUID); w.Code != 200 {
		t.Errorf("first key lost its own chat: status = %d, want 200", w.Code)
	}
	if w := sendAs(srv, "second", otherUUID); w.Code != 200 {
		t.Errorf("second key lost its own chat: status = %d, want 200", w.Code)
	}
}

// The catalog that async delivery resolves against can disagree with the local
// config: the same alias may name a different chat there. Authorizing the local
// answer would authorize one chat while the message goes to another, so the
// send pipeline applies the scope to the address it is actually delivering to.
func TestKeyScope_AsyncCatalogDisagreesWithConfig(t *testing.T) {
	const catalogUUID = "a55cdddb-a5b2-5901-9b8b-f4bfc522b448"

	// Local config: own-chat -> ownUUID. Catalog: own-chat -> catalogUUID.
	entries := []config.ChatEntry{{Name: "own-chat", ID: ownUUID}}
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}

	var delivered string
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		chatID := p.ChatID
		if !isUUID(chatID) {
			chatID = catalogUUID
		}
		if !ChatAllowed(ctx, chatID) {
			return "", ErrChatNotAllowed
		}
		delivered = chatID
		return "req-id", nil
	}
	passthrough := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{scoped}, AsyncMode: true, DefaultRoutingMode: "catalog"}
	srv := New(cfg, sendFn, passthrough, WithConfigInfo(nil, entries))

	w := sendAs(srv, "narrow", "own-chat")
	if w.Code != 403 {
		t.Errorf("status = %d, want 403: the alias resolved to a chat outside the scope", w.Code)
	}
	if delivered != "" {
		t.Errorf("message was delivered to %s despite the scope", delivered)
	}
	if strings.Contains(w.Body.String(), catalogUUID) {
		t.Errorf("response named the catalog's chat: %s", w.Body.String())
	}
}

// In async mode the chat resolver is a pass-through; the scope for aliases is
// applied by the send pipeline once the address is final.
func TestKeyScope_AsyncMode(t *testing.T) {
	entries := []config.ChatEntry{
		{Name: "own-chat", ID: ownUUID},
		{Name: "other-chat", ID: otherUUID},
	}
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}

	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{scoped}, AsyncMode: true, DefaultRoutingMode: "mixed"}
	// Stands in for the enqueue pipeline: resolves the alias, then applies the
	// scope to the final address, as internal/cmd does before publishing.
	aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		chatID := p.ChatID
		if id, ok := aliases[chatID]; ok {
			chatID = id
		}
		if !ChatAllowed(ctx, chatID) {
			return "", ErrChatNotAllowed
		}
		return "req-id", nil
	}
	passthrough := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, passthrough, WithConfigInfo(nil, entries))

	// A bare UUID in mixed routing mode needs an explicit bot; that rule is
	// unrelated to scoping, so the UUID cases carry one.
	sendAsync := func(chatID string) *httptest.ResponseRecorder {
		body := strings.NewReader(fmt.Sprintf(`{"chat_id":%q,"message":"hi","bot":"b"}`, chatID))
		return doRequest(srv, "POST", "/api/v1/send", body, map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer narrow",
		})
	}

	tests := []struct {
		name   string
		chatID string
		want   int
	}{
		{"own alias resolves through the catalog", "own-chat", 202},
		{"own uuid", ownUUID, 202},
		{"foreign alias", "other-chat", 403},
		{"foreign uuid", otherUUID, 403},
		{"alias the pipeline cannot resolve", "catalog-only", 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if w := sendAsync(tc.chatID); w.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// --- multi-chat fan-out (rebase onto main) ---

// chat_id accepts a comma-separated list and delivery is best-effort per target,
// so a scope checked on one chat while the fan-out sends to several would be no
// scope at all. A list naming any chat outside the scope is refused whole.
func TestKeyScope_FanoutCannotSmuggleForeignChat(t *testing.T) {
	scoped := ResolvedKey{Name: "b2c-at", Key: "narrow", Chats: []string{ownUUID}}

	var delivered []string
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		delivered = append(delivered, p.ChatID)
		return "sync-id", nil
	}
	aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
	resolver := func(chatID string) (ChatResolveResult, error) {
		if id, ok := aliases[chatID]; ok {
			return ChatResolveResult{ChatID: id}, nil
		}
		if chatID == ownUUID || chatID == otherUUID {
			return ChatResolveResult{ChatID: chatID}, nil
		}
		return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q, available: other-chat, own-chat", chatID)
	}
	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{scoped}}
	srv := New(cfg, sendFn, resolver)

	for _, chatID := range []string{
		"own-chat,other-chat",
		"other-chat,own-chat",
		"own-chat," + otherUUID,
		ownUUID + ",other-chat",
	} {
		t.Run(chatID, func(t *testing.T) {
			delivered = nil
			w := sendAs(srv, "narrow", chatID)
			if w.Code != 403 {
				t.Errorf("status = %d, want 403 (body: %s)", w.Code, w.Body.String())
			}
			if len(delivered) != 0 {
				t.Errorf("partial delivery to %v despite a foreign chat in the list", delivered)
			}
		})
	}

	t.Run("all targets in scope", func(t *testing.T) {
		delivered = nil
		if w := sendAs(srv, "narrow", "own-chat,"+ownUUID); w.Code != 200 {
			t.Errorf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		if len(delivered) == 0 {
			t.Error("nothing delivered for an allowed list")
		}
	})
}

func TestKeyScope_SendDefaultsToSoleChat(t *testing.T) {
	sole := ResolvedKey{Name: "nxs-anomaly", Key: "narrow", Chats: []string{ownUUID}, AllowQueryAuth: true}
	pair := ResolvedKey{Name: "pair", Key: "pair", Chats: []string{ownUUID, otherUUID}, AllowQueryAuth: true}
	open := ResolvedKey{Name: "any-app", Key: "open", AllowQueryAuth: true}

	tests := []struct {
		name          string
		defaultChat   string
		target        string
		body          string
		wantCode      int
		wantDelivered []string
	}{
		{"sole-chat key without chat_id goes to its chat", "", "/api/v1/send?api_key=narrow", `{"text":"hi"}`, 200, []string{ownUUID}},
		{"sole-chat key ignores chat_id in the query", "", "/api/v1/send?api_key=narrow&chat_id=" + otherUUID, `{"text":"hi"}`, 200, []string{ownUUID}},
		{"sole-chat key wins over the global default", "other-chat", "/api/v1/send?api_key=narrow", `{"text":"hi"}`, 200, []string{ownUUID}},
		{"sole-chat key still refused a foreign chat in the body", "", "/api/v1/send?api_key=narrow", `{"chat_id":"` + otherUUID + `","text":"hi"}`, 403, nil},
		{"multi-chat key without chat_id needs one", "", "/api/v1/send?api_key=pair", `{"text":"hi"}`, 400, nil},
		{"unscoped key without chat_id needs one", "", "/api/v1/send?api_key=open", `{"text":"hi"}`, 400, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var delivered []string
			cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{sole, pair, open}, DefaultChatAlias: tc.defaultChat}
			sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
				delivered = append(delivered, p.ChatID)
				return "sync-id", nil
			}
			aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
			chatResolver := func(chatID string) (ChatResolveResult, error) {
				if id, ok := aliases[chatID]; ok {
					return ChatResolveResult{ChatID: id}, nil
				}
				return ChatResolveResult{ChatID: chatID}, nil
			}
			srv := New(cfg, sendFn, chatResolver)

			w := doRequest(srv, "POST", tc.target, strings.NewReader(tc.body), map[string]string{"Content-Type": "application/json"})
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantCode, w.Body.String())
			}
			if fmt.Sprint(delivered) != fmt.Sprint(tc.wantDelivered) {
				t.Errorf("delivered to %v, want %v", delivered, tc.wantDelivered)
			}
		})
	}
}
