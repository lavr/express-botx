package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// newGitlabTestServer builds a server with the gitlab endpoint enabled and a
// send function that records the last SendPayload it received.
func newGitlabTestServer(t *testing.T, cfg *GitlabConfig) (*Server, *captureSend) {
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
		return "sync-1", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if chatID == "unknown-alias" {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(Config{Listen: ":0", BasePath: "/api/v1"}, sendFn, chatResolver, WithGitlab(cfg))
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
	noteSystemPayload = `{
		"object_kind": "note",
		"user": {"name": "Carol"},
		"object_attributes": {"note": "changed the description", "noteable_type": "MergeRequest", "system": true}
	}`
	noteCommitPayload = `{
		"object_kind": "note",
		"user": {"name": "Carol"},
		"object_attributes": {"note": "nice commit", "noteable_type": "Commit", "system": false}
	}`
)

func gitlabHeaders(token string) map[string]string {
	return map[string]string{
		"Content-Type":   "application/json",
		"X-Gitlab-Token": token,
	}
}

func TestGitlab_Open(t *testing.T) {
	srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1", cap.count())
	}
	msg := cap.last().Message
	for _, want := range []string{"Новый MR", "Add feature X", "Alice", "feature-x -> main", "mergeable"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if cap.last().ChatID != "chat1" {
		t.Errorf("chat = %q, want chat1", cap.last().ChatID)
	}
}

func TestGitlab_Merge(t *testing.T) {
	srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrMergePayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1", cap.count())
	}
	msg := cap.last().Message
	for _, want := range []string{"MR слит", "Успешно слито", "Add feature X"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestGitlab_Comment(t *testing.T) {
	srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(noteCommentPayload), gitlabHeaders("secret"))
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if cap.count() != 1 {
		t.Fatalf("send count = %d, want 1", cap.count())
	}
	msg := cap.last().Message
	for _, want := range []string{"Комментарий в MR", "Carol", "Looks good to me", "Add feature X"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestGitlab_IgnoredEvents(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"mr_update", mrUpdatePayload},
		{"note_system", noteSystemPayload},
		{"note_commit", noteCommitPayload},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cap := newGitlabTestServer(t, &GitlabConfig{DefaultChatID: "chat1", SecretToken: "secret"})
			w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(tc.payload), gitlabHeaders("secret"))
			if w.Code != 200 {
				t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 0 {
				t.Errorf("send count = %d, want 0 (event should be ignored)", cap.count())
			}
			if !strings.Contains(w.Body.String(), "ignored") {
				t.Errorf("body missing ignored marker: %s", w.Body.String())
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

func TestGitlab_NotRegisteredWithoutConfig(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), map[string]string{"X-Gitlab-Token": "secret"})
	if w.Code == 200 {
		t.Fatalf("expected non-200 when gitlab not configured, got %d", w.Code)
	}
}
