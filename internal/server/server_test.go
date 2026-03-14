package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer creates a Server with stub send/chat functions for testing.
func newTestServer(keys []ResolvedKey, opts ...func(*Config)) *Server {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     keys,
	}
	for _, o := range opts {
		o(&cfg)
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "test-sync-id", nil
	}
	chatResolver := func(chatID string) (string, error) {
		if chatID == "unknown-alias" {
			return "", fmt.Errorf("unknown chat alias %q", chatID)
		}
		return chatID, nil
	}
	return New(cfg, sendFn, chatResolver)
}

// newTestServerWithOpts creates a Server with stub functions and server Options.
func newTestServerWithOpts(keys []ResolvedKey, srvOpts ...Option) *Server {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     keys,
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "test-sync-id", nil
	}
	chatResolver := func(chatID string) (string, error) {
		return chatID, nil
	}
	return New(cfg, sendFn, chatResolver, srvOpts...)
}

func doRequest(srv *Server, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func parseResponse(t *testing.T, w *httptest.ResponseRecorder) sendResponse {
	t.Helper()
	var resp sendResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

// --- healthz ---

func TestHealthz(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "test", Key: "key1"}})
	w := doRequest(srv, "GET", "/healthz", nil, nil)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

// --- auth ---

func TestAuth_BearerToken(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "myapp", Key: "secret123"}})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"Authorization": "Bearer secret123",
		"Content-Type":  "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_XAPIKey(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "myapp", Key: "secret123"}})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "secret123",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_NoCredentials(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "myapp", Key: "secret123"}})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"Content-Type": "application/json",
	})
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuth_WrongKey(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "myapp", Key: "secret123"}})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "wrong-key",
		"Content-Type": "application/json",
	})
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAuth_BotSignature_Enabled(t *testing.T) {
	srv := newTestServer(nil, func(c *Config) {
		c.AllowBotSecretAuth = true
		c.BotSignatures = map[string]string{"ABCDEF123456": ""}
	})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-Bot-Signature": "ABCDEF123456",
		"Content-Type":    "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_BotSignature_Disabled(t *testing.T) {
	srv := newTestServer(nil, func(c *Config) {
		c.AllowBotSecretAuth = false
		c.BotSignatures = map[string]string{"ABCDEF123456": ""}
	})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-Bot-Signature": "ABCDEF123456",
		"Content-Type":    "application/json",
	})
	// 403 because credentials were presented but rejected (bot secret auth is disabled)
	if w.Code != 403 {
		t.Fatalf("expected 403 when bot secret auth disabled, got %d", w.Code)
	}
}

func TestAuth_BotSignature_Wrong(t *testing.T) {
	srv := newTestServer(nil, func(c *Config) {
		c.AllowBotSecretAuth = true
		c.BotSignatures = map[string]string{"ABCDEF123456": ""}
	})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-Bot-Signature": "WRONG",
		"Content-Type":    "application/json",
	})
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAuth_MultipleKeys(t *testing.T) {
	srv := newTestServer([]ResolvedKey{
		{Name: "monitoring", Key: "mon-key"},
		{Name: "ci", Key: "ci-key"},
	})
	body := `{"chat_id":"chat-1","message":"hi"}`

	// Second key works
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "ci-key",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// --- send handler: JSON ---

func TestSend_JSON_TextOnly(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":"chat-1","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true")
	}
	if resp.SyncID != "test-sync-id" {
		t.Fatalf("expected sync_id=test-sync-id, got %q", resp.SyncID)
	}
}

func TestSend_JSON_WithFile(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":"chat-1","message":"see attached","file":{"name":"test.txt","data":"aGVsbG8="}}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSend_JSON_MissingChatID(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := parseResponse(t, w)
	if resp.Error != "chat_id is required" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestSend_JSON_EmptyBody(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":"chat-1"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := parseResponse(t, w)
	if resp.Error != "message or file required" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestSend_JSON_InvalidStatus(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":"chat-1","message":"hi","status":"bad"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSend_JSON_InvalidJSON(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader("{invalid"), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSend_JSON_ChatAlias_NotFound(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":"unknown-alias","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSend_UnsupportedContentType(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader("data"), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "text/plain",
	})
	if w.Code != 415 {
		t.Fatalf("expected 415, got %d", w.Code)
	}
}

// --- send handler: multipart ---

func TestSend_Multipart_TextOnly(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("chat_id", "chat-1")
	w.WriteField("message", "multipart text")
	w.Close()

	rec := doRequest(srv, "POST", "/api/v1/send", &buf, map[string]string{
		"X-API-Key":    "k",
		"Content-Type": w.FormDataContentType(),
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSend_Multipart_WithFile(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("chat_id", "chat-1")
	w.WriteField("message", "file attached")
	part, _ := w.CreateFormFile("file", "report.txt")
	part.Write([]byte("file content here"))
	w.Close()

	rec := doRequest(srv, "POST", "/api/v1/send", &buf, map[string]string{
		"X-API-Key":    "k",
		"Content-Type": w.FormDataContentType(),
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- send handler: upstream error ---

func TestSend_UpstreamError(t *testing.T) {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	failSend := func(ctx context.Context, p *SendPayload) (string, error) {
		return "", fmt.Errorf("connection refused")
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, failSend, chatResolver)

	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 502 {
		t.Fatalf("expected 502, got %d", w.Code)
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "upstream error") {
		t.Fatalf("expected upstream error, got: %s", resp.Error)
	}
}

// --- base path ---

func TestSend_CustomBasePath(t *testing.T) {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/express",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) { return "id", nil }
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver)

	body := `{"chat_id":"chat-1","message":"hi"}`

	// Custom path works
	w := doRequest(srv, "POST", "/express/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200 on /express/send, got %d: %s", w.Code, w.Body.String())
	}

	// Default path does not work
	w = doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code == 200 {
		t.Fatalf("expected /api/v1/send to NOT match with custom base path")
	}
}

// --- alertmanager ---

func testAlertmanagerConfig(t *testing.T) *AlertmanagerConfig {
	t.Helper()
	tmpl, err := ParseAlertmanagerTemplate(DefaultAlertmanagerTemplate)
	if err != nil {
		t.Fatalf("parse default template: %v", err)
	}
	return &AlertmanagerConfig{
		DefaultChatID:          "alert-chat-id",
		ErrorSeverities: []string{"critical", "warning"},
		Template:        tmpl,
	}
}

func alertmanagerPayload(status string, alerts ...AlertItem) string {
	w := AlertmanagerWebhook{
		Version:     "4",
		GroupKey:     "test-group",
		Status:      status,
		Receiver:    "express",
		GroupLabels: map[string]string{"alertname": "TestAlert"},
		Alerts:      alerts,
	}
	b, _ := json.Marshal(w)
	return string(b)
}

func TestAlertmanager_Firing(t *testing.T) {
	amCfg := testAlertmanagerConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithAlertmanager(amCfg),
	)

	body := alertmanagerPayload("firing", AlertItem{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical", "instance": "web-01"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})

	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true")
	}
}

func TestAlertmanager_Resolved(t *testing.T) {
	amCfg := testAlertmanagerConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithAlertmanager(amCfg),
	)

	body := alertmanagerPayload("resolved", AlertItem{
		Status:      "resolved",
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical", "instance": "web-01"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})

	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAlertmanager_NoAlerts(t *testing.T) {
	amCfg := testAlertmanagerConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithAlertmanager(amCfg),
	)

	body := alertmanagerPayload("firing")
	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAlertmanager_InvalidJSON(t *testing.T) {
	amCfg := testAlertmanagerConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithAlertmanager(amCfg),
	)

	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader("{bad"), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAlertmanager_NotConfigured(t *testing.T) {
	srv := newTestServerWithOpts([]ResolvedKey{{Name: "t", Key: "k"}})

	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})
	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	// Route not registered when amCfg is nil, expect 405 (method not allowed) or 404
	if w.Code == 200 {
		t.Fatalf("expected non-200 when alertmanager not configured, got 200")
	}
}

func TestAlertmanager_StatusMapping(t *testing.T) {
	amCfg := testAlertmanagerConfig(t)

	tests := []struct {
		name     string
		webhook  AlertmanagerWebhook
		expected string
	}{
		{
			"resolved always ok",
			AlertmanagerWebhook{Status: "resolved", Alerts: []AlertItem{{Labels: map[string]string{"severity": "critical"}}}},
			"ok",
		},
		{
			"firing critical is error",
			AlertmanagerWebhook{Status: "firing", Alerts: []AlertItem{{Labels: map[string]string{"severity": "critical"}}}},
			"error",
		},
		{
			"firing warning is error",
			AlertmanagerWebhook{Status: "firing", Alerts: []AlertItem{{Labels: map[string]string{"severity": "warning"}}}},
			"error",
		},
		{
			"firing info is ok",
			AlertmanagerWebhook{Status: "firing", Alerts: []AlertItem{{Labels: map[string]string{"severity": "info"}}}},
			"ok",
		},
	}

	srv := &Server{amCfg: amCfg}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := srv.resolveAlertStatus(tt.webhook)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestAlertmanager_ChatIDQueryParam(t *testing.T) {
	tmpl, _ := ParseAlertmanagerTemplate(`test`)
	amCfg := &AlertmanagerConfig{
		DefaultChatID:          "default-chat",
		ErrorSeverities: []string{"critical"},
		Template:        tmpl,
	}

	var lastChatID string
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		lastChatID = p.ChatID
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver, WithAlertmanager(amCfg))

	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test", "severity": "critical"},
	})

	// With query param
	w := doRequest(srv, "POST", "/api/v1/alertmanager?chat_id=override-chat", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if lastChatID != "override-chat" {
		t.Errorf("expected chat_id=override-chat, got %q", lastChatID)
	}

	// Without query param — uses config default
	w = doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if lastChatID != "default-chat" {
		t.Errorf("expected chat_id=default-chat, got %q", lastChatID)
	}
}

func TestAlertmanager_NoChatID(t *testing.T) {
	tmpl, _ := ParseAlertmanagerTemplate(`test`)
	amCfg := &AlertmanagerConfig{
		DefaultChatID:          "", // no default
		ErrorSeverities: []string{"critical"},
		Template:        tmpl,
	}
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithAlertmanager(amCfg),
	)

	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})

	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAlertmanager_CustomTemplate(t *testing.T) {
	tmpl, err := ParseAlertmanagerTemplate(`ALERT: {{ index .GroupLabels "alertname" }} is {{ .Status }}`)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	amCfg := &AlertmanagerConfig{
		DefaultChatID:          "chat-1",
		ErrorSeverities: []string{"critical"},
		Template:        tmpl,
	}

	var lastMsg string
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		lastMsg = p.Message
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver, WithAlertmanager(amCfg))

	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "DiskFull", "severity": "critical"},
	})

	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(lastMsg, "ALERT: TestAlert is firing") {
		t.Errorf("unexpected message: %q", lastMsg)
	}
}

// --- grafana ---

func testGrafanaConfig(t *testing.T) *GrafanaConfig {
	t.Helper()
	tmpl, err := ParseGrafanaTemplate(DefaultGrafanaTemplate)
	if err != nil {
		t.Fatalf("parse default grafana template: %v", err)
	}
	return &GrafanaConfig{
		DefaultChatID: "alert-chat-id",
		ErrorStates:   []string{"alerting"},
		Template:      tmpl,
	}
}

func grafanaPayload(status, state, title string, alerts ...GrafanaAlertItem) string {
	w := GrafanaWebhook{
		Version:     "1",
		GroupKey:    "test-group",
		Status:     status,
		State:      state,
		Title:      title,
		Receiver:   "express",
		OrgID:      1,
		GroupLabels: map[string]string{"alertname": "TestAlert"},
		Alerts:     alerts,
	}
	b, _ := json.Marshal(w)
	return string(b)
}

func TestGrafana_Firing(t *testing.T) {
	grCfg := testGrafanaConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithGrafana(grCfg),
	)

	body := grafanaPayload("firing", "alerting", "[FIRING:1] HighCPU", GrafanaAlertItem{
		Status:       "firing",
		Labels:       map[string]string{"alertname": "HighCPU", "grafana_folder": "Production"},
		Annotations:  map[string]string{"summary": "CPU > 90%"},
		DashboardURL: "http://grafana:3000/d/abc",
		PanelURL:     "http://grafana:3000/d/abc?viewPanel=1",
		SilenceURL:   "http://grafana:3000/alerting/silence/new",
	})

	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true")
	}
}

func TestGrafana_Resolved(t *testing.T) {
	grCfg := testGrafanaConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithGrafana(grCfg),
	)

	body := grafanaPayload("resolved", "ok", "[RESOLVED] HighCPU", GrafanaAlertItem{
		Status:      "resolved",
		Labels:      map[string]string{"alertname": "HighCPU", "grafana_folder": "Production"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})

	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGrafana_NoAlerts(t *testing.T) {
	grCfg := testGrafanaConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithGrafana(grCfg),
	)

	body := grafanaPayload("firing", "alerting", "test")
	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGrafana_InvalidJSON(t *testing.T) {
	grCfg := testGrafanaConfig(t)
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithGrafana(grCfg),
	)

	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader("{bad"), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGrafana_NotConfigured(t *testing.T) {
	srv := newTestServerWithOpts([]ResolvedKey{{Name: "t", Key: "k"}})

	body := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})
	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code == 200 {
		t.Fatalf("expected non-200 when grafana not configured, got 200")
	}
}

func TestGrafana_StatusMapping(t *testing.T) {
	grCfg := testGrafanaConfig(t)

	tests := []struct {
		name     string
		webhook  GrafanaWebhook
		expected string
	}{
		{
			"resolved always ok",
			GrafanaWebhook{Status: "resolved", State: "ok", Alerts: []GrafanaAlertItem{{}}},
			"ok",
		},
		{
			"alerting is error",
			GrafanaWebhook{Status: "firing", State: "alerting", Alerts: []GrafanaAlertItem{{}}},
			"error",
		},
		{
			"no_data is ok",
			GrafanaWebhook{Status: "firing", State: "no_data", Alerts: []GrafanaAlertItem{{}}},
			"ok",
		},
		{
			"pending is ok",
			GrafanaWebhook{Status: "firing", State: "pending", Alerts: []GrafanaAlertItem{{}}},
			"ok",
		},
	}

	srv := &Server{grCfg: grCfg}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := srv.resolveGrafanaStatus(tt.webhook)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestGrafana_ChatIDQueryParam(t *testing.T) {
	tmpl, _ := ParseGrafanaTemplate(`test`)
	grCfg := &GrafanaConfig{
		DefaultChatID: "default-chat",
		ErrorStates:   []string{"alerting"},
		Template:      tmpl,
	}

	var lastChatID string
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		lastChatID = p.ChatID
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver, WithGrafana(grCfg))

	body := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})

	// With query param
	w := doRequest(srv, "POST", "/api/v1/grafana?chat_id=override-chat", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if lastChatID != "override-chat" {
		t.Errorf("expected chat_id=override-chat, got %q", lastChatID)
	}

	// Without query param — uses config default
	w = doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if lastChatID != "default-chat" {
		t.Errorf("expected chat_id=default-chat, got %q", lastChatID)
	}
}

func TestGrafana_NoChatID(t *testing.T) {
	tmpl, _ := ParseGrafanaTemplate(`test`)
	grCfg := &GrafanaConfig{
		DefaultChatID: "",
		ErrorStates:   []string{"alerting"},
		Template:      tmpl,
	}
	srv := newTestServerWithOpts(
		[]ResolvedKey{{Name: "t", Key: "k"}},
		WithGrafana(grCfg),
	)

	body := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})

	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGrafana_CustomTemplate(t *testing.T) {
	tmpl, err := ParseGrafanaTemplate(`GRAFANA: {{ .Title }} is {{ .State }}`)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	grCfg := &GrafanaConfig{
		DefaultChatID: "chat-1",
		ErrorStates:   []string{"alerting"},
		Template:      tmpl,
	}

	var lastMsg string
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		lastMsg = p.Message
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver, WithGrafana(grCfg))

	body := grafanaPayload("firing", "alerting", "[FIRING:1] DiskFull", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "DiskFull"},
	})

	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(lastMsg, "GRAFANA: [FIRING:1] DiskFull is alerting") {
		t.Errorf("unexpected message: %q", lastMsg)
	}
}

// --- multi-bot ---

func newMultiBotTestServer(keys []ResolvedKey) *Server {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     keys,
		BotNames: []string{"prod", "test"},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "sync-" + p.Bot, nil
	}
	chatResolver := func(chatID string) (string, error) {
		return chatID, nil
	}
	return New(cfg, sendFn, chatResolver)
}

func TestSend_MultiBot_RequiresBot(t *testing.T) {
	srv := newMultiBotTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "bot is required") {
		t.Errorf("expected 'bot is required', got: %s", resp.Error)
	}
}

func TestSend_MultiBot_UnknownBot(t *testing.T) {
	srv := newMultiBotTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"bot":"staging","chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "unknown bot") {
		t.Errorf("expected 'unknown bot', got: %s", resp.Error)
	}
}

func TestSend_MultiBot_ValidBot(t *testing.T) {
	srv := newMultiBotTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"bot":"prod","chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if resp.SyncID != "sync-prod" {
		t.Errorf("expected sync_id=sync-prod, got %q", resp.SyncID)
	}
}

func TestSend_SingleBot_BotFieldOptional(t *testing.T) {
	// Single-bot server (no BotNames) — bot field is not required
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAlertmanager_MultiBot_RequiresBot(t *testing.T) {
	amCfg := testAlertmanagerConfig(t)
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
		BotNames: []string{"prod", "test"},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver, WithAlertmanager(amCfg))

	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test", "severity": "critical", "instance": "x"},
		Annotations: map[string]string{"summary": "test"},
	})

	// Without ?bot= — should fail
	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// With ?bot=prod — should succeed
	w = doRequest(srv, "POST", "/api/v1/alertmanager?bot=prod", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGrafana_MultiBot_RequiresBot(t *testing.T) {
	grCfg := testGrafanaConfig(t)
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
		BotNames: []string{"prod", "test"},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver, WithGrafana(grCfg))

	body := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test", "grafana_folder": "Prod"},
		Annotations: map[string]string{"summary": "test"},
	})

	// Without ?bot= — should fail
	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// With ?bot=test — should succeed
	w = doRequest(srv, "POST", "/api/v1/grafana?bot=test", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuth_BotSignature_Multiple(t *testing.T) {
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		AllowBotSecretAuth: true,
		BotSignatures:      map[string]string{"SIG_PROD": "prod", "SIG_TEST": "test"},
		BotNames:           []string{"prod", "test"},
	}
	var lastBot string
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		lastBot = p.Bot
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver)

	// Prod signature works and binds to prod bot
	body := `{"chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-Bot-Signature": "SIG_PROD",
		"Content-Type":    "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200 for prod sig, got %d: %s", w.Code, w.Body.String())
	}
	if lastBot != "prod" {
		t.Errorf("expected bot=prod, got %q", lastBot)
	}

	// Test signature works and binds to test bot
	w = doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-Bot-Signature": "SIG_TEST",
		"Content-Type":    "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200 for test sig, got %d: %s", w.Code, w.Body.String())
	}
	if lastBot != "test" {
		t.Errorf("expected bot=test, got %q", lastBot)
	}

	// Wrong signature rejected
	w = doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-Bot-Signature": "SIG_WRONG",
		"Content-Type":    "application/json",
	})
	if w.Code != 403 {
		t.Fatalf("expected 403 for wrong sig, got %d", w.Code)
	}
}

func TestAuth_BotSignature_PrivilegeEscalation(t *testing.T) {
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		AllowBotSecretAuth: true,
		BotSignatures:      map[string]string{"SIG_TEST": "test"},
		BotNames:           []string{"prod", "test"},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver)

	// Authenticated as test, trying to send as prod — should be rejected
	body := `{"bot":"prod","chat_id":"chat-1","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-Bot-Signature": "SIG_TEST",
		"Content-Type":    "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400 for privilege escalation, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "does not match authenticated bot") {
		t.Errorf("expected mismatch error, got: %s", resp.Error)
	}
}

func TestGrafana_StatusSentToUpstream(t *testing.T) {
	tmpl, _ := ParseGrafanaTemplate(`test`)
	grCfg := &GrafanaConfig{
		DefaultChatID: "chat-1",
		ErrorStates:   []string{"alerting"},
		Template:      tmpl,
	}

	var lastStatus string
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		lastStatus = p.Status
		return "id", nil
	}
	chatResolver := func(chatID string) (string, error) { return chatID, nil }
	srv := New(cfg, sendFn, chatResolver, WithGrafana(grCfg))

	// Firing with state=alerting → error
	body := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})
	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if lastStatus != "error" {
		t.Errorf("expected status=error, got %q", lastStatus)
	}

	// Resolved → ok
	body = grafanaPayload("resolved", "ok", "test", GrafanaAlertItem{
		Status: "resolved",
		Labels: map[string]string{"alertname": "Test"},
	})
	w = doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if lastStatus != "ok" {
		t.Errorf("expected status=ok, got %q", lastStatus)
	}
}
