package server

import (
	"context"
	"strings"
	"testing"
)

func newGitLabTestServer(t *testing.T, glCfg *GitLabConfig) (*Server, *[]SendPayload) {
	t.Helper()
	sent := []SendPayload{}
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		sent = append(sent, *p)
		return "gitlab-sync-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: "resolved-" + chatID}, nil
	}
	return New(cfg, sendFn, chatResolver, WithGitLab(glCfg)), &sent
}

func gitLabHeaders(event, token string) map[string]string {
	return map[string]string{
		"Content-Type":   "application/json",
		"X-Gitlab-Event": event,
		"X-Gitlab-Token": token,
	}
}

func TestGitLab_PushProjectRouting(t *testing.T) {
	srv, sent := newGitLabTestServer(t, &GitLabConfig{
		Token: "secret",
		Projects: map[string]string{
			"group/service": "service-chat",
		},
	})

	body := `{
		"object_kind": "push",
		"ref": "refs/heads/main",
		"after": "1234567890abcdef",
		"user_name": "Ivan",
		"project": {
			"name": "service",
			"path_with_namespace": "group/service",
			"web_url": "https://gitlab.example.com/group/service"
		},
		"total_commits_count": 2,
		"commits": [
			{"id":"1234567890abcdef","message":"first commit\nbody","author":{"name":"Ivan"}},
			{"id":"abcdef1234567890","message":"second commit","author":{"name":"Petr"}}
		]
	}`

	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(body), gitLabHeaders("Push Hook", "secret"))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 sent payload, got %d", len(*sent))
	}
	got := (*sent)[0]
	if got.ChatID != "resolved-service-chat" {
		t.Fatalf("ChatID = %q, want resolved-service-chat", got.ChatID)
	}
	if got.Status != "ok" {
		t.Fatalf("Status = %q, want ok", got.Status)
	}
	if !strings.Contains(got.Message, "GitLab push to group/service/main") {
		t.Fatalf("unexpected message: %s", got.Message)
	}
	if len(got.Bubble) != 1 || len(got.Bubble[0]) != 1 {
		t.Fatalf("expected GitLab button, got %#v", got.Bubble)
	}
}

func TestGitLab_InvalidToken(t *testing.T) {
	srv, sent := newGitLabTestServer(t, &GitLabConfig{
		Token:         "secret",
		DefaultChatID: "gitlab-chat",
	})

	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(`{}`), gitLabHeaders("Push Hook", "wrong"))
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if len(*sent) != 0 {
		t.Fatalf("expected no sent payloads, got %d", len(*sent))
	}
}

func TestGitLab_PipelineFailedStatus(t *testing.T) {
	srv, sent := newGitLabTestServer(t, &GitLabConfig{
		Token:         "secret",
		DefaultChatID: "gitlab-chat",
	})

	body := `{
		"object_kind": "pipeline",
		"user": {"name": "Ivan"},
		"project": {
			"name": "service",
			"path_with_namespace": "group/service",
			"web_url": "https://gitlab.example.com/group/service"
		},
		"object_attributes": {
			"id": 42,
			"ref": "main",
			"status": "failed",
			"duration": 61,
			"url": "https://gitlab.example.com/group/service/-/pipelines/42"
		}
	}`

	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(body), gitLabHeaders("Pipeline Hook", "secret"))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(*sent) != 1 {
		t.Fatalf("expected 1 sent payload, got %d", len(*sent))
	}
	got := (*sent)[0]
	if got.Status != "error" {
		t.Fatalf("Status = %q, want error", got.Status)
	}
	if !strings.Contains(got.Message, "pipeline #42 failed") {
		t.Fatalf("unexpected message: %s", got.Message)
	}
}

func TestGitLab_DisabledEventAcceptedWithoutSend(t *testing.T) {
	srv, sent := newGitLabTestServer(t, &GitLabConfig{
		Token:         "secret",
		DefaultChatID: "gitlab-chat",
		Events: map[string]bool{
			"push":          true,
			"merge_request": false,
			"pipeline":      false,
			"tag_push":      false,
			"job":           false,
		},
	})

	body := `{
		"object_kind": "pipeline",
		"project": {"path_with_namespace": "group/service"},
		"object_attributes": {"id": 42, "ref": "main", "status": "success"}
	}`

	w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(body), gitLabHeaders("Pipeline Hook", "secret"))
	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if len(*sent) != 0 {
		t.Fatalf("expected no sent payloads, got %d", len(*sent))
	}
}
