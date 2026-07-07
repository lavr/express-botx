package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
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
	if cfg.Template == nil {
		tmpl, err := ParseGitlabTemplate(DefaultGitlabTemplate)
		if err != nil {
			t.Fatalf("parse default template: %v", err)
		}
		cfg.Template = tmpl
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
		{"mr_open", mrOpenPayload, []string{"merge_request.open", "myproj", "Add feature X", "Alice"}},
		{"mr_merge", mrMergePayload, []string{"merge_request.merge", "Add feature X", "Bob"}},
		{"note", noteCommentPayload, []string{"note.MergeRequest", "Carol"}},
		{"push", pushPayload, []string{"push", "myproj", "Erin"}},
		{"pipeline", pipelinePayload, []string{"pipeline.failed", "Frank"}},
		{"job", jobPayload, []string{"build.success", "Grace"}},
		{"issue", issuePayload, []string{"issue.open", "Something broke", "Heidi"}},
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

func TestGitlab_NotRegisteredWithoutConfig(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), map[string]string{"X-Gitlab-Token": "secret"})
	if w.Code == 200 {
		t.Fatalf("expected non-200 when gitlab not configured, got %d", w.Code)
	}
}
