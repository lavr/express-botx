package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lavr/express-botx/internal/config"
)

// scopeTestServer builds a bare Server whose chat resolver maps a fixed set of
// aliases to UUIDs. "bad-alias" returns a resolve error; other unknown values
// pass through as themselves, mirroring how a UUID resolves to itself. It is used
// to unit-test senderScopeTargets without going through the HTTP handler.
func scopeTestServer(t *testing.T) *Server {
	t.Helper()
	aliases := map[string]string{
		"team-a-alerts": "uuid-a",
		"team-a-dev":    "uuid-b",
		"team-b-alerts": "uuid-c",
	}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		if chatID == "bad-alias" {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
		}
		if u, ok := aliases[chatID]; ok {
			return ChatResolveResult{ChatID: u}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	return New(Config{Listen: ":0", BasePath: "/api/v1"}, okSend, chatFn)
}

// TestSenderScopeTargets covers alias/UUID membership equivalence, out-of-scope
// detection, and canonicalisation to the configured sender target for the
// sender-scoped ?chat_id filter.
func TestSenderScopeTargets(t *testing.T) {
	s := scopeTestServer(t)
	tests := []struct {
		name        string
		requested   []string
		allowed     []string
		wantTargets []string
		wantBad     string
		wantOK      bool
	}{
		{
			name:        "alias matches same alias",
			requested:   []string{"team-a-alerts"},
			allowed:     []string{"team-a-alerts"},
			wantTargets: []string{"team-a-alerts"},
			wantOK:      true,
		},
		{
			name:        "alias request against UUID in scope canonicalises to UUID",
			requested:   []string{"team-a-alerts"},
			allowed:     []string{"uuid-a"},
			wantTargets: []string{"uuid-a"},
			wantOK:      true,
		},
		{
			name:        "UUID request against alias in scope canonicalises to alias",
			requested:   []string{"uuid-a"},
			allowed:     []string{"team-a-alerts"},
			wantTargets: []string{"team-a-alerts"},
			wantOK:      true,
		},
		{
			name:      "foreign alias is a violation",
			requested: []string{"team-b-alerts"},
			allowed:   []string{"team-a-alerts"},
			wantBad:   "team-b-alerts",
			wantOK:    false,
		},
		{
			name:      "foreign UUID is a violation",
			requested: []string{"uuid-c"},
			allowed:   []string{"team-a-alerts"},
			wantBad:   "uuid-c",
			wantOK:    false,
		},
		{
			name:      "unresolvable requested chat is a violation",
			requested: []string{"bad-alias"},
			allowed:   []string{"team-a-alerts"},
			wantBad:   "bad-alias",
			wantOK:    false,
		},
		{
			name:        "subset of several is allowed",
			requested:   []string{"team-a-dev"},
			allowed:     []string{"team-a-alerts", "team-a-dev"},
			wantTargets: []string{"team-a-dev"},
			wantOK:      true,
		},
		{
			name:        "alias and its own UUID collapse to one target",
			requested:   []string{"team-a-alerts", "uuid-a"},
			allowed:     []string{"team-a-alerts"},
			wantTargets: []string{"team-a-alerts"},
			wantOK:      true,
		},
		{
			name:      "one out-of-scope among valid returns that one",
			requested: []string{"team-a-alerts", "team-b-alerts"},
			allowed:   []string{"team-a-alerts", "team-a-dev"},
			wantBad:   "team-b-alerts",
			wantOK:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := make(map[string]GitlabTarget, len(tt.allowed))
			for _, target := range tt.allowed {
				resolved, err := s.chats(target)
				if err != nil {
					t.Fatalf("resolve allowed target %q: %v", target, err)
				}
				scope[resolved.ChatID] = GitlabTarget{Target: target}
			}
			targets, reject, ok := s.senderScopeTargets(tt.requested, scope)
			if ok != tt.wantOK || !slices.Equal(targets, tt.wantTargets) {
				t.Errorf("senderScopeTargets(%v, %v) = (%v, %q, %v), want (%v, bad %q, %v)",
					tt.requested, tt.allowed, targets, reject, ok, tt.wantTargets, tt.wantBad, tt.wantOK)
			}
			if tt.wantBad != "" && !strings.Contains(reject, fmt.Sprintf("%q", tt.wantBad)) {
				t.Errorf("reject message %q does not name chat %q", reject, tt.wantBad)
			}
		})
	}
}

// newGitlabTestServer builds a server with the gitlab endpoint enabled and a
// send function that records the last SendPayload it received.
func newGitlabTestServer(t *testing.T, cfg *GitlabConfig) (*Server, *captureSend) {
	return newGitlabTestServerCfg(t, Config{Listen: ":0", BasePath: "/api/v1"}, cfg, nil)
}

// newGitlabTestServerCfg is like newGitlabTestServer but lets the caller control
// the server Config (e.g. DefaultChatAlias) and inject a send error.
func newGitlabTestServerCfg(t *testing.T, srvCfg Config, cfg *GitlabConfig, sendErr error) (*Server, *captureSend) {
	t.Helper()
	for i := range cfg.Senders {
		if cfg.Senders[i].Templates == nil {
			tmpls, err := ParseGitlabTemplates(nil)
			if err != nil {
				t.Fatalf("parse default templates: %v", err)
			}
			cfg.Senders[i].Templates = tmpls
		}
	}
	cap := &captureSend{}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		if sendErr != nil {
			return "", sendErr
		}
		return "sync-1", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if chatID == "unknown-alias" {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(srvCfg, sendFn, chatResolver, WithGitlab(cfg))
	return srv, cap
}

func gitlabTestSender(secret string, targets ...string) GitlabSender {
	scope := make(map[string]GitlabTarget, len(targets))
	for _, target := range targets {
		scope[target] = GitlabTarget{Target: target}
	}
	return GitlabSender{Secret: secret, Scope: scope, Targets: targets}
}

func gitlabTestConfig(secret string, targets ...string) *GitlabConfig {
	return &GitlabConfig{Senders: []GitlabSender{gitlabTestSender(secret, targets...)}}
}

// captureSend records the payloads passed to the send function.
type captureSend struct {
	mu    sync.Mutex
	calls []*SendPayload
}

func (c *captureSend) record(p *SendPayload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *p
	c.calls = append(c.calls, &cp)
}

func (c *captureSend) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *captureSend) last() *SendPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return nil
	}
	return c.calls[len(c.calls)-1]
}

const (
	mrOpenPayload = `{
		"object_kind": "merge_request",
		"user": {"name": "Alice", "username": "alice"},
		"project": {"name": "myproj", "web_url": "https://gl/myproj"},
		"object_attributes": {
			"action": "open",
			"title": "Add feature X",
			"url": "https://gl/myproj/-/merge_requests/1",
			"source_branch": "feature-x",
			"target_branch": "main",
			"detailed_merge_status": "mergeable"
		}
	}`
	mrMergePayload = `{
		"object_kind": "merge_request",
		"user": {"name": "Bob", "username": "bob"},
		"project": {"name": "myproj"},
		"object_attributes": {
			"action": "merge",
			"title": "Add feature X",
			"url": "https://gl/myproj/-/merge_requests/1",
			"source_branch": "feature-x",
			"target_branch": "main"
		}
	}`
	mrUpdatePayload = `{
		"object_kind": "merge_request",
		"user": {"name": "Alice"},
		"object_attributes": {"action": "update", "title": "Add feature X"}
	}`
	noteCommentPayload = `{
		"object_kind": "note",
		"user": {"name": "Carol", "username": "carol"},
		"project": {"name": "myproj"},
		"object_attributes": {
			"note": "Looks good to me",
			"noteable_type": "MergeRequest",
			"url": "https://gl/myproj/-/merge_requests/1#note_1",
			"system": false
		},
		"merge_request": {
			"title": "Add feature X",
			"url": "https://gl/myproj/-/merge_requests/1",
			"source_branch": "feature-x",
			"target_branch": "main"
		}
	}`
	pushPayload = `{
		"object_kind": "push",
		"user_name": "Erin",
		"project": {"name": "myproj", "web_url": "https://gl/myproj"},
		"ref": "refs/heads/main"
	}`
	tagPushPayload = `{
		"object_kind": "tag_push",
		"user_name": "Erin",
		"project": {"name": "myproj", "web_url": "https://gl/myproj"},
		"ref": "refs/tags/v1.0.0"
	}`
	pipelinePayload = `{
		"object_kind": "pipeline",
		"user": {"name": "Frank"},
		"project": {"name": "myproj", "web_url": "https://gl/myproj"},
		"object_attributes": {"status": "failed", "id": 42}
	}`
	jobPayload = `{
		"object_kind": "build",
		"build_status": "success",
		"user": {"name": "Grace"},
		"project": {"name": "myproj", "web_url": "https://gl/myproj"}
	}`
	issuePayload = `{
		"object_kind": "issue",
		"user": {"name": "Heidi"},
		"project": {"name": "myproj", "web_url": "https://gl/myproj"},
		"object_attributes": {"action": "open", "title": "Something broke", "url": "https://gl/myproj/-/issues/7"}
	}`
	unknownKindPayload = `{
		"object_kind": "wiki_page",
		"user": {"name": "Ivan"},
		"project": {"name": "myproj", "web_url": "https://gl/myproj"}
	}`
	// mrOpenUsernameOnlyPayload carries only user.username (no user.name), to
	// exercise the author fallback.
	mrOpenUsernameOnlyPayload = `{
		"object_kind": "merge_request",
		"user": {"username": "jane"},
		"project": {"name": "myproj"},
		"object_attributes": {
			"action": "open",
			"title": "Add feature Y",
			"url": "https://gl/myproj/-/merge_requests/2",
			"source_branch": "feature-y",
			"target_branch": "main"
		}
	}`
)

func gitlabHeaders(token string) map[string]string {
	return map[string]string{
		"Content-Type":   "application/json",
		"X-Gitlab-Token": token,
	}
}

// mustDecode parses a JSON payload string into a map for the unit tests.
func mustDecode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return m
}

func TestDeriveEventKey(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		wantKind    string
		wantSubtype string
		wantKey     string
	}{
		{"mr_open", mrOpenPayload, "merge_request", "open", "merge_request.open"},
		{"mr_merge", mrMergePayload, "merge_request", "merge", "merge_request.merge"},
		{"note_mr", noteCommentPayload, "note", "MergeRequest", "note.MergeRequest"},
		{"pipeline_status", pipelinePayload, "pipeline", "failed", "pipeline.failed"},
		{"build_status", jobPayload, "build", "success", "build.success"},
		{"push", pushPayload, "push", "", "push"},
		{"tag_push", tagPushPayload, "tag_push", "", "tag_push"},
		{"issue_action", issuePayload, "issue", "open", "issue.open"},
		{"unknown_kind", unknownKindPayload, "wiki_page", "", "wiki_page"},
		{"empty", `{}`, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, subtype, key := deriveEventKey(mustDecode(t, tc.payload))
			if kind != tc.wantKind || subtype != tc.wantSubtype || key != tc.wantKey {
				t.Errorf("deriveEventKey = (%q, %q, %q), want (%q, %q, %q)",
					kind, subtype, key, tc.wantKind, tc.wantSubtype, tc.wantKey)
			}
		})
	}
}

func TestDeriveEventKey_FallbackSubtype(t *testing.T) {
	// An unknown kind with object_attributes.action should use the action.
	kind, subtype, key := deriveEventKey(mustDecode(t, `{
		"object_kind": "deployment",
		"object_attributes": {"action": "created"}
	}`))
	if kind != "deployment" || subtype != "created" || key != "deployment.created" {
		t.Errorf("got (%q, %q, %q), want deployment/created/deployment.created", kind, subtype, key)
	}
	// An unknown kind with object_attributes.status (no action) uses the status.
	kind, subtype, key = deriveEventKey(mustDecode(t, `{
		"object_kind": "deployment",
		"object_attributes": {"status": "running"}
	}`))
	if kind != "deployment" || subtype != "running" || key != "deployment.running" {
		t.Errorf("got (%q, %q, %q), want deployment/running/deployment.running", kind, subtype, key)
	}
	// An unknown kind with only a flat top-level status (real GitLab deployment
	// hooks have no object_attributes) falls back to that status.
	kind, subtype, key = deriveEventKey(mustDecode(t, `{
		"object_kind": "deployment",
		"status": "failed"
	}`))
	if kind != "deployment" || subtype != "failed" || key != "deployment.failed" {
		t.Errorf("got (%q, %q, %q), want deployment/failed/deployment.failed", kind, subtype, key)
	}
}

func TestNormalizeGitlab(t *testing.T) {
	t.Run("merge_request", func(t *testing.T) {
		v := normalizeGitlab(mustDecode(t, mrOpenPayload))
		if v.Kind != "merge_request" || v.EventKey != "merge_request.open" || v.Action != "open" {
			t.Errorf("kind/key/action = %q/%q/%q", v.Kind, v.EventKey, v.Action)
		}
		if v.Project != "myproj" || v.User != "Alice" || v.Title != "Add feature X" {
			t.Errorf("project/user/title = %q/%q/%q", v.Project, v.User, v.Title)
		}
		if v.URL != "https://gl/myproj/-/merge_requests/1" {
			t.Errorf("url = %q", v.URL)
		}
	})
	t.Run("username_fallback", func(t *testing.T) {
		v := normalizeGitlab(mustDecode(t, mrOpenUsernameOnlyPayload))
		if v.User != "jane" {
			t.Errorf("user = %q, want username fallback jane", v.User)
		}
	})
	t.Run("push_user_name_and_web_url_fallback", func(t *testing.T) {
		v := normalizeGitlab(mustDecode(t, pushPayload))
		if v.User != "Erin" {
			t.Errorf("user = %q, want user_name fallback Erin", v.User)
		}
		if v.URL != "https://gl/myproj" {
			t.Errorf("url = %q, want project.web_url fallback", v.URL)
		}
		if v.Title != "" {
			t.Errorf("title = %q, want empty for push", v.Title)
		}
	})
	t.Run("path_with_namespace_fallback", func(t *testing.T) {
		v := normalizeGitlab(mustDecode(t, `{
			"object_kind": "push",
			"project": {"path_with_namespace": "grp/myproj"}
		}`))
		if v.Project != "grp/myproj" {
			t.Errorf("project = %q, want path_with_namespace fallback", v.Project)
		}
	})
	t.Run("empty_payload", func(t *testing.T) {
		v := normalizeGitlab(map[string]any{})
		if v.Kind != "" || v.EventKey != "" || v.Project != "" || v.User != "" {
			t.Errorf("expected zero view, got %+v", v)
		}
	})
}

func TestGitlabGet(t *testing.T) {
	raw := mustDecode(t, mrOpenPayload)
	// Success: nested string.
	if got := gitlabNestedGet(raw, "object_attributes.title"); got != "Add feature X" {
		t.Errorf("get title = %v", got)
	}
	// Success via the view helper.
	v := gitlabView{Raw: raw}
	if got := v.Get("project.web_url"); got != "https://gl/myproj" {
		t.Errorf("Get project.web_url = %v", got)
	}
	// Miss: absent leaf.
	if got := gitlabNestedGet(raw, "object_attributes.nope"); got != nil {
		t.Errorf("get absent leaf = %v, want nil", got)
	}
	// Miss: path descends through a non-object.
	if got := gitlabNestedGet(raw, "object_attributes.title.deeper"); got != nil {
		t.Errorf("get through scalar = %v, want nil", got)
	}
	// Nil map and empty path.
	if got := gitlabNestedGet(nil, "a.b"); got != nil {
		t.Errorf("get on nil map = %v, want nil", got)
	}
	if got := gitlabNestedGet(raw, ""); got != nil {
		t.Errorf("get empty path = %v, want nil", got)
	}
}

func TestGitlab_GenericRender(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wants   []string
	}{
		// merge_request/note/push/pipeline/issue have built-in per-event templates;
		// job (build) and wiki_page fall through to the generic default (which
		// prints the raw event key).
		{"mr_open", mrOpenPayload, []string{"myproj", "Add feature X", "Alice"}},
		{"mr_merge", mrMergePayload, []string{"Add feature X", "Bob"}},
		{"note", noteCommentPayload, []string{"Carol", "Looks good to me"}},
		{"push", pushPayload, []string{"myproj", "Erin"}},
		{"pipeline", pipelinePayload, []string{"myproj", "failed"}},
		{"job", jobPayload, []string{"build.success", "Grace"}},
		{"issue", issuePayload, []string{"Something broke", "Heidi"}},
		{"unknown_kind", unknownKindPayload, []string{"wiki_page", "myproj", "Ivan"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "chat1"))
			w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(tc.payload), gitlabHeaders("secret"))
			if w.Code != 200 {
				t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 1 {
				t.Fatalf("send count = %d, want 1", cap.count())
			}
			msg := cap.last().Message
			for _, want := range tc.wants {
				if !strings.Contains(msg, want) {
					t.Errorf("message missing %q:\n%s", want, msg)
				}
			}
		})
	}
}

func TestGitlab_TokenValidation(t *testing.T) {
	cases := []struct {
		name     string
		token    string
		wantCode int
		wantSend int
	}{
		{"valid", "secret", 200, 1},
		{"invalid", "wrong", 401, 0},
		{"empty", "", 401, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "chat1"))
			headers := map[string]string{"Content-Type": "application/json"}
			if tc.token != "" {
				headers["X-Gitlab-Token"] = tc.token
			}
			w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), headers)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantCode, w.Body.String())
			}
			if cap.count() != tc.wantSend {
				t.Errorf("send count = %d, want %d", cap.count(), tc.wantSend)
			}
		})
	}
}

func TestResolveGitlabAuthReturnsMatchedRuntime(t *testing.T) {
	want := GitlabSender{Label: "dev", Secret: "dev-token"}
	s := &Server{gitCfg: &GitlabConfig{Senders: []GitlabSender{
		want,
		{Label: "ops", Secret: "ops-token"},
	}}}
	got, ok := s.resolveGitlabAuth("dev-token")
	if !ok || got.Label != "dev" {
		t.Fatalf("got (%+v, %v), want dev sender", got, ok)
	}
}

// An empty sender secret must never authenticate: serve startup rejects empty
// secrets, but a hand-built config (WithGitlab) can carry one, and it must not
// match an empty or arbitrary token.
func TestResolveGitlabAuthEmptySenderSecretNeverMatches(t *testing.T) {
	s := &Server{gitCfg: &GitlabConfig{Senders: []GitlabSender{
		{Label: "broken", Secret: ""},
	}}}
	for _, token := range []string{"", "anything"} {
		if got, ok := s.resolveGitlabAuth(token); ok || got != nil {
			t.Fatalf("token %q: got (%+v, %v), want no match", token, got, ok)
		}
	}
}

// On duplicate secrets (possible only in hand-built configs; serve startup
// rejects them) the first matching sender must win, deterministically.
func TestResolveGitlabAuthFirstSenderWinsOnDuplicateSecrets(t *testing.T) {
	s := &Server{gitCfg: &GitlabConfig{Senders: []GitlabSender{
		{Label: "first", Secret: "dup"},
		{Label: "second", Secret: "dup"},
	}}}
	got, ok := s.resolveGitlabAuth("dup")
	if !ok || got.Label != "first" {
		t.Fatalf("got (%+v, %v), want first sender", got, ok)
	}
}

func TestGitlab_PerSenderIsolation(t *testing.T) {
	devTemplates, err := ParseGitlabTemplates(map[string]string{
		"merge_request.open": "DEV {{ .Title }}",
	})
	if err != nil {
		t.Fatalf("parse dev templates: %v", err)
	}
	opsTemplates, err := ParseGitlabTemplates(map[string]string{
		"merge_request.open": "OPS {{ .Title }}",
	})
	if err != nil {
		t.Fatalf("parse ops templates: %v", err)
	}

	cfg := &GitlabConfig{Senders: []GitlabSender{
		{
			Label:       "dev",
			Secret:      "dev-token",
			Scope:       map[string]GitlabTarget{"dev-route": {Target: "dev-route"}},
			Only:        []string{"merge_request.*"},
			ErrorEvents: []string{"merge_request.open"},
			Templates:   devTemplates,
			Routes:      []compiledRoute{{chats: []string{"dev-route"}}},
		},
		{
			Label:     "ops",
			Secret:    "ops-token",
			Scope:     map[string]GitlabTarget{"ops-route": {Target: "ops-route"}},
			Exclude:   []string{"pipeline.*"},
			Templates: opsTemplates,
			Routes:    []compiledRoute{{chats: []string{"ops-route"}}},
		},
	}}

	tests := []struct {
		name       string
		token      string
		wantCode   int
		wantChat   string
		wantPrefix string
		wantStatus string
	}{
		{name: "dev runtime", token: "dev-token", wantCode: http.StatusOK, wantChat: "dev-route", wantPrefix: "DEV ", wantStatus: "error"},
		{name: "ops runtime", token: "ops-token", wantCode: http.StatusOK, wantChat: "ops-route", wantPrefix: "OPS ", wantStatus: "ok"},
		{name: "unknown token", token: "unknown", wantCode: http.StatusUnauthorized},
		{name: "empty token", token: "", wantCode: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
			headers := gitlabHeaders(tt.token)
			if tt.token == "" {
				delete(headers, "X-Gitlab-Token")
			}
			w := doRequest(srv, "POST", "/api/v1/gitlab?bot=other", strings.NewReader(mrOpenPayload), headers)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode != http.StatusOK {
				if cap.count() != 0 {
					t.Fatalf("send count = %d, want 0", cap.count())
				}
				return
			}
			if cap.count() != 1 {
				t.Fatalf("send count = %d, want 1", cap.count())
			}
			got := cap.last()
			if got.ChatID != tt.wantChat || !strings.HasPrefix(got.Message, tt.wantPrefix) || got.Status != tt.wantStatus {
				t.Errorf("send = %+v, want chat %q, message prefix %q, status %q", got, tt.wantChat, tt.wantPrefix, tt.wantStatus)
			}
		})
	}
}

func TestGitlab_SenderSelectionMatrix(t *testing.T) {
	templates, err := ParseGitlabTemplates(nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	matchingRoutes := []compiledRoute{{chats: []string{"alerts"}}}
	nonMatchingRoutes := []compiledRoute{{
		conds: []compiledCondition{{selector: "project", matchers: []patternMatcher{globMatcher{pattern: "other"}}}},
		chats: []string{"alerts"},
	}}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		switch chatID {
		case "alerts":
			return ChatResolveResult{ChatID: "uuid-a"}, nil
		case "dev":
			return ChatResolveResult{ChatID: "uuid-b"}, nil
		default:
			return ChatResolveResult{ChatID: chatID}, nil
		}
	}

	tests := []struct {
		name       string
		query      string
		routes     []compiledRoute
		wantCode   int
		wantChats  []string
		wantReason string
	}{
		{name: "query canonical without routes", query: "?chat_id=uuid-a,uuid-a", wantCode: http.StatusOK, wantChats: []string{"uuid-a"}},
		{name: "query alias derived through resolver", query: "?chat_id=alerts,uuid-a", wantCode: http.StatusOK, wantChats: []string{"uuid-a"}},
		{name: "query overrides routes", query: "?chat_id=uuid-b", routes: matchingRoutes, wantCode: http.StatusOK, wantChats: []string{"uuid-b"}},
		{name: "targets without query or routes", wantCode: http.StatusOK, wantChats: []string{"uuid-a", "uuid-b"}},
		{name: "routes without query", routes: matchingRoutes, wantCode: http.StatusOK, wantChats: []string{"uuid-a"}},
		{name: "empty query", query: "?chat_id=,,", wantCode: http.StatusBadRequest},
		{name: "partial out of scope", query: "?chat_id=uuid-a,foreign", wantCode: http.StatusForbidden},
		{name: "no route match", routes: nonMatchingRoutes, wantCode: http.StatusOK, wantReason: "no route matched"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &GitlabConfig{Senders: []GitlabSender{{
				Label:     "dev",
				Secret:    "dev-token",
				Scope:     map[string]GitlabTarget{"uuid-a": {Target: "alerts"}, "uuid-b": {Target: "dev"}},
				Targets:   []string{"alerts", "dev"},
				Templates: templates,
				Routes:    tt.routes,
			}}}
			srv, cap := newGitlabFanoutServer(t, cfg, okSend, chatFn)
			w := doRequest(srv, "POST", "/api/v1/gitlab"+tt.query, strings.NewReader(mrOpenPayload), gitlabHeaders("dev-token"))
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if cap.count() != len(tt.wantChats) {
				t.Fatalf("send count = %d, want %d", cap.count(), len(tt.wantChats))
			}
			for i, want := range tt.wantChats {
				if got := cap.calls[i].ChatID; got != want {
					t.Errorf("send[%d].chat = %q, want %q", i, got, want)
				}
			}
			if tt.wantReason != "" {
				var body struct {
					OK      bool   `json:"ok"`
					Ignored bool   `json:"ignored"`
					Event   string `json:"event"`
					Reason  string `json:"reason"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
				}
				if !body.OK || !body.Ignored || body.Reason != tt.wantReason {
					t.Errorf("response = %+v, want ignored reason %q", body, tt.wantReason)
				}
			}
		})
	}
}

func TestSenderScopeTargetsUsesPrebuiltScope(t *testing.T) {
	s := scopeTestServer(t)
	targets, reject, ok := s.senderScopeTargets(
		[]string{"team-a-alerts", "uuid-a", "team-a-dev"},
		map[string]GitlabTarget{"uuid-a": {Target: "team-a-alerts"}, "uuid-b": {Target: "team-a-dev"}},
	)
	if !ok || reject != "" || !slices.Equal(targets, []string{"team-a-alerts", "team-a-dev"}) {
		t.Fatalf("got (%v, %q, %v), want ([team-a-alerts team-a-dev], empty, true)", targets, reject, ok)
	}
}

// A requested alias whose bot binding contradicts the scope target's binding is
// rejected: honoring it would silently switch the sending bot (the runtime
// mirror of the startup route bot-conflict check).
func TestSenderScopeTargetsRejectsBotConflict(t *testing.T) {
	chatFn := func(chatID string) (ChatResolveResult, error) {
		if chatID == "alerts-ops" {
			return ChatResolveResult{ChatID: "uuid-a", Bot: "ops"}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	s := New(Config{Listen: ":0", BasePath: "/api/v1"}, okSend, chatFn)
	scope := map[string]GitlabTarget{"uuid-a": {Target: "alerts-dev", Bot: "dev"}}

	_, reject, ok := s.senderScopeTargets([]string{"alerts-ops"}, scope)
	if ok || !strings.Contains(reject, `"alerts-ops" is bound to bot "ops"`) {
		t.Fatalf("got (ok=%v, reject=%q), want bot-conflict rejection", ok, reject)
	}

	// An unbound reference (raw UUID) to the same chat stays allowed.
	targets, reject, ok := s.senderScopeTargets([]string{"uuid-a"}, scope)
	if !ok || !slices.Equal(targets, []string{"alerts-dev"}) {
		t.Fatalf("got (%v, %q, %v), want ([alerts-dev], empty, true)", targets, reject, ok)
	}
}

// WithGitlab must normalize hand-built senders: a nil Templates registry gets
// the built-in defaults (instead of a nil-pointer panic on the first rendered
// event) and a missing Label gets the "senders[i]" fallback.
func TestWithGitlabNormalizesHandBuiltSenders(t *testing.T) {
	cfg := &GitlabConfig{Senders: []GitlabSender{{
		Secret:  "tok",
		Scope:   map[string]GitlabTarget{"uuid-a": {Target: "uuid-a"}},
		Targets: []string{"uuid-a"},
	}}}
	cap := &captureSend{}
	send := func(ctx context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-1", nil
	}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(Config{Listen: ":0", BasePath: "/api/v1"}, send, chatFn, WithGitlab(cfg))

	if got := cfg.Senders[0].Label; got != "senders[0]" {
		t.Errorf("label = %q, want senders[0]", got)
	}
	if cfg.Senders[0].Templates == nil {
		t.Fatal("templates not defaulted")
	}
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("tok"))
	if w.Code != 200 || cap.count() != 1 {
		t.Fatalf("status = %d, sends = %d, want 200 and 1 send (body: %s)", w.Code, cap.count(), w.Body.String())
	}
}

// WithGitlab(nil) must leave the endpoint disabled rather than panic in New:
// callers may build the option from a maybe-nil config.
func TestWithGitlabNilConfigDisablesEndpoint(t *testing.T) {
	chatFn := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(Config{Listen: ":0", BasePath: "/api/v1"}, okSend, chatFn, WithGitlab(nil))
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404 (endpoint disabled)", w.Code)
	}
}

func TestGitlab_EndpointNotRegisteredWithoutConfig(t *testing.T) {
	// Without WithGitlab, the /gitlab route must not exist: a request to it
	// returns 404 (chi's default) rather than reaching the handler.
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404 (route should be unregistered)", w.Code)
	}
}

func TestGitlab_ChatOverride(t *testing.T) {
	srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "chat1", "chat2"))
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=chat2", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.last().ChatID != "chat2" {
		t.Errorf("chat = %q, want chat2 (override)", cap.last().ChatID)
	}
}

// TestGitlab_MultiChatOverride: ?chat_id may itself list several chats
// (comma-separated) and fans the event out to each, deduplicating repeats.
func TestGitlab_MultiChatOverride(t *testing.T) {
	srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "chatA", "chatB"))
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=chatA,+chatB+,chatA", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (deduped fan-out)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 2 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v, want ok with 2 results, 0 errors", resp)
	}
	if resp.Results[0].Chat != "chatA" || resp.Results[1].Chat != "chatB" {
		t.Errorf("results = %+v, want chatA then chatB in order", resp.Results)
	}
}

func TestGitlab_ChatResolveError(t *testing.T) {
	// A chat that fails to resolve is a per-chat delivery outcome, not a
	// request-level error: with the single default chat unresolvable, every
	// target fails -> 502 with the error in errors[] (unified contract).
	srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "unknown-alias"))
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (body: %s)", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Errorf("send count = %d, want 0", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if resp.OK || len(resp.Results) != 0 || len(resp.Errors) != 1 || resp.Errors[0].Chat != "unknown-alias" {
		t.Errorf("response = %+v, want not-ok with single unknown-alias error", resp)
	}
}

func TestGitlab_InvalidJSON(t *testing.T) {
	srv, _ := newGitlabTestServer(t, gitlabTestConfig("secret", "chat1"))
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader("{not json"), gitlabHeaders("secret"))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

func TestGitlab_AuthorUsernameFallback(t *testing.T) {
	srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "chat1"))
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenUsernameOnlyPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1", cap.count())
	}
	if !strings.Contains(cap.last().Message, "jane") {
		t.Errorf("message missing username fallback %q:\n%s", "jane", cap.last().Message)
	}
}

func TestGitlab_SendFailure(t *testing.T) {
	srv, cap := newGitlabTestServerCfg(t, Config{Listen: ":0", BasePath: "/api/v1"},
		gitlabTestConfig("secret", "chat1"), fmt.Errorf("botx unavailable"))
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (body: %s)", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Errorf("send count = %d, want 1 (send attempted then failed)", cap.count())
	}
}

func TestEventMatches(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		eventKey string
		entries  []string
		want     bool
	}{
		{"exact_key", "merge_request", "merge_request.open", []string{"merge_request.open"}, true},
		{"bare_kind", "merge_request", "merge_request.open", []string{"merge_request"}, true},
		{"wildcard", "merge_request", "merge_request.merge", []string{"merge_request.*"}, true},
		{"no_match", "merge_request", "merge_request.open", []string{"pipeline.failed"}, false},
		{"empty_entries", "push", "push", nil, false},
		{"push_bare", "push", "push", []string{"push"}, true},
		{"among_many", "pipeline", "pipeline.failed", []string{"push", "pipeline.failed", "merge_request.*"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventMatches(tc.kind, tc.eventKey, tc.entries); got != tc.want {
				t.Errorf("eventMatches(%q,%q,%v) = %v, want %v", tc.kind, tc.eventKey, tc.entries, got, tc.want)
			}
		})
	}
}

func TestPassesFilter(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		eventKey string
		only     []string
		exclude  []string
		want     bool
	}{
		{"only_empty_passes_all", "push", "push", nil, nil, true},
		{"only_set_not_listed", "push", "push", []string{"merge_request.*"}, nil, false},
		{"only_set_listed", "merge_request", "merge_request.open", []string{"merge_request.*"}, nil, true},
		{"exclude_subtracts", "merge_request", "merge_request.update", nil, []string{"merge_request.update"}, false},
		{"wildcard_only", "merge_request", "merge_request.merge", []string{"merge_request.*"}, nil, true},
		{"bare_kind_matches_open", "merge_request", "merge_request.open", []string{"merge_request"}, nil, true},
		{"bare_kind_matches_merge", "merge_request", "merge_request.merge", []string{"merge_request"}, nil, true},
		{"exclude_wins_over_only", "merge_request", "merge_request.update", []string{"merge_request.*"}, []string{"merge_request.update"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := passesFilter(tc.kind, tc.eventKey, tc.only, tc.exclude); got != tc.want {
				t.Errorf("passesFilter(%q,%q,%v,%v) = %v, want %v",
					tc.kind, tc.eventKey, tc.only, tc.exclude, got, tc.want)
			}
		})
	}
}

func TestGitlab_FilterIgnoresEvent(t *testing.T) {
	t.Run("excluded_event_ignored", func(t *testing.T) {
		cfg := gitlabTestConfig("secret", "chat1")
		cfg.Senders[0].Exclude = []string{"merge_request.update"}
		srv, cap := newGitlabTestServer(t, cfg)
		w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrUpdatePayload), gitlabHeaders("secret"))
		if w.Code != 200 {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		if cap.count() != 0 {
			t.Errorf("send count = %d, want 0 (event ignored)", cap.count())
		}
		var resp gitlabIgnoredResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
		}
		if !resp.OK || !resp.Ignored || resp.Event != "merge_request.update" {
			t.Errorf("response = %+v, want ok/ignored merge_request.update", resp)
		}
	})
	t.Run("only_not_listed_ignored", func(t *testing.T) {
		cfg := gitlabTestConfig("secret", "chat1")
		cfg.Senders[0].Only = []string{"pipeline.*"}
		srv, cap := newGitlabTestServer(t, cfg)
		w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		if w.Code != 200 {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		if cap.count() != 0 {
			t.Errorf("send count = %d, want 0 (not in only)", cap.count())
		}
	})
	t.Run("only_listed_passes", func(t *testing.T) {
		cfg := gitlabTestConfig("secret", "chat1")
		cfg.Senders[0].Only = []string{"merge_request.*"}
		srv, cap := newGitlabTestServer(t, cfg)
		w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		if w.Code != 200 {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		if cap.count() != 1 {
			t.Errorf("send count = %d, want 1 (matches only)", cap.count())
		}
	})
}

func TestGitlab_EmptyMessageIgnored(t *testing.T) {
	// A payload without object_kind renders the generic default to an empty
	// string; with an allow-all config it must be ignored, not sent blank.
	srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "chat1"))
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(`{}`), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Errorf("send count = %d, want 0 (empty message ignored)", cap.count())
	}
	var resp gitlabIgnoredResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || !resp.Ignored {
		t.Errorf("response = %+v, want ok/ignored", resp)
	}
}

func TestGitlab_ErrorEventsStatus(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		errorEvents []string
		wantStatus  string
	}{
		{"exact_key_error", pipelinePayload, []string{"pipeline.failed"}, "error"},
		{"ordinary_event_ok", mrOpenPayload, []string{"pipeline.failed"}, "ok"},
		{"wildcard_error", pipelinePayload, []string{"pipeline.*"}, "error"},
		{"empty_error_events_ok", pipelinePayload, nil, "ok"},
		{"bare_kind_error", jobPayload, []string{"build"}, "error"},
		{"not_in_error_events_ok", mrOpenPayload, []string{"pipeline.*", "build.failed"}, "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := gitlabTestConfig("secret", "chat1")
			cfg.Senders[0].ErrorEvents = tc.errorEvents
			srv, cap := newGitlabTestServer(t, cfg)
			w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(tc.payload), gitlabHeaders("secret"))
			if w.Code != 200 {
				t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 1 {
				t.Fatalf("send count = %d, want 1", cap.count())
			}
			if got := cap.last().Status; got != tc.wantStatus {
				t.Errorf("status = %q, want %q", got, tc.wantStatus)
			}
		})
	}
}

func TestGitlabTemplates_Selection(t *testing.T) {
	gt, err := ParseGitlabTemplates(map[string]string{
		"issue.open": "OPEN {{ .Title }}",
		"issue":      "ISSUE {{ .Title }}",
	})
	if err != nil {
		t.Fatalf("ParseGitlabTemplates: %v", err)
	}
	// Exact event key beats the bare kind.
	if msg, _ := gt.Render("issue", "issue.open", gitlabView{Title: "T"}); msg != "OPEN T" {
		t.Errorf("exact key render = %q, want %q", msg, "OPEN T")
	}
	// Bare kind is the fallback for other subtypes.
	if msg, _ := gt.Render("issue", "issue.close", gitlabView{Title: "T"}); msg != "ISSUE T" {
		t.Errorf("bare kind render = %q, want %q", msg, "ISSUE T")
	}
	// Unknown event falls through to the generic default (prints the event key).
	msg, err := gt.Render("wiki_page", "wiki_page", gitlabView{EventKey: "wiki_page", Project: "p"})
	if err != nil {
		t.Fatalf("render default: %v", err)
	}
	if !strings.Contains(msg, "wiki_page") {
		t.Errorf("default render = %q, want it to contain the event key", msg)
	}
}

func TestGitlabTemplates_Override(t *testing.T) {
	// A user entry replaces a built-in default of the same key.
	gt, err := ParseGitlabTemplates(map[string]string{
		"merge_request.open": "custom {{ .Title }}",
	})
	if err != nil {
		t.Fatalf("ParseGitlabTemplates: %v", err)
	}
	msg, _ := gt.Render("merge_request", "merge_request.open", gitlabView{Title: "X"})
	if msg != "custom X" {
		t.Errorf("override render = %q, want %q", msg, "custom X")
	}
}

func TestGitlabTemplates_ParseError(t *testing.T) {
	if _, err := ParseGitlabTemplates(map[string]string{"push": "{{ .Broken "}); err == nil {
		t.Fatal("expected parse error at startup for a malformed template")
	}
}

func TestGitlabTemplates_AmbiguousCatchAllRejected(t *testing.T) {
	// The bare "pipeline" and "pipeline.*" forms canonicalise to the same slot;
	// supplying both must be rejected deterministically at parse time rather than
	// letting nondeterministic map iteration pick a winner.
	_, err := ParseGitlabTemplates(map[string]string{
		"pipeline":   "bare",
		"pipeline.*": "wildcard",
	})
	if err == nil {
		t.Fatal("expected error for equivalent catch-all keys, got nil")
	}
	// A wildcard alone (overriding the built-in bare default) stays valid.
	if _, err := ParseGitlabTemplates(map[string]string{"pipeline.*": "wildcard"}); err != nil {
		t.Fatalf("wildcard-only should be valid: %v", err)
	}
}

func TestGitlabTemplates_DefaultAlwaysPresent(t *testing.T) {
	gt, err := ParseGitlabTemplates(nil)
	if err != nil {
		t.Fatalf("ParseGitlabTemplates(nil): %v", err)
	}
	if gt.def == nil {
		t.Fatal("registry default template is nil")
	}
	// Every built-in key compiles into byKey (minus "default").
	if _, ok := gt.byKey["merge_request.open"]; !ok {
		t.Error("built-in merge_request.open missing from registry")
	}
}

func TestGitlabTemplates_RawAndGet(t *testing.T) {
	gt, err := ParseGitlabTemplates(map[string]string{
		"merge_request.open": `{{ .Raw.object_attributes.title }} @ {{ get .Raw "project.web_url" }}`,
	})
	if err != nil {
		t.Fatalf("ParseGitlabTemplates: %v", err)
	}
	v := normalizeGitlab(mustDecode(t, mrOpenPayload))
	msg, err := gt.Render(v.Kind, v.EventKey, v)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if msg != "Add feature X @ https://gl/myproj" {
		t.Errorf("raw/get render = %q, want %q", msg, "Add feature X @ https://gl/myproj")
	}
}

func TestGitlabDefaultHelper(t *testing.T) {
	fn := gitlabFuncMap()["default"].(func(dflt, val any) any)
	if got := fn("fallback", ""); got != "fallback" {
		t.Errorf("default(fallback, \"\") = %v, want fallback", got)
	}
	if got := fn("fallback", nil); got != "fallback" {
		t.Errorf("default(fallback, nil) = %v, want fallback", got)
	}
	if got := fn("fallback", "value"); got != "value" {
		t.Errorf("default(fallback, value) = %v, want value", got)
	}
	// A non-empty, non-string value (e.g. a number) is not "empty" → returned as-is.
	if got := fn("fallback", 42); got != 42 {
		t.Errorf("default(fallback, 42) = %v, want 42", got)
	}
}

// A kind.* wildcard template key must be selectable at runtime, matching the
// config layer that validates and documents it (same catch-all as bare kind).
// merge_request.update has no exact or bare-kind built-in default, so it falls
// through to the merge_request.* wildcard.
func TestGitlab_TemplateWildcardKey(t *testing.T) {
	const mrUpdatePayload = `{
		"object_kind": "merge_request",
		"object_attributes": {"action": "update", "title": "Add feature X"}
	}`
	gt, err := ParseGitlabTemplates(map[string]string{"merge_request.*": "WILD {{ .Title }}"})
	if err != nil {
		t.Fatalf("ParseGitlabTemplates: %v", err)
	}
	cfg := gitlabTestConfig("secret", "chat1")
	cfg.Senders[0].Templates = gt
	srv, cap := newGitlabTestServer(t, cfg)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrUpdatePayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.last().Message != "WILD Add feature X" {
		t.Errorf("message = %q, want %q (kind.* wildcard template)", cap.last().Message, "WILD Add feature X")
	}
}

// A user "kind.*" wildcard template must override the built-in bare-kind default
// (e.g. the built-in "pipeline"), not be shadowed by it. pipeline.failed has no
// exact user or built-in template, so the user's pipeline.* catch-all must win
// over the built-in bare "pipeline".
func TestGitlab_UserWildcardOverridesBuiltinBareKind(t *testing.T) {
	gt, err := ParseGitlabTemplates(map[string]string{"pipeline.*": "CUSTOM {{ .Action }}"})
	if err != nil {
		t.Fatalf("ParseGitlabTemplates: %v", err)
	}
	cfg := gitlabTestConfig("secret", "chat1")
	cfg.Senders[0].Templates = gt
	srv, cap := newGitlabTestServer(t, cfg)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(pipelinePayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if got := cap.last().Message; got != "CUSTOM failed" {
		t.Errorf("message = %q, want %q (user pipeline.* must override built-in bare pipeline)", got, "CUSTOM failed")
	}
}

func TestGitlab_TemplateOverrideEndpoint(t *testing.T) {
	gt, err := ParseGitlabTemplates(map[string]string{"merge_request.open": "OVR {{ .Title }}"})
	if err != nil {
		t.Fatalf("ParseGitlabTemplates: %v", err)
	}
	cfg := gitlabTestConfig("secret", "chat1")
	cfg.Senders[0].Templates = gt
	srv, cap := newGitlabTestServer(t, cfg)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.last().Message != "OVR Add feature X" {
		t.Errorf("message = %q, want %q", cap.last().Message, "OVR Add feature X")
	}
}

// Runtime template-execution failure (parses fine, errors at Execute) must
// surface as HTTP 400 with no message sent, distinct from parse-time failures.
func TestGitlab_TemplateExecutionError(t *testing.T) {
	// {{ .Title.X }} parses but errors at execution because .Title is a string.
	gt, err := ParseGitlabTemplates(map[string]string{"merge_request.open": "{{ .Title.X }}"})
	if err != nil {
		t.Fatalf("ParseGitlabTemplates: %v", err)
	}
	cfg := gitlabTestConfig("secret", "chat1")
	cfg.Senders[0].Templates = gt
	srv, cap := newGitlabTestServer(t, cfg)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 on template execution error; body: %s", w.Code, w.Body.String())
	}
	if got := cap.last(); got != nil {
		t.Errorf("expected no send on template error, got %+v", got)
	}
}

// --- Task 6: routing fan-out and multi-send ---

// mustCompileRoutes compiles YAML routing rules for the handler fan-out tests.
func mustCompileRoutes(t *testing.T, in []config.GitlabRouteYAMLConfig) []compiledRoute {
	t.Helper()
	routes, err := CompileGitlabRoutes(in)
	if err != nil {
		t.Fatalf("CompileGitlabRoutes: %v", err)
	}
	return routes
}

// newGitlabFanoutServer builds a gitlab server with caller-supplied send and
// chat resolvers so fan-out tests can simulate per-chat success and failure. A
// nil chatFn resolves every alias to itself (ChatID == alias).
func newGitlabFanoutServer(t *testing.T, cfg *GitlabConfig, sendFn SendFunc, chatFn ChatResolver) (*Server, *captureSend) {
	t.Helper()
	for i := range cfg.Senders {
		if cfg.Senders[i].Templates == nil {
			tmpls, err := ParseGitlabTemplates(nil)
			if err != nil {
				t.Fatalf("parse default templates: %v", err)
			}
			cfg.Senders[i].Templates = tmpls
		}
	}
	cap := &captureSend{}
	send := func(ctx context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return sendFn(ctx, p)
	}
	if chatFn == nil {
		chatFn = func(chatID string) (ChatResolveResult, error) {
			return ChatResolveResult{ChatID: chatID}, nil
		}
	}
	srv := New(Config{Listen: ":0", BasePath: "/api/v1"}, send, chatFn, WithGitlab(cfg))
	return srv, cap
}

// okSend is a SendFunc that always succeeds with a fixed sync id.
func okSend(context.Context, *SendPayload) (string, error) { return "sync-1", nil }

// TestGitlab_QueryChatBypassesRoutes: an explicit ?chat_id overrides the routing
// engine entirely, delivering to that single chat with the original response.
func TestGitlab_QueryChatBypassesRoutes(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"myproj"}}, Chats: []string{"chatA", "chatB"}},
	})
	cfg := gitlabTestConfig("secret", "override")
	cfg.Senders[0].Routes = routes
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=override", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1 (routes bypassed)", cap.count())
	}
	if cap.last().ChatID != "override" {
		t.Errorf("chat = %q, want override", cap.last().ChatID)
	}
	// Single-chat path returns the uniform MultiSendResponse (results[0].sync_id).
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || resp.Results[0].Chat != "override" || resp.Results[0].SyncID != "sync-1" {
		t.Errorf("response = %+v, want ok with single override->sync-1 result", resp)
	}
}

// TestGitlab_FanoutTwoChats: a matching rule fans the event out to both chats.
func TestGitlab_FanoutTwoChats(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"myproj"}}, Chats: []string{"chatA", "chatB"}},
	})
	cfg := gitlabTestConfig("secret")
	cfg.Senders[0].Routes = routes
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 2 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v, want ok with 2 results, 0 errors", resp)
	}
	got := map[string]string{}
	for _, r := range resp.Results {
		got[r.Chat] = r.SyncID
	}
	if got["chatA"] != "sync-1" || got["chatB"] != "sync-1" {
		t.Errorf("results = %+v, want chatA/chatB -> sync-1", resp.Results)
	}
}

// TestGitlab_FanoutNamespacedProject: the reserved `project` selector must match
// against project.path_with_namespace (real GitLab sends a namespaced path), so a
// namespaced glob like "group/backend/*" routes correctly end-to-end through
// normalizeGitlab. This guards the decoupling between routing (namespace) and the
// template `.Project` field (short name for display).
func TestGitlab_FanoutNamespacedProject(t *testing.T) {
	const payload = `{
		"object_kind": "merge_request",
		"user": {"name": "Alice", "username": "alice"},
		"project": {"name": "api", "path_with_namespace": "group/backend/api", "web_url": "https://gl/group/backend/api"},
		"object_attributes": {
			"action": "open",
			"title": "Add feature X",
			"url": "https://gl/group/backend/api/-/merge_requests/1",
			"source_branch": "feature-x",
			"target_branch": "main"
		}
	}`
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		// Short-name glob must NOT match a namespaced path (belt-and-braces).
		{Match: map[string][]string{"project": {"api"}}, Chats: []string{"wrong"}},
		{Match: map[string][]string{"project": {"group/backend/*"}}, Chats: []string{"backend-mrs"}},
	})
	cfg := gitlabTestConfig("secret")
	cfg.Senders[0].Routes = routes
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(payload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1 (only the namespaced rule matches)", cap.count())
	}
	if got := cap.last().ChatID; got != "backend-mrs" {
		t.Errorf("chat = %q, want backend-mrs (namespaced glob matched path_with_namespace)", got)
	}
	// Template .Project stays the short name even though routing used the path.
	if got := cap.last().Message; !strings.Contains(got, "[api]") {
		t.Errorf("message = %q, want it to render the short project name [api]", got)
	}
}

// TestGitlab_FanoutErrorStatus: the error_events status mapping must apply on the
// multi-send path too, not only the single-chat path — every fanned-out delivery
// of a matching event carries status "error".
func TestGitlab_FanoutErrorStatus(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"kind": {"pipeline"}}, Chats: []string{"chatA", "chatB"}},
	})
	cfg := gitlabTestConfig("secret")
	cfg.Senders[0].Routes = routes
	cfg.Senders[0].ErrorEvents = []string{"pipeline.failed"}
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(pipelinePayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2", cap.count())
	}
	for _, p := range cap.calls {
		if p.Status != "error" {
			t.Errorf("chat %q status = %q, want error", p.ChatID, p.Status)
		}
	}
}

// TestGitlab_FanoutPartialFailure: one chat fails; the response is 200 with the
// surviving result and the failure listed in errors.
func TestGitlab_FanoutPartialFailure(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"myproj"}}, Chats: []string{"chatA", "chatB"}},
	})
	sendFn := func(_ context.Context, p *SendPayload) (string, error) {
		if p.ChatID == "chatB" {
			return "", fmt.Errorf("botx unavailable")
		}
		return "sync-1", nil
	}
	cfg := gitlabTestConfig("secret")
	cfg.Senders[0].Routes = routes
	srv, cap := newGitlabFanoutServer(t, cfg, sendFn, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (partial success); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both attempted)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 1 {
		t.Fatalf("response = %+v, want ok with 1 result, 1 error", resp)
	}
	if resp.Results[0].Chat != "chatA" || resp.Errors[0].Chat != "chatB" {
		t.Errorf("result/error chats = %q/%q, want chatA/chatB", resp.Results[0].Chat, resp.Errors[0].Chat)
	}
}

// TestGitlab_FanoutAllFail: every delivery fails -> 502 with all errors listed.
func TestGitlab_FanoutAllFail(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"myproj"}}, Chats: []string{"chatA", "chatB"}},
	})
	sendFn := func(context.Context, *SendPayload) (string, error) {
		return "", fmt.Errorf("botx unavailable")
	}
	cfg := gitlabTestConfig("secret")
	cfg.Senders[0].Routes = routes
	srv, cap := newGitlabFanoutServer(t, cfg, sendFn, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (all failed); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both attempted)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if resp.OK || len(resp.Results) != 0 || len(resp.Errors) != 2 {
		t.Errorf("response = %+v, want not-ok with 0 results, 2 errors", resp)
	}
}

// TestGitlab_NoRouteMatchFallsBackToDefault: when no rule matches, the event is
// delivered to the configured default chat rather than dropped.

// TestGitlab_NoRouteMatchNoDefaultIgnored: no rule matches and no default chat
// is configured -> the event is ignored (200) rather than delivered or errored.
func TestGitlab_NoRouteMatchNoDefaultIgnored(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"otherproj"}}, Chats: []string{"chatA"}},
	})
	cfg := gitlabTestConfig("secret")
	cfg.Senders[0].Routes = routes
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Errorf("send count = %d, want 0 (no match, no default)", cap.count())
	}
	var resp gitlabIgnoredResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || !resp.Ignored || resp.Event != "merge_request.open" {
		t.Errorf("response = %+v, want ok/ignored merge_request.open", resp)
	}
}

// TestGitlab_FanoutChatResolveError: a chat that fails to resolve is reported as
// a per-chat error while the other chat still delivers (best-effort fan-out).
func TestGitlab_FanoutChatResolveError(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"myproj"}}, Chats: []string{"chatA", "bad-alias"}},
	})
	chatFn := func(chatID string) (ChatResolveResult, error) {
		if chatID == "bad-alias" {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	cfg := gitlabTestConfig("secret")
	cfg.Senders[0].Routes = routes
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, chatFn)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (one chat still delivers); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1 (only the resolvable chat)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || resp.Results[0].Chat != "chatA" || len(resp.Errors) != 1 || resp.Errors[0].Chat != "bad-alias" {
		t.Errorf("response = %+v, want chatA result + bad-alias error", resp)
	}
}

// --- Task 3 (per-sender tokens): isolated send path for a matched sender ---

// senderTestConfig returns one two-chat sender runtime.
func senderTestConfig() *GitlabConfig {
	return &GitlabConfig{Senders: []GitlabSender{
		gitlabTestSender("team-a-token", "team-a-chat1", "team-a-chat2"),
	}}
}

// TestGitlab_SenderFanout: an event authenticated with a sender token is fanned
// out to exactly that sender's chats and nothing else.
func TestGitlab_SenderFanout(t *testing.T) {
	srv, cap := newGitlabFanoutServer(t, senderTestConfig(), okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both sender chats)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 2 || len(resp.Errors) != 0 {
		t.Fatalf("response = %+v, want ok with 2 results, 0 errors", resp)
	}
	got := map[string]bool{}
	for _, r := range resp.Results {
		got[r.Chat] = true
	}
	if !got["team-a-chat1"] || !got["team-a-chat2"] {
		t.Errorf("results = %+v, want team-a-chat1 and team-a-chat2", resp.Results)
	}
}

// TestGitlab_SenderChatFilterSubset: ?chat_id listing a subset of the sender's
// own chats delivers to exactly that subset, not the full scope.
func TestGitlab_SenderChatFilterSubset(t *testing.T) {
	srv, cap := newGitlabFanoutServer(t, senderTestConfig(), okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=team-a-chat2", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1 (filtered to the requested chat)", cap.count())
	}
	if got := cap.last().ChatID; got != "team-a-chat2" {
		t.Errorf("chat = %q, want team-a-chat2", got)
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || resp.Results[0].Chat != "team-a-chat2" {
		t.Errorf("response = %+v, want single team-a-chat2 result", resp)
	}
}

// TestGitlab_SenderChatFilterOutOfScope: ?chat_id naming a chat the sender is not
// allowed to reach is refused with 403 and delivers nothing (scope breakout).
func TestGitlab_SenderChatFilterOutOfScope(t *testing.T) {
	srv, cap := newGitlabFanoutServer(t, senderTestConfig(), okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=chat1", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403 (out-of-scope chat); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Fatalf("send count = %d, want 0 (nothing sent on scope violation)", cap.count())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if ok, _ := resp["ok"].(bool); ok || !strings.Contains(fmt.Sprint(resp["error"]), "chat1") {
		t.Errorf("response = %+v, want ok:false with an error naming chat1", resp)
	}
}

// TestGitlab_SenderChatFilterPartialOutOfScope: one allowed + one foreign chat in
// ?chat_id is refused wholesale (403), delivering to neither.
func TestGitlab_SenderChatFilterPartialOutOfScope(t *testing.T) {
	srv, cap := newGitlabFanoutServer(t, senderTestConfig(), okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=team-a-chat1,chat1", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403 (partial out-of-scope); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Fatalf("send count = %d, want 0 (all-or-nothing on scope violation)", cap.count())
	}
}

// TestGitlab_SenderChatFilterEmpty: an explicitly-present but empty ?chat_id is a
// 400 request error, not a silent fall-back to the full sender scope.
func TestGitlab_SenderChatFilterEmpty(t *testing.T) {
	for _, q := range []string{"?chat_id=", "?chat_id=,,", "?chat_id=+,+"} {
		t.Run(q, func(t *testing.T) {
			srv, cap := newGitlabFanoutServer(t, senderTestConfig(), okSend, nil)
			w := doRequest(srv, "POST", "/api/v1/gitlab"+q, strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
			if w.Code != 400 {
				t.Fatalf("status = %d, want 400 (empty chat_id); body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 0 {
				t.Fatalf("send count = %d, want 0 (nothing sent on empty chat_id)", cap.count())
			}
		})
	}
}

// TestGitlab_SenderChatFilterAliasUUID: alias↔UUID equivalence — a sender scoped
// by alias accepts the UUID it resolves to (and vice versa) in ?chat_id.
func TestGitlab_SenderChatFilterAliasUUID(t *testing.T) {
	aliases := map[string]string{"team-a-alerts": "uuid-a", "team-a-dev": "uuid-b"}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		if u, ok := aliases[chatID]; ok {
			return ChatResolveResult{ChatID: u}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}

	t.Run("UUID request against alias scope", func(t *testing.T) {
		cfg := &GitlabConfig{
			Senders: []GitlabSender{{
				Secret:  "team-a-token",
				Scope:   map[string]GitlabTarget{"uuid-a": {Target: "team-a-alerts"}, "uuid-b": {Target: "team-a-dev"}},
				Targets: []string{"team-a-alerts", "team-a-dev"},
			}},
		}
		srv, cap := newGitlabFanoutServer(t, cfg, okSend, chatFn)
		w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=uuid-a", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200 (UUID matches alias scope); body: %s", w.Code, w.Body.String())
		}
		if cap.count() != 1 || cap.last().ChatID != "uuid-a" {
			t.Fatalf("sends = %d, last = %q, want 1 to uuid-a", cap.count(), cap.last().ChatID)
		}
	})

	t.Run("alias request against UUID scope", func(t *testing.T) {
		cfg := &GitlabConfig{
			Senders: []GitlabSender{{
				Secret:  "team-a-token",
				Scope:   map[string]GitlabTarget{"uuid-a": {Target: "uuid-a"}, "uuid-b": {Target: "uuid-b"}},
				Targets: []string{"uuid-a", "uuid-b"},
			}},
		}
		srv, cap := newGitlabFanoutServer(t, cfg, okSend, chatFn)
		w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=team-a-alerts", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200 (alias matches UUID scope); body: %s", w.Code, w.Body.String())
		}
		// gitlabDeliver resolves each target alias to its UUID before sending, so
		// the recorded chat is the resolved uuid-a even though ?chat_id used the
		// alias.
		if cap.count() != 1 || cap.last().ChatID != "uuid-a" {
			t.Fatalf("sends = %d, last = %q, want 1 to uuid-a", cap.count(), cap.last().ChatID)
		}
	})
}

// TestGitlab_SenderChatFilterMultiSubset: ?chat_id naming several in-scope chats
// fans out to exactly those chats, in request order, leaving the rest untouched.
func TestGitlab_SenderChatFilterMultiSubset(t *testing.T) {
	cfg := &GitlabConfig{Senders: []GitlabSender{
		gitlabTestSender("team-a-token", "team-a-c1", "team-a-c2", "team-a-c3"),
	}}
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=team-a-c1,team-a-c3", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (only the requested subset)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	got := []string{resp.Results[0].Chat, resp.Results[1].Chat}
	if !slices.Equal(got, []string{"team-a-c1", "team-a-c3"}) {
		t.Errorf("results = %v, want [team-a-c1 team-a-c3] in request order", got)
	}
}

// TestGitlab_SenderChatFilterUUIDMultiBot: in multi-bot mode a sender scoped by a
// bot-bound alias may name that chat by the UUID it resolves to; the request is
// canonicalised back to the configured alias so its bot binding is preserved and
// delivery succeeds (rather than failing with "bot is required").
func TestGitlab_SenderChatFilterUUIDMultiBot(t *testing.T) {
	cfg := &GitlabConfig{
		Senders: []GitlabSender{{
			Secret: "team-a-token", Scope: map[string]GitlabTarget{"uuid-a": {Target: "team-a-alerts", Bot: "bot-a"}}, Targets: []string{"team-a-alerts"},
		}},
	}
	tmpls, err := ParseGitlabTemplates(nil)
	if err != nil {
		t.Fatalf("parse default templates: %v", err)
	}
	cfg.Senders[0].Templates = tmpls
	cap := &captureSend{}
	send := func(ctx context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-1", nil
	}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		if chatID == "team-a-alerts" {
			return ChatResolveResult{ChatID: "uuid-a", Bot: "bot-a"}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(Config{Listen: ":0", BasePath: "/api/v1", BotNames: []string{"bot-a", "bot-b"}}, send, chatFn, WithGitlab(cfg))

	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=uuid-a", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (UUID canonicalises to bound alias); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 || cap.last().ChatID != "uuid-a" || cap.last().Bot != "bot-a" {
		t.Fatalf("send = %+v, want single delivery to uuid-a via bot-a", cap.last())
	}
}

// TestGitlab_SenderFilterApplies: sender-local filters drop matching events.
func TestGitlab_SenderFilterApplies(t *testing.T) {
	cfg := senderTestConfig()
	cfg.Senders[0].Only = []string{"pipeline"}
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Fatalf("send count = %d, want 0 (filtered out)", cap.count())
	}
	var resp gitlabIgnoredResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || !resp.Ignored || resp.Event != "merge_request.open" {
		t.Errorf("response = %+v, want ok/ignored merge_request.open", resp)
	}
}

// TestGitlab_SenderErrorStatus: the error_events status mapping applies on the
// sender path too — matching events deliver with status "error" to sender chats.
func TestGitlab_SenderErrorStatus(t *testing.T) {
	cfg := senderTestConfig()
	cfg.Senders[0].ErrorEvents = []string{"pipeline.failed"}
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(pipelinePayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2", cap.count())
	}
	for _, p := range cap.calls {
		if p.Status != "error" {
			t.Errorf("chat %q status = %q, want error", p.ChatID, p.Status)
		}
	}
}

// TestGitlab_SenderSingleChat: a sender configured with exactly one chat
// delivers once, still via the fan-out response shape.
func TestGitlab_SenderSingleChat(t *testing.T) {
	cfg := &GitlabConfig{Senders: []GitlabSender{gitlabTestSender("team-b-token", "team-b-chat")}}
	srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("team-b-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1", cap.count())
	}
	if got := cap.last().ChatID; got != "team-b-chat" {
		t.Errorf("chat = %q, want team-b-chat", got)
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || resp.Results[0].Chat != "team-b-chat" {
		t.Errorf("response = %+v, want single team-b-chat result", resp)
	}
}

// TestGitlab_SenderIgnoresQueryBot: ?bot= must not apply on the sender path — a
// sender token cannot pick another configured bot's identity. (In this harness
// an honoured ?bot=other would fail every delivery with "bot not available".)
func TestGitlab_SenderIgnoresQueryBot(t *testing.T) {
	srv, cap := newGitlabFanoutServer(t, senderTestConfig(), okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab?bot=other", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (?bot ignored for senders); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both sender chats)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Errors) != 0 {
		t.Errorf("response = %+v, want ok without errors", resp)
	}
}

// TestGitlab_SenderFanoutAllFail: every sender-chat delivery fails -> the
// documented 502 fan-out response with all errors, same as the routes path.
func TestGitlab_SenderFanoutAllFail(t *testing.T) {
	sendFn := func(context.Context, *SendPayload) (string, error) {
		return "", fmt.Errorf("botx unavailable")
	}
	srv, cap := newGitlabFanoutServer(t, senderTestConfig(), sendFn, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (all failed); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both attempted)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if resp.OK || len(resp.Results) != 0 || len(resp.Errors) != 2 {
		t.Errorf("response = %+v, want not-ok with 0 results, 2 errors", resp)
	}
}

// TestGitlab_SenderFanoutPartialFailure: one sender chat fails -> 200 with the
// surviving result plus the per-chat error, same contract as the routes path.
func TestGitlab_SenderFanoutPartialFailure(t *testing.T) {
	sendFn := func(_ context.Context, p *SendPayload) (string, error) {
		if p.ChatID == "team-a-chat2" {
			return "", fmt.Errorf("botx unavailable")
		}
		return "sync-1", nil
	}
	srv, cap := newGitlabFanoutServer(t, senderTestConfig(), sendFn, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (partial success); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both attempted)", cap.count())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 1 {
		t.Fatalf("response = %+v, want ok with 1 result, 1 error", resp)
	}
	if resp.Results[0].Chat != "team-a-chat1" || resp.Errors[0].Chat != "team-a-chat2" {
		t.Errorf("result/error chats = %q/%q, want team-a-chat1/team-a-chat2", resp.Results[0].Chat, resp.Errors[0].Chat)
	}
}

// TestGitlab_SenderMultiBot: in multi-bot mode a sender request always passes
// requestBot="" (?bot= is ignored, see TestGitlab_SenderIgnoresQueryBot), so
// resolveRequestBot can only get a bot from the chat's own binding. A chat
// bound to a bot resolves and delivers; an unbound chat (or a raw UUID, which
// can never carry a binding) fails every delivery with "bot is required".
// buildGitlabConfig rejects the unbound case at startup (see
// TestBuildGitlabConfig_MultiBot_RejectsUnboundSenderAlias /
// _RejectsUUIDSenderChat in internal/cmd) — this test pins the runtime
// behavior those startup checks exist to prevent.
func TestGitlab_SenderMultiBot(t *testing.T) {
	cfg := &GitlabConfig{
		Senders: []GitlabSender{
			{
				Secret: "team-a-token",
				Scope: map[string]GitlabTarget{
					"team-a-bound": {Target: "team-a-bound"}, "team-a-unbound": {Target: "team-a-unbound"},
				},
				Targets: []string{"team-a-bound", "team-a-unbound"},
			},
		},
	}
	tmpls, err := ParseGitlabTemplates(nil)
	if err != nil {
		t.Fatalf("parse default templates: %v", err)
	}
	cfg.Senders[0].Templates = tmpls

	cap := &captureSend{}
	send := func(ctx context.Context, p *SendPayload) (string, error) {
		cap.record(p)
		return "sync-1", nil
	}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		if chatID == "team-a-bound" {
			return ChatResolveResult{ChatID: chatID, Bot: "bot-a"}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(Config{Listen: ":0", BasePath: "/api/v1", BotNames: []string{"bot-a", "bot-b"}}, send, chatFn, WithGitlab(cfg))

	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("team-a-token"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (partial success — bound chat delivers); body: %s", w.Code, w.Body.String())
	}
	var resp MultiSendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if len(resp.Results) != 1 || resp.Results[0].Chat != "team-a-bound" {
		t.Errorf("results = %+v, want single result for team-a-bound", resp.Results)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Chat != "team-a-unbound" || !strings.Contains(resp.Errors[0].Error, "bot is required") {
		t.Errorf("errors = %+v, want team-a-unbound failing with \"bot is required\"", resp.Errors)
	}
	if cap.count() != 1 || cap.last().Bot != "bot-a" {
		t.Errorf("send Bot = %+v, want single send with Bot=bot-a", cap.last())
	}
}

// TestGitlab_EmptyChatIDIsRequestError: an explicit-but-empty ?chat_id (?chat_id=
// or ?chat_id=,,) is a 400 request error, not a silent fall-through to the routing
// engine or default chat.
func TestGitlab_EmptyChatIDIsRequestError(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"myproj"}}, Chats: []string{"chatA"}},
	})
	for _, q := range []string{"?chat_id=", "?chat_id=,,"} {
		t.Run(q, func(t *testing.T) {
			cfg := gitlabTestConfig("secret")
			cfg.Senders[0].Routes = routes
			srv, cap := newGitlabFanoutServer(t, cfg, okSend, nil)
			w := doRequest(srv, "POST", "/api/v1/gitlab"+q, strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
			if w.Code != 400 {
				t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
			}
			if cap.count() != 0 {
				t.Errorf("send count = %d, want 0 (must not fall through to routes)", cap.count())
			}
		})
	}
}
