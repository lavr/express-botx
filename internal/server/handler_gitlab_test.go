package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lavr/express-botx/internal/config"
)

// newGitlabTestServer builds a server with the gitlab endpoint enabled and a
// send function that records the last SendPayload it received.
func newGitlabTestServer(t *testing.T, cfg *GitlabConfig) (*Server, *captureSend) {
	return newGitlabTestServerCfg(t, Config{Listen: ":0", BasePath: "/api/v1"}, cfg, nil)
}

// newGitlabTestServerCfg is like newGitlabTestServer but lets the caller control
// the server Config (e.g. DefaultChatAlias) and inject a send error.
func newGitlabTestServerCfg(t *testing.T, srvCfg Config, cfg *GitlabConfig, sendErr error) (*Server, *captureSend) {
	t.Helper()
	if cfg.Templates == nil {
		tmpls, err := ParseGitlabTemplates(nil)
		if err != nil {
			t.Fatalf("parse default templates: %v", err)
		}
		cfg.Templates = tmpls
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
			srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
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
			srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
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
	srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
	w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=chat2", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.last().ChatID != "chat2" {
		t.Errorf("chat = %q, want chat2 (override)", cap.last().ChatID)
	}
}

func TestGitlab_ChatResolveError(t *testing.T) {
	srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "unknown-alias", SecretToken: "secret"})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Errorf("send count = %d, want 0", cap.count())
	}
}

func TestGitlab_MissingChatID(t *testing.T) {
	srv, cap := newGitlabTestServer(t, &GitlabConfig{SecretToken: "secret"})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if cap.count() != 0 {
		t.Errorf("send count = %d, want 0", cap.count())
	}
}

func TestGitlab_InvalidJSON(t *testing.T) {
	srv, _ := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader("{not json"), gitlabHeaders("secret"))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
}

func TestGitlab_AuthorUsernameFallback(t *testing.T) {
	srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
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
		&GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"}, fmt.Errorf("botx unavailable"))
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (body: %s)", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Errorf("send count = %d, want 1 (send attempted then failed)", cap.count())
	}
}

func TestGitlab_ChatResolutionFallbacks(t *testing.T) {
	t.Run("global_default_chat", func(t *testing.T) {
		srv, cap := newGitlabTestServerCfg(t, Config{Listen: ":0", BasePath: "/api/v1", DefaultChatAlias: "globchat"},
			&GitlabConfig{SecretToken: "secret"}, nil)
		w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		if w.Code != 200 {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		if cap.last().ChatID != "globchat" {
			t.Errorf("chat = %q, want globchat", cap.last().ChatID)
		}
	})
	t.Run("single_chat_fallback", func(t *testing.T) {
		srv, cap := newGitlabTestServer(t, &GitlabConfig{SecretToken: "secret", FallbackChatID: "onlychat"})
		w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		if w.Code != 200 {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		if cap.last().ChatID != "onlychat" {
			t.Errorf("chat = %q, want onlychat", cap.last().ChatID)
		}
	})
	t.Run("query_overrides_default_chat", func(t *testing.T) {
		srv, cap := newGitlabTestServerCfg(t, Config{Listen: ":0", BasePath: "/api/v1", DefaultChatAlias: "globchat"},
			&GitlabConfig{SecretToken: "secret", FallbackChatID: "onlychat"}, nil)
		w := doRequest(srv, "POST", "/api/v1/gitlab?chat_id=override", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		if w.Code != 200 {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		if cap.last().ChatID != "override" {
			t.Errorf("chat = %q, want override", cap.last().ChatID)
		}
	})
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
		srv, cap := newGitlabTestServer(t, &GitlabConfig{
			DefaultChatID: "chat1", SecretToken: "secret",
			Exclude: []string{"merge_request.update"},
		})
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
		srv, cap := newGitlabTestServer(t, &GitlabConfig{
			DefaultChatID: "chat1", SecretToken: "secret",
			Only: []string{"pipeline.*"},
		})
		w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		if w.Code != 200 {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		if cap.count() != 0 {
			t.Errorf("send count = %d, want 0 (not in only)", cap.count())
		}
	})
	t.Run("only_listed_passes", func(t *testing.T) {
		srv, cap := newGitlabTestServer(t, &GitlabConfig{
			DefaultChatID: "chat1", SecretToken: "secret",
			Only: []string{"merge_request.*"},
		})
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
	srv, cap := newGitlabTestServer(t, &GitlabConfig{
		DefaultChatID: "chat1", SecretToken: "secret",
	})
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
			srv, cap := newGitlabTestServer(t, &GitlabConfig{
				DefaultChatID: "chat1", SecretToken: "secret",
				ErrorEvents: tc.errorEvents,
			})
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
	srv, cap := newGitlabTestServer(t, &GitlabConfig{
		DefaultChatID: "chat1", SecretToken: "secret", Templates: gt,
	})
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
	srv, cap := newGitlabTestServer(t, &GitlabConfig{
		DefaultChatID: "chat1", SecretToken: "secret", Templates: gt,
	})
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
	srv, cap := newGitlabTestServer(t, &GitlabConfig{
		DefaultChatID: "chat1", SecretToken: "secret", Templates: gt,
	})
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
	srv, cap := newGitlabTestServer(t, &GitlabConfig{
		DefaultChatID: "chat1", SecretToken: "secret", Templates: gt,
	})
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
	if cfg.Templates == nil {
		tmpls, err := ParseGitlabTemplates(nil)
		if err != nil {
			t.Fatalf("parse default templates: %v", err)
		}
		cfg.Templates = tmpls
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
	srv, cap := newGitlabFanoutServer(t, &GitlabConfig{SecretToken: "secret", Routes: routes}, okSend, nil)
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
	// Single-chat path keeps the plain sendResponse shape (sync_id, no results).
	var resp sendResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || resp.SyncID != "sync-1" {
		t.Errorf("response = %+v, want ok/sync-1", resp)
	}
}

// TestGitlab_FanoutTwoChats: a matching rule fans the event out to both chats.
func TestGitlab_FanoutTwoChats(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"myproj"}}, Chats: []string{"chatA", "chatB"}},
	})
	srv, cap := newGitlabFanoutServer(t, &GitlabConfig{SecretToken: "secret", Routes: routes}, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2", cap.count())
	}
	var resp gitlabFanoutResponse
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
	srv, cap := newGitlabFanoutServer(t, &GitlabConfig{SecretToken: "secret", Routes: routes}, sendFn, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (partial success); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both attempted)", cap.count())
	}
	var resp gitlabFanoutResponse
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
	srv, cap := newGitlabFanoutServer(t, &GitlabConfig{SecretToken: "secret", Routes: routes}, sendFn, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502 (all failed); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 2 {
		t.Fatalf("send count = %d, want 2 (both attempted)", cap.count())
	}
	var resp gitlabFanoutResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if resp.OK || len(resp.Results) != 0 || len(resp.Errors) != 2 {
		t.Errorf("response = %+v, want not-ok with 0 results, 2 errors", resp)
	}
}

// TestGitlab_NoRouteMatchFallsBackToDefault: when no rule matches, the event is
// delivered to the configured default chat rather than dropped.
func TestGitlab_NoRouteMatchFallsBackToDefault(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"otherproj"}}, Chats: []string{"chatA"}},
	})
	srv, cap := newGitlabFanoutServer(t, &GitlabConfig{SecretToken: "secret", DefaultChatID: "dflt", Routes: routes}, okSend, nil)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 || cap.last().ChatID != "dflt" {
		t.Fatalf("send count = %d chat = %q, want 1 to dflt", cap.count(), func() string {
			if cap.last() == nil {
				return ""
			}
			return cap.last().ChatID
		}())
	}
	var resp gitlabFanoutResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || resp.Results[0].Chat != "dflt" {
		t.Errorf("response = %+v, want ok with single dflt result", resp)
	}
}

// TestGitlab_NoRouteMatchNoDefaultIgnored: no rule matches and no default chat
// is configured -> the event is ignored (200) rather than delivered or errored.
func TestGitlab_NoRouteMatchNoDefaultIgnored(t *testing.T) {
	routes := mustCompileRoutes(t, []config.GitlabRouteYAMLConfig{
		{Match: map[string][]string{"project": {"otherproj"}}, Chats: []string{"chatA"}},
	})
	srv, cap := newGitlabFanoutServer(t, &GitlabConfig{SecretToken: "secret", Routes: routes}, okSend, nil)
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
	srv, cap := newGitlabFanoutServer(t, &GitlabConfig{SecretToken: "secret", Routes: routes}, okSend, chatFn)
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (one chat still delivers); body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1 (only the resolvable chat)", cap.count())
	}
	var resp gitlabFanoutResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !resp.OK || len(resp.Results) != 1 || resp.Results[0].Chat != "chatA" || len(resp.Errors) != 1 || resp.Errors[0].Chat != "bad-alias" {
		t.Errorf("response = %+v, want chatA result + bad-alias error", resp)
	}
}
