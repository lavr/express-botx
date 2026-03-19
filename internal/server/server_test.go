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

	"github.com/lavr/express-botx/internal/config"
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
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if chatID == "unknown-alias" {
			return ChatResolveResult{}, fmt.Errorf("unknown chat alias %q", chatID)
		}
		return ChatResolveResult{ChatID: chatID}, nil
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
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
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

func TestSend_JSON_MissingChatID_WithDefaultChat(t *testing.T) {
	var capturedChatID string
	cfg := Config{
		Listen:           ":0",
		BasePath:         "/api/v1",
		Keys:             []ResolvedKey{{Name: "t", Key: "k"}},
		DefaultChatAlias: "general",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedChatID = p.ChatID
		return "test-sync-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedChatID != "general" {
		t.Fatalf("expected chat_id 'general' (default), got %q", capturedChatID)
	}
}

func TestSend_JSON_ExplicitChatID_OverridesDefault(t *testing.T) {
	var capturedChatID string
	cfg := Config{
		Listen:           ":0",
		BasePath:         "/api/v1",
		Keys:             []ResolvedKey{{Name: "t", Key: "k"}},
		DefaultChatAlias: "general",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedChatID = p.ChatID
		return "test-sync-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"chat_id":"deploy","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedChatID != "deploy" {
		t.Fatalf("expected chat_id 'deploy', got %q", capturedChatID)
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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

func TestAlertmanager_GlobalDefaultChat(t *testing.T) {
	tmpl, _ := ParseAlertmanagerTemplate(`test`)
	amCfg := &AlertmanagerConfig{
		DefaultChatID:   "", // no webhook default
		ErrorSeverities: []string{"critical"},
		Template:        tmpl,
	}

	var capturedChatID string
	cfg := Config{
		Listen:           ":0",
		BasePath:         "/api/v1",
		Keys:             []ResolvedKey{{Name: "t", Key: "k"}},
		DefaultChatAlias: "general",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedChatID = p.ChatID
		return "id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver, WithAlertmanager(amCfg))

	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test", "severity": "critical"},
	})
	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedChatID != "general" {
		t.Errorf("expected chat_id=general (global default), got %q", capturedChatID)
	}
}

func TestAlertmanager_WebhookDefaultOverridesGlobalDefault(t *testing.T) {
	tmpl, _ := ParseAlertmanagerTemplate(`test`)
	amCfg := &AlertmanagerConfig{
		DefaultChatID:   "alerts", // webhook default takes priority
		ErrorSeverities: []string{"critical"},
		Template:        tmpl,
	}

	var capturedChatID string
	cfg := Config{
		Listen:           ":0",
		BasePath:         "/api/v1",
		Keys:             []ResolvedKey{{Name: "t", Key: "k"}},
		DefaultChatAlias: "general",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedChatID = p.ChatID
		return "id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver, WithAlertmanager(amCfg))

	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test", "severity": "critical"},
	})
	w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedChatID != "alerts" {
		t.Errorf("expected chat_id=alerts (webhook default), got %q", capturedChatID)
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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

func TestGrafana_GlobalDefaultChat(t *testing.T) {
	tmpl, _ := ParseGrafanaTemplate(`test`)
	grCfg := &GrafanaConfig{
		DefaultChatID: "", // no webhook default
		ErrorStates:   []string{"alerting"},
		Template:      tmpl,
	}

	var capturedChatID string
	cfg := Config{
		Listen:           ":0",
		BasePath:         "/api/v1",
		Keys:             []ResolvedKey{{Name: "t", Key: "k"}},
		DefaultChatAlias: "general",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedChatID = p.ChatID
		return "id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver, WithGrafana(grCfg))

	body := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})
	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedChatID != "general" {
		t.Errorf("expected chat_id=general (global default), got %q", capturedChatID)
	}
}

func TestGrafana_WebhookDefaultOverridesGlobalDefault(t *testing.T) {
	tmpl, _ := ParseGrafanaTemplate(`test`)
	grCfg := &GrafanaConfig{
		DefaultChatID: "alerts", // webhook default takes priority
		ErrorStates:   []string{"alerting"},
		Template:      tmpl,
	}

	var capturedChatID string
	cfg := Config{
		Listen:           ":0",
		BasePath:         "/api/v1",
		Keys:             []ResolvedKey{{Name: "t", Key: "k"}},
		DefaultChatAlias: "general",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedChatID = p.ChatID
		return "id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver, WithGrafana(grCfg))

	body := grafanaPayload("firing", "alerting", "test", GrafanaAlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "Test"},
	})
	w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedChatID != "alerts" {
		t.Errorf("expected chat_id=alerts (webhook default), got %q", capturedChatID)
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
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

func TestSend_MultiBot_ChatBoundBot(t *testing.T) {
	// Chat resolver returns a bound bot — no need for explicit "bot" in request
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
		BotNames: []string{"prod", "test"},
	}
	var lastBot string
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		lastBot = p.Bot
		return "id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if chatID == "deploy" {
			return ChatResolveResult{ChatID: "uuid-deploy", Bot: "prod"}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	// No "bot" in request, but chat has bound bot → should succeed
	body := `{"chat_id":"deploy","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if lastBot != "prod" {
		t.Errorf("expected bot=prod from chat binding, got %q", lastBot)
	}

	// Explicit "bot" overrides chat binding
	body = `{"bot":"test","chat_id":"deploy","message":"hi"}`
	w = doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if lastBot != "test" {
		t.Errorf("expected bot=test from explicit override, got %q", lastBot)
	}
}

func TestSend_SingleBot_RejectsMismatchedChatBot(t *testing.T) {
	cfg := Config{
		Listen:        ":0",
		BasePath:      "/api/v1",
		Keys:          []ResolvedKey{{Name: "t", Key: "k"}},
		SingleBotName: "prod",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "id", nil
	}
	// Chat resolver returns a bot binding that doesn't match the single bot
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if chatID == "alerts" {
			return ChatResolveResult{ChatID: "uuid-alerts", Bot: "alert-bot"}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	// Chat bound to "alert-bot" but server runs as "prod" → should fail
	body := `{"chat_id":"alerts","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400 for mismatched chat-bound bot, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "not available") {
		t.Errorf("expected 'not available' error, got: %s", resp.Error)
	}

	// Chat without binding → should pass
	body = `{"chat_id":"general","message":"hi"}`
	w = doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200 for unbound chat, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSend_SingleBot_EnvFlags_RejectsChatBoundBot(t *testing.T) {
	// Server started from env/flags — SingleBotName is empty.
	// Chat has a bot binding → should be rejected because env/flags sender
	// is not a named bot and cannot serve named bot requests.
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
		// SingleBotName deliberately empty (env/flags mode)
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		if chatID == "alerts" {
			return ChatResolveResult{ChatID: "uuid-alerts", Bot: "alert-bot"}, nil
		}
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	// Chat bound to "alert-bot" but server has no named bot → 400
	body := `{"chat_id":"alerts","message":"hi"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 400 {
		t.Fatalf("expected 400 for chat-bound bot with unnamed sender, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "not available") {
		t.Errorf("expected 'not available' error, got: %s", resp.Error)
	}

	// Chat without binding → should pass
	body = `{"chat_id":"general","message":"hi"}`
	w = doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})
	if w.Code != 200 {
		t.Fatalf("expected 200 for unbound chat, got %d: %s", w.Code, w.Body.String())
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
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

// --- docs ---

// --- config endpoints ---

func TestBotList(t *testing.T) {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) { return "", nil }
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
	srv := New(cfg, sendFn, chatResolver, WithConfigInfo(
		[]config.BotEntry{
			{Name: "alert-bot", Host: "h2", ID: "id-2"},
			{Name: "deploy-bot", Host: "h1", ID: "id-1"},
		},
		nil,
	))

	w := doRequest(srv, "GET", "/api/v1/bot/list", nil, map[string]string{"X-API-Key": "k"})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var bots []config.BotEntry
	json.NewDecoder(w.Body).Decode(&bots)
	if len(bots) != 2 {
		t.Fatalf("expected 2 bots, got %d", len(bots))
	}
	if bots[0].Name != "alert-bot" {
		t.Errorf("bots[0].Name = %q, want %q", bots[0].Name, "alert-bot")
	}
}

func TestBotList_NoAuth(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	w := doRequest(srv, "GET", "/api/v1/bot/list", nil, nil)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestChatsAliasList(t *testing.T) {
	cfg := Config{
		Listen:   ":0",
		BasePath: "/api/v1",
		Keys:     []ResolvedKey{{Name: "t", Key: "k"}},
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) { return "", nil }
	chatResolver := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
	srv := New(cfg, sendFn, chatResolver, WithConfigInfo(
		nil,
		[]config.ChatEntry{
			{Name: "deploy", ID: "uuid-1", Bot: "deploy-bot"},
			{Name: "general", ID: "uuid-2"},
		},
	))

	w := doRequest(srv, "GET", "/api/v1/chats/alias/list", nil, map[string]string{"X-API-Key": "k"})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var chats []config.ChatEntry
	json.NewDecoder(w.Body).Decode(&chats)
	if len(chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chats))
	}
	if chats[0].Bot != "deploy-bot" {
		t.Errorf("chats[0].Bot = %q, want %q", chats[0].Bot, "deploy-bot")
	}
	if chats[1].Bot != "" {
		t.Errorf("chats[1].Bot = %q, want empty", chats[1].Bot)
	}
}

func TestBotList_Empty(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}})
	w := doRequest(srv, "GET", "/api/v1/bot/list", nil, map[string]string{"X-API-Key": "k"})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "null") && !strings.Contains(w.Body.String(), "[]") {
		t.Errorf("expected null or [], got: %s", w.Body.String())
	}
}

func TestDocs_Enabled(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}}, func(c *Config) {
		c.EnableDocs = true
	})

	// /docs redirects to /docs/
	w := doRequest(srv, "GET", "/docs", nil, nil)
	if w.Code != 301 {
		t.Fatalf("expected 301, got %d", w.Code)
	}

	// /docs/ serves Swagger UI HTML
	w = doRequest(srv, "GET", "/docs/", nil, nil)
	if w.Code != 200 {
		t.Fatalf("expected 200 for /docs/, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "swagger-ui") {
		t.Error("/docs/ should contain swagger-ui")
	}

	// /docs/openapi.yaml serves the spec
	w = doRequest(srv, "GET", "/docs/openapi.yaml", nil, nil)
	if w.Code != 200 {
		t.Fatalf("expected 200 for openapi.yaml, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "openapi:") {
		t.Error("openapi.yaml should contain openapi: field")
	}
}

func TestResolveOrigin(t *testing.T) {
	tests := []struct {
		name       string
		extScheme  string
		extHost    string
		headers    map[string]string
		reqHost    string
		wantScheme string
		wantHost   string
	}{
		{
			name:       "external URL takes priority",
			extScheme:  "https",
			extHost:    "express-botx.invitro-dev.k8s",
			headers:    map[string]string{"X-Forwarded-Host": "other.host", "X-Forwarded-Proto": "http"},
			reqHost:    "localhost:8080",
			wantScheme: "https",
			wantHost:   "express-botx.invitro-dev.k8s",
		},
		{
			name:       "external URL without scheme defaults to http",
			extHost:    "express-botx.invitro-dev.k8s",
			reqHost:    "localhost:8080",
			wantScheme: "http",
			wantHost:   "express-botx.invitro-dev.k8s",
		},
		{
			name:       "X-Forwarded headers",
			headers:    map[string]string{"X-Forwarded-Host": "app.example.com", "X-Forwarded-Proto": "https"},
			reqHost:    "localhost:8080",
			wantScheme: "https",
			wantHost:   "app.example.com",
		},
		{
			name:       "X-Forwarded-Host only, scheme defaults to http",
			headers:    map[string]string{"X-Forwarded-Host": "app.example.com"},
			reqHost:    "localhost:8080",
			wantScheme: "http",
			wantHost:   "app.example.com",
		},
		{
			name:       "Host header fallback",
			reqHost:    "myhost:9090",
			wantScheme: "http",
			wantHost:   "myhost:9090",
		},
		{
			name:       "strip default http port 80",
			headers:    map[string]string{"X-Forwarded-Host": "app.example.com:80", "X-Forwarded-Proto": "http"},
			wantScheme: "http",
			wantHost:   "app.example.com",
		},
		{
			name:       "strip default https port 443",
			headers:    map[string]string{"X-Forwarded-Host": "app.example.com:443", "X-Forwarded-Proto": "https"},
			wantScheme: "https",
			wantHost:   "app.example.com",
		},
		{
			name:       "keep non-default port",
			headers:    map[string]string{"X-Forwarded-Host": "app.example.com:8443", "X-Forwarded-Proto": "https"},
			wantScheme: "https",
			wantHost:   "app.example.com:8443",
		},
		{
			name:       "no headers and no host defaults to localhost:8080",
			wantScheme: "http",
			wantHost:   "localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/openapi.yaml", nil)
			r.Host = tt.reqHost
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			scheme, host := resolveOrigin(r, tt.extScheme, tt.extHost)
			if scheme != tt.wantScheme {
				t.Errorf("scheme = %q, want %q", scheme, tt.wantScheme)
			}
			if host != tt.wantHost {
				t.Errorf("host = %q, want %q", host, tt.wantHost)
			}
		})
	}
}

func TestDocs_OpenAPISpecReplacesHostAndScheme(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}}, func(c *Config) {
		c.EnableDocs = true
		c.ExternalURL = "https://express-botx.invitro-dev.k8s"
	})

	w := doRequest(srv, "GET", "/docs/openapi.yaml", nil, nil)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "default: express-botx.invitro-dev.k8s") {
		t.Errorf("expected host from external_url, got:\n%s", body)
	}
	if !strings.Contains(body, "default: https\n") {
		t.Errorf("expected scheme https from external_url, got:\n%s", body)
	}
	if strings.Contains(body, "localhost:8080") {
		t.Error("spec should not contain localhost:8080")
	}
}

func TestDocs_Disabled(t *testing.T) {
	srv := newTestServer([]ResolvedKey{{Name: "t", Key: "k"}}, func(c *Config) {
		c.EnableDocs = false
	})

	w := doRequest(srv, "GET", "/docs/", nil, nil)
	if w.Code == 200 {
		t.Fatalf("expected non-200 when docs disabled, got 200")
	}
}

// --- async mode ---

func TestSend_AsyncMode_DirectPublish(t *testing.T) {
	var capturedPayload *SendPayload
	cfg := Config{
		Listen:    ":0",
		BasePath:  "/api/v1",
		Keys:      []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode: true,
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedPayload = p
		return "test-request-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"bot_id":"00000000-0000-0000-0000-000000000001","chat_id":"00000000-0000-0000-0000-000000000002","message":"async hello","routing_mode":"direct"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	if !resp.OK {
		t.Error("expected ok=true")
	}
	if !resp.Queued {
		t.Error("expected queued=true")
	}
	if resp.RequestID != "test-request-id" {
		t.Errorf("request_id = %q, want %q", resp.RequestID, "test-request-id")
	}

	if capturedPayload == nil {
		t.Fatal("sendFn was not called")
	}
	if capturedPayload.BotID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("BotID = %q, want %q", capturedPayload.BotID, "00000000-0000-0000-0000-000000000001")
	}
	if capturedPayload.ChatID != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("ChatID = %q, want %q", capturedPayload.ChatID, "00000000-0000-0000-0000-000000000002")
	}
	if capturedPayload.Message != "async hello" {
		t.Errorf("Message = %q, want %q", capturedPayload.Message, "async hello")
	}
}

func TestSend_AsyncMode_MissingBotID(t *testing.T) {
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		Keys:               []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode:          true,
		DefaultRoutingMode: "direct",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	// Missing bot_id in async direct mode → 400
	body := `{"chat_id":"chat-001","message":"no bot_id"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "bot_id is required") {
		t.Errorf("expected 'bot_id is required' error, got: %s", resp.Error)
	}
}

func TestSend_AsyncMode_Multipart(t *testing.T) {
	var capturedPayload *SendPayload
	cfg := Config{
		Listen:    ":0",
		BasePath:  "/api/v1",
		Keys:      []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode: true,
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedPayload = p
		return "test-req-multipart", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	// Build multipart form
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("bot_id", "00000000-0000-0000-0000-000000000003")
	mw.WriteField("chat_id", "00000000-0000-0000-0000-000000000004")
	mw.WriteField("message", "multipart async")
	mw.WriteField("routing_mode", "direct")
	mw.Close()

	w := doRequest(srv, "POST", "/api/v1/send", &buf, map[string]string{
		"X-API-Key":    "k",
		"Content-Type": mw.FormDataContentType(),
	})

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	if capturedPayload == nil {
		t.Fatal("sendFn was not called")
	}
	if capturedPayload.BotID != "00000000-0000-0000-0000-000000000003" {
		t.Errorf("BotID = %q, want %q", capturedPayload.BotID, "00000000-0000-0000-0000-000000000003")
	}
	if capturedPayload.RoutingMode != "direct" {
		t.Errorf("RoutingMode = %q, want %q", capturedPayload.RoutingMode, "direct")
	}
}

func TestSend_AsyncMode_EnqueueError(t *testing.T) {
	cfg := Config{
		Listen:    ":0",
		BasePath:  "/api/v1",
		Keys:      []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode: true,
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "", fmt.Errorf("broker connection refused")
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"bot_id":"00000000-0000-0000-0000-000000000005","chat_id":"00000000-0000-0000-0000-000000000006","message":"will fail"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})

	if w.Code != 502 {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "enqueue error") {
		t.Errorf("expected 'enqueue error', got: %s", resp.Error)
	}
}

func TestSend_AsyncMode_MixedMode_BotAlias(t *testing.T) {
	// Mixed mode: bot alias provided instead of bot_id → should be accepted
	var capturedPayload *SendPayload
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		Keys:               []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode:          true,
		DefaultRoutingMode: "mixed",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedPayload = p
		return "test-mixed-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"bot":"alerts","chat_id":"deploy","message":"mixed with alias","routing_mode":"mixed"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	if capturedPayload == nil {
		t.Fatal("sendFn was not called")
	}
	if capturedPayload.Bot != "alerts" {
		t.Errorf("Bot = %q, want %q", capturedPayload.Bot, "alerts")
	}
}

func TestSend_AsyncMode_CatalogMode_BotAlias(t *testing.T) {
	// Catalog mode: bot alias is sufficient (no bot_id needed)
	var capturedPayload *SendPayload
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		Keys:               []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode:          true,
		DefaultRoutingMode: "catalog",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		capturedPayload = p
		return "test-catalog-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"bot":"alerts","chat_id":"deploy","message":"catalog with alias"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})

	if w.Code != 202 {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	if capturedPayload == nil {
		t.Fatal("sendFn was not called")
	}
	if capturedPayload.Bot != "alerts" {
		t.Errorf("Bot = %q, want %q", capturedPayload.Bot, "alerts")
	}
}

func TestSend_AsyncMode_CatalogMode_NoBotOrBotIDOrChatID(t *testing.T) {
	// Catalog mode: no bot_id, bot, or chat_id → error
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		Keys:               []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode:          true,
		DefaultRoutingMode: "catalog",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"message":"no bot or chat"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := parseResponse(t, w)
	if !strings.Contains(resp.Error, "chat_id is required") && !strings.Contains(resp.Error, "bot_id, bot, or chat_id") {
		t.Errorf("expected chat_id or bot identification error, got: %s", resp.Error)
	}
}

func TestSend_AsyncMode_CatalogMode_ChatIDOnly(t *testing.T) {
	// Catalog mode: chat_id without bot info should pass validation
	// (bot can be derived from chat binding during resolution)
	cfg := Config{
		Listen:             ":0",
		BasePath:           "/api/v1",
		Keys:               []ResolvedKey{{Name: "t", Key: "k"}},
		AsyncMode:          true,
		DefaultRoutingMode: "catalog",
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "req-123", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	srv := New(cfg, sendFn, chatResolver)

	body := `{"chat_id":"deploy","message":"hello"}`
	w := doRequest(srv, "POST", "/api/v1/send", strings.NewReader(body), map[string]string{
		"X-API-Key":    "k",
		"Content-Type": "application/json",
	})

	// Should not be rejected at validation - the send function handles resolution
	if w.Code == 400 {
		t.Fatalf("expected request to pass validation, got 400: %s", w.Body.String())
	}
}
