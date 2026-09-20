package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const incidentRelayPayload = `{
  "text": "NOTIFICATION: [P2] Disk almost full\nTeam: sre\nService: storage\nStatus: firing\nSeverity: critical\nAlert URL: http://ir/alerts/42",
  "alert_id": 42,
  "alert_url": "http://ir/alerts/42",
  "source_event_url": null,
  "team": "sre",
  "status": "firing",
  "source": "grafana",
  "title": "Disk almost full",
  "message": "node-1 at 94%",
  "severity": "critical",
  "priority": "P2",
  "priority_label": "P2 High",
  "assignee": "ivanov",
  "service": "storage",
  "service_id": 7,
  "service_name": "Storage",
  "service_slug": "storage",
  "service_links": [{"id": 1, "type": "dashboard", "label": "Board", "url": "http://g/d/1"}],
  "service_runbooks": [{"id": 2, "title": "Disk runbook", "url": "http://rb/2", "severity": "critical"}],
  "correlation": null
}`

func incidentRelayServer(t *testing.T, cfg *IncidentRelayConfig, sent *SendPayload) *Server {
	t.Helper()
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		*sent = *p
		return "test-sync-id", nil
	}
	return New(
		Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "t", Key: "k"}}},
		sendFn, selfChatResolver(),
		WithIncidentRelay(cfg),
	)
}

func TestIncidentRelay_MessageSource(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		payload     string
		wantCode    int
		wantMessage string
	}{
		{
			name:        "webhook source delivers text verbatim",
			source:      IncidentRelayMessageSourceWebhook,
			payload:     incidentRelayPayload,
			wantCode:    200,
			wantMessage: "NOTIFICATION: [P2] Disk almost full\nTeam: sre\nService: storage\nStatus: firing\nSeverity: critical\nAlert URL: http://ir/alerts/42",
		},
		{
			name:        "falls back to title and message when text is empty",
			source:      IncidentRelayMessageSourceWebhook,
			payload:     `{"title":"Disk almost full","message":"node-1 at 94%","severity":"critical","status":"firing"}`,
			wantCode:    200,
			wantMessage: "Disk almost full\n\nnode-1 at 94%",
		},
		{
			name:        "falls back to message alone when title is empty",
			source:      IncidentRelayMessageSourceWebhook,
			payload:     `{"message":"node-1 at 94%","status":"firing"}`,
			wantCode:    200,
			wantMessage: "node-1 at 94%",
		},
		{
			name:     "template source ignores text",
			source:   IncidentRelayMessageSourceTemplate,
			payload:  incidentRelayPayload,
			wantCode: 200,
			// exercised through an explicit template below
			wantMessage: "critical/firing Disk almost full",
		},
		{
			name:     "empty object carries no content",
			source:   IncidentRelayMessageSourceWebhook,
			payload:  `{}`,
			wantCode: 400,
		},
		{
			name:     "null payload carries no content",
			source:   IncidentRelayMessageSourceWebhook,
			payload:  `null`,
			wantCode: 400,
		},
		{
			name:     "invalid JSON is refused",
			source:   IncidentRelayMessageSourceWebhook,
			payload:  `{bad`,
			wantCode: 400,
		},
	}

	tmpl, err := ParseIncidentRelayTemplate(`{{ .Severity }}/{{ .Status }} {{ .Title }}`)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent SendPayload
			srv := incidentRelayServer(t, &IncidentRelayConfig{
				DefaultChatID:   "infra",
				MessageSource:   tt.source,
				ErrorSeverities: []string{"critical", "high"},
				Template:        tmpl,
			}, &sent)

			w := doRequest(srv, "POST", "/api/v1/incidentrelay", strings.NewReader(tt.payload), webhookHeaders())
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if tt.wantCode != 200 {
				return
			}
			if sent.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", sent.Message, tt.wantMessage)
			}
		})
	}
}

func TestIncidentRelay_Status(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantStatus string
	}{
		{
			name:       "critical while firing is an error",
			payload:    `{"text":"t","status":"firing","severity":"critical"}`,
			wantStatus: "error",
		},
		{
			name:       "resolved is ok whatever the severity",
			payload:    `{"text":"t","status":"resolved","severity":"critical"}`,
			wantStatus: "ok",
		},
		{
			name:       "severity outside error_severities is ok",
			payload:    `{"text":"t","status":"firing","severity":"warning"}`,
			wantStatus: "ok",
		},
		{
			name:       "acknowledged keeps the severity mapping",
			payload:    `{"text":"t","status":"acknowledged","severity":"high"}`,
			wantStatus: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent SendPayload
			srv := incidentRelayServer(t, &IncidentRelayConfig{
				DefaultChatID:   "infra",
				MessageSource:   IncidentRelayMessageSourceWebhook,
				ErrorSeverities: []string{"critical", "high"},
			}, &sent)

			w := doRequest(srv, "POST", "/api/v1/incidentrelay", strings.NewReader(tt.payload), webhookHeaders())
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			if sent.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", sent.Status, tt.wantStatus)
			}
		})
	}
}

func TestIncidentRelay_ScopedQueryKey(t *testing.T) {
	tests := []struct {
		name      string
		chatParam string
		wantCode  int
		wantSends int
	}{
		{
			name:      "own chat is delivered",
			chatParam: "own-chat",
			wantCode:  200,
			wantSends: 1,
		},
		{
			name:      "foreign chat is refused",
			chatParam: "other-chat",
			wantCode:  403,
			wantSends: 0,
		},
		{
			name:      "a foreign chat alongside an own one refuses the whole request",
			chatParam: "own-chat,other-chat",
			wantCode:  403,
			wantSends: 0,
		},
		{
			name:      "a foreign chat named by uuid is refused too",
			chatParam: otherUUID,
			wantCode:  403,
			wantSends: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sends := 0
			aliases := map[string]string{"own-chat": ownUUID, "other-chat": otherUUID}
			cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{
				{Name: "ir", Key: "secret", Chats: []string{ownUUID}, AllowQueryAuth: true},
			}}
			sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
				sends++
				return "sync-id", nil
			}
			chatFn := func(chatID string) (ChatResolveResult, error) {
				if id, ok := aliases[chatID]; ok {
					return ChatResolveResult{ChatID: id}, nil
				}
				if chatID == ownUUID || chatID == otherUUID {
					return ChatResolveResult{ChatID: chatID}, nil
				}
				return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
			}
			srv := New(cfg, sendFn, chatFn, WithIncidentRelay(&IncidentRelayConfig{
				MessageSource:   IncidentRelayMessageSourceWebhook,
				ErrorSeverities: []string{"critical"},
			}))

			w := doRequest(srv, "POST",
				"/api/v1/incidentrelay?api_key=secret&chat_id="+tt.chatParam,
				strings.NewReader(`{"text":"boom","status":"firing","severity":"critical"}`),
				map[string]string{"Content-Type": "application/json"})

			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
			if sends != tt.wantSends {
				t.Errorf("deliveries = %d, want %d", sends, tt.wantSends)
			}
		})
	}
}

func TestIncidentRelay_NilTemplateFallsBackToBuiltIn(t *testing.T) {
	var sent SendPayload
	srv := incidentRelayServer(t, &IncidentRelayConfig{
		DefaultChatID: "infra",
		MessageSource: IncidentRelayMessageSourceTemplate,
	}, &sent)

	w := doRequest(srv, "POST", "/api/v1/incidentrelay",
		strings.NewReader(`{"title":"Disk almost full","severity":"critical","status":"firing"}`),
		webhookHeaders())
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(sent.Message, "Disk almost full") {
		t.Errorf("built-in template was not used: %q", sent.Message)
	}
}
