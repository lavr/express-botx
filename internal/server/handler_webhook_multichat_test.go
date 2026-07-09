package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Task 3: /alertmanager and /grafana share the project-wide multi-chat contract.
// ?chat_id may list several chats (comma-separated) and the event fans out to
// each best-effort; the response is always a MultiSendResponse, even for a single
// or default chat (results[0]). These tests exercise both handlers through the
// same table so the two surfaces stay in lockstep.

// webhookSurface abstracts the two notification endpoints so one table drives
// both: a POST path, an already-decodable firing payload, and a config builder
// that injects a custom send + chat resolver.
type webhookSurface struct {
	name    string
	path    string
	payload string
	// newServer builds a server for this surface with the given send/chat
	// resolvers and default chat wiring (webhookDefault -> DefaultChatID,
	// globalDefault -> cfg.DefaultChatAlias).
	newServer func(t *testing.T, webhookDefault, globalDefault string, sendFn SendFunc, chatFn ChatResolver) *Server
}

func webhookSurfaces(t *testing.T) []webhookSurface {
	t.Helper()
	amTmpl, err := ParseAlertmanagerTemplate(`alert {{ .Status }}`)
	if err != nil {
		t.Fatalf("parse alertmanager template: %v", err)
	}
	grTmpl, err := ParseGrafanaTemplate(`grafana {{ .Status }}`)
	if err != nil {
		t.Fatalf("parse grafana template: %v", err)
	}
	amPayload := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test", "severity": "critical"},
	})
	grPayload := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})
	return []webhookSurface{
		{
			name:    "alertmanager",
			path:    "/api/v1/alertmanager",
			payload: amPayload,
			newServer: func(t *testing.T, webhookDefault, globalDefault string, sendFn SendFunc, chatFn ChatResolver) *Server {
				return New(
					Config{Listen: ":0", BasePath: "/api/v1", DefaultChatAlias: globalDefault, Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
					sendFn, chatFn,
					WithAlertmanager(&AlertmanagerConfig{
						DefaultChatID:   webhookDefault,
						ErrorSeverities: []string{"critical"},
						Template:        amTmpl,
					}),
				)
			},
		},
		{
			name:    "grafana",
			path:    "/api/v1/grafana",
			payload: grPayload,
			newServer: func(t *testing.T, webhookDefault, globalDefault string, sendFn SendFunc, chatFn ChatResolver) *Server {
				return New(
					Config{Listen: ":0", BasePath: "/api/v1", DefaultChatAlias: globalDefault, Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
					sendFn, chatFn,
					WithGrafana(&GrafanaConfig{
						DefaultChatID: webhookDefault,
						ErrorStates:   []string{"alerting"},
						Template:      grTmpl,
					}),
				)
			},
		},
	}
}

func webhookHeaders() map[string]string {
	return map[string]string{"X-API-Key": "k", "Content-Type": "application/json"}
}

// selfChatResolver resolves every alias to itself, failing only for aliases in
// the fail set.
func selfChatResolver(fail ...string) ChatResolver {
	failSet := make(map[string]bool, len(fail))
	for _, f := range fail {
		failSet[f] = true
	}
	return func(chatID string) (ChatResolveResult, error) {
		if failSet[chatID] {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
}

// TestWebhook_SingleChatUnifiedResponse: a single/default chat still returns the
// uniform MultiSendResponse (results[0].sync_id), not the old {ok,sync_id} form.
func TestWebhook_SingleChatUnifiedResponse(t *testing.T) {
	for _, s := range webhookSurfaces(t) {
		t.Run(s.name, func(t *testing.T) {
			cap := &captureSend{}
			send := func(ctx context.Context, p *SendPayload) (string, error) {
				cap.record(p)
				return "sync-1", nil
			}
			srv := s.newServer(t, "default-chat", "", send, selfChatResolver())
			w := doRequest(srv, "POST", s.path, strings.NewReader(s.payload), webhookHeaders())
			if w.Code != 200 {
				t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 1 {
				t.Fatalf("send count = %d, want 1", cap.count())
			}
			if cap.last().ChatID != "default-chat" {
				t.Errorf("chat = %q, want default-chat", cap.last().ChatID)
			}
			var resp MultiSendResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
			}
			if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 0 {
				t.Fatalf("response = %+v, want ok with 1 result, 0 errors", resp)
			}
			if resp.Results[0].Chat != "default-chat" || resp.Results[0].SyncID != "sync-1" {
				t.Errorf("results[0] = %+v, want default-chat -> sync-1", resp.Results[0])
			}
		})
	}
}

// TestWebhook_MultiChatFanout: ?chat_id=a,b fans out to both chats (deduping
// repeats), preserving target order in the results.
func TestWebhook_MultiChatFanout(t *testing.T) {
	for _, s := range webhookSurfaces(t) {
		t.Run(s.name, func(t *testing.T) {
			cap := &captureSend{}
			send := func(ctx context.Context, p *SendPayload) (string, error) {
				cap.record(p)
				return "sync-1", nil
			}
			srv := s.newServer(t, "default-chat", "", send, selfChatResolver())
			w := doRequest(srv, "POST", s.path+"?chat_id=chatA,+chatB+,chatA", strings.NewReader(s.payload), webhookHeaders())
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
		})
	}
}

// TestWebhook_PartialFailure: one chat resolves and one does not — 200 with the
// successful result and the failed chat in errors[] (best-effort).
func TestWebhook_PartialFailure(t *testing.T) {
	for _, s := range webhookSurfaces(t) {
		t.Run(s.name, func(t *testing.T) {
			cap := &captureSend{}
			send := func(ctx context.Context, p *SendPayload) (string, error) {
				cap.record(p)
				return "sync-1", nil
			}
			srv := s.newServer(t, "", "", send, selfChatResolver("bad"))
			w := doRequest(srv, "POST", s.path+"?chat_id=chatA,bad", strings.NewReader(s.payload), webhookHeaders())
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (partial success), body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 1 {
				t.Fatalf("send count = %d, want 1 (only chatA sent)", cap.count())
			}
			var resp MultiSendResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
			}
			if !resp.OK || len(resp.Results) != 1 || len(resp.Errors) != 1 {
				t.Fatalf("response = %+v, want ok with 1 result, 1 error", resp)
			}
			if resp.Results[0].Chat != "chatA" {
				t.Errorf("results[0].Chat = %q, want chatA", resp.Results[0].Chat)
			}
			if resp.Errors[0].Chat != "bad" || !strings.Contains(resp.Errors[0].Error, "resolving chat") {
				t.Errorf("errors[0] = %+v, want bad -> resolving chat error", resp.Errors[0])
			}
		})
	}
}

// TestWebhook_AllFail: every target fails to resolve -> 502 with all errors and
// no results (unified contract, no send attempted).
func TestWebhook_AllFail(t *testing.T) {
	for _, s := range webhookSurfaces(t) {
		t.Run(s.name, func(t *testing.T) {
			cap := &captureSend{}
			send := func(ctx context.Context, p *SendPayload) (string, error) {
				cap.record(p)
				return "sync-1", nil
			}
			srv := s.newServer(t, "", "", send, selfChatResolver("badA", "badB"))
			w := doRequest(srv, "POST", s.path+"?chat_id=badA,badB", strings.NewReader(s.payload), webhookHeaders())
			if w.Code != 502 {
				t.Fatalf("status = %d, want 502 (all failed), body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 0 {
				t.Errorf("send count = %d, want 0", cap.count())
			}
			var resp MultiSendResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
			}
			if resp.OK || len(resp.Results) != 0 || len(resp.Errors) != 2 {
				t.Errorf("response = %+v, want not-ok with 0 results, 2 errors", resp)
			}
		})
	}
}

// TestWebhook_SendFailureAllFail: chats resolve but the upstream send fails for
// every target -> 502 with per-chat errors (send was attempted).
func TestWebhook_SendFailureAllFail(t *testing.T) {
	for _, s := range webhookSurfaces(t) {
		t.Run(s.name, func(t *testing.T) {
			cap := &captureSend{}
			send := func(ctx context.Context, p *SendPayload) (string, error) {
				cap.record(p)
				return "", fmt.Errorf("botx unavailable")
			}
			srv := s.newServer(t, "", "", send, selfChatResolver())
			w := doRequest(srv, "POST", s.path+"?chat_id=chatA,chatB", strings.NewReader(s.payload), webhookHeaders())
			if w.Code != 502 {
				t.Fatalf("status = %d, want 502, body: %s", w.Code, w.Body.String())
			}
			if cap.count() != 2 {
				t.Errorf("send count = %d, want 2 (both attempted then failed)", cap.count())
			}
			var resp MultiSendResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
			}
			if resp.OK || len(resp.Errors) != 2 {
				t.Errorf("response = %+v, want not-ok with 2 errors", resp)
			}
		})
	}
}

// TestWebhook_DefaultChatNoQuery: with no ?chat_id the endpoint delivers to the
// configured default chain (webhook default, then global default), unified shape.
func TestWebhook_DefaultChatNoQuery(t *testing.T) {
	cases := []struct {
		name           string
		webhookDefault string
		globalDefault  string
		wantChat       string
	}{
		{"webhook_default", "webhook-chat", "global-chat", "webhook-chat"},
		{"global_default", "", "global-chat", "global-chat"},
	}
	for _, s := range webhookSurfaces(t) {
		for _, tc := range cases {
			t.Run(s.name+"/"+tc.name, func(t *testing.T) {
				cap := &captureSend{}
				send := func(ctx context.Context, p *SendPayload) (string, error) {
					cap.record(p)
					return "sync-1", nil
				}
				srv := s.newServer(t, tc.webhookDefault, tc.globalDefault, send, selfChatResolver())
				w := doRequest(srv, "POST", s.path, strings.NewReader(s.payload), webhookHeaders())
				if w.Code != 200 {
					t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
				}
				if cap.count() != 1 || cap.last().ChatID != tc.wantChat {
					t.Fatalf("chat = %q (count %d), want %q", cap.last().ChatID, cap.count(), tc.wantChat)
				}
				var resp MultiSendResponse
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
				}
				if !resp.OK || len(resp.Results) != 1 || resp.Results[0].Chat != tc.wantChat {
					t.Errorf("response = %+v, want single %q result", resp, tc.wantChat)
				}
			})
		}
	}
}

// TestWebhook_EmptyChatIDIsRequestError: an explicitly-present but empty chat_id
// (?chat_id= or ?chat_id=,,) is a 400 request error, not a silent fall-back to the
// configured default chat.
func TestWebhook_EmptyChatIDIsRequestError(t *testing.T) {
	for _, s := range webhookSurfaces(t) {
		for _, q := range []string{"?chat_id=", "?chat_id=,,", "?chat_id=+,+"} {
			t.Run(s.name+" "+q, func(t *testing.T) {
				cap := &captureSend{}
				send := func(ctx context.Context, p *SendPayload) (string, error) {
					cap.record(p)
					return "sync-1", nil
				}
				// default-chat is configured, so a *missing* chat_id would deliver
				// there; an explicit-but-empty chat_id must instead 400.
				srv := s.newServer(t, "default-chat", "", send, selfChatResolver())
				w := doRequest(srv, "POST", s.path+q, strings.NewReader(s.payload), webhookHeaders())
				if w.Code != 400 {
					t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
				}
				if cap.count() != 0 {
					t.Errorf("send count = %d, want 0 (must not fall back to default)", cap.count())
				}
			})
		}
	}
}
