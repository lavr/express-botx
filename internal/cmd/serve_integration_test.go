package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- mock BotX API ---

type mockBotxAPI struct {
	mu       sync.Mutex
	calls    []capturedSend // captured /notifications/direct calls
	tokenVal string         // token to return
}

type capturedSend struct {
	GroupChatID string          `json:"group_chat_id"`
	Notification *struct {
		Status   string          `json:"status"`
		Body     string          `json:"body"`
		Metadata json.RawMessage `json:"metadata,omitempty"`
	} `json:"notification,omitempty"`
	File *struct {
		FileName string `json:"file_name"`
		Data     string `json:"data"`
	} `json:"file,omitempty"`
}

func newMockBotxAPI() *mockBotxAPI {
	return &mockBotxAPI{tokenVal: "mock-token-abc"}
}

func (m *mockBotxAPI) handler() http.Handler {
	mux := http.NewServeMux()

	// Token endpoint: GET /api/v2/botx/bots/{id}/token?signature=...
	mux.HandleFunc("GET /api/v2/botx/bots/{id}/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"result": m.tokenVal,
		})
	})

	// Send endpoint: POST /api/v4/botx/notifications/direct
	mux.HandleFunc("POST /api/v4/botx/notifications/direct", func(w http.ResponseWriter, r *http.Request) {
		// Verify bearer token
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+m.tokenVal {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var req capturedSend
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		m.mu.Lock()
		m.calls = append(m.calls, req)
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"result": map[string]string{
				"sync_id": fmt.Sprintf("sync-%d", len(m.calls)),
			},
		})
	})

	return mux
}

func (m *mockBotxAPI) getCalls() []capturedSend {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]capturedSend, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockBotxAPI) resetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
}

// --- helpers ---

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func waitForServer(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://%s/healthz", addr)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s not ready after %s", addr, timeout)
}

type serveResult struct {
	err error
}

// startServe starts runServe in a background goroutine and returns when the server is ready.
// Returns a channel that receives the result when the server stops, and a cancel function.
func startServe(t *testing.T, args []string) (result chan serveResult, cancel func()) {
	t.Helper()

	result = make(chan serveResult, 1)
	deps, _, _ := testDeps()

	// We need to be able to stop the server. runServe listens for SIGTERM,
	// but in tests we'll just use the --listen port and let t.Cleanup close things.
	go func() {
		err := runServe(args, deps)
		result <- serveResult{err: err}
	}()

	// Extract listen address from args
	listenAddr := ":8080" // default
	for i, a := range args {
		if a == "--listen" && i+1 < len(args) {
			listenAddr = args[i+1]
		}
	}

	waitForServer(t, listenAddr, 5*time.Second)
	return result, func() {
		// Server will be stopped by the test ending (deferred cleanup)
	}
}

func doPost(t *testing.T, url, apiKey, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sending request: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var result map[string]any
	json.Unmarshal(respBody, &result)
	return resp.StatusCode, result
}

// --- integration tests ---

func TestServeIntegration_SingleBot_Send(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  main:
    host: %s
    id: bot-001
    secret: secret-001
chats:
  deploy: a0000000-0000-0000-0000-000000000001
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
`, botxSrv.URL, listenAddr))

	result, _ := startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})
	_ = result

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	// Test 1: send text message
	code, resp := doPost(t, baseURL+"/send", "test-key", `{"chat_id":"deploy","message":"hello from test"}`)
	if code != 200 {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}
	if calls[0].GroupChatID != "a0000000-0000-0000-0000-000000000001" {
		t.Errorf("GroupChatID = %q, want %q", calls[0].GroupChatID, "a0000000-0000-0000-0000-000000000001")
	}
	if calls[0].Notification == nil || calls[0].Notification.Body != "hello from test" {
		t.Errorf("unexpected notification body: %+v", calls[0].Notification)
	}
}

func TestServeIntegration_SingleBot_SendWithFile(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  main:
    host: %s
    id: bot-001
    secret: secret-001
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
`, botxSrv.URL, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	code, resp := doPost(t, baseURL+"/send", "test-key",
		`{"chat_id":"b0000000-0000-0000-0000-000000000002","message":"see file","file":{"name":"test.txt","data":"aGVsbG8="}}`)
	if code != 200 {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].File == nil || calls[0].File.FileName != "test.txt" {
		t.Errorf("expected file test.txt, got %+v", calls[0].File)
	}
	if calls[0].Notification == nil || calls[0].Notification.Body != "see file" {
		t.Errorf("expected message 'see file', got %+v", calls[0].Notification)
	}
}

func TestServeIntegration_MultiBotSend(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  prod:
    host: %s
    id: bot-prod
    secret: secret-prod
  test:
    host: %s
    id: bot-test
    secret: secret-test
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
`, botxSrv.URL, botxSrv.URL, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	// Without bot — should fail
	code, resp := doPost(t, baseURL+"/send", "test-key", `{"chat_id":"c0000000-0000-0000-0000-000000000003","message":"hi"}`)
	if code != 400 {
		t.Fatalf("expected 400 without bot, got %d: %v", code, resp)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "bot is required") {
		t.Errorf("expected 'bot is required', got %q", errMsg)
	}

	// With valid bot
	code, resp = doPost(t, baseURL+"/send", "test-key", `{"bot":"prod","chat_id":"c0000000-0000-0000-0000-000000000003","message":"via prod"}`)
	if code != 200 {
		t.Fatalf("expected 200 with bot=prod, got %d: %v", code, resp)
	}

	// With unknown bot
	code, resp = doPost(t, baseURL+"/send", "test-key", `{"bot":"staging","chat_id":"c0000000-0000-0000-0000-000000000003","message":"hi"}`)
	if code != 400 {
		t.Fatalf("expected 400 for unknown bot, got %d: %v", code, resp)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 successful call, got %d", len(calls))
	}
	if calls[0].Notification.Body != "via prod" {
		t.Errorf("expected 'via prod', got %q", calls[0].Notification.Body)
	}
}

func TestServeIntegration_MultiBotAlertmanager(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  prod:
    host: %s
    id: bot-prod
    secret: secret-prod
  test:
    host: %s
    id: bot-test
    secret: secret-test
chats:
  alerts: d0000000-0000-0000-0000-000000000004
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
  alertmanager:
    default_chat_id: alerts
`, botxSrv.URL, botxSrv.URL, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)
	alertPayload := `{"version":"4","groupKey":"g","status":"firing","receiver":"x","groupLabels":{"alertname":"Test"},"alerts":[{"status":"firing","labels":{"alertname":"HighCPU","severity":"critical","instance":"web-01"},"annotations":{"summary":"CPU high"},"startsAt":"2026-01-01T00:00:00Z"}]}`

	// Without ?bot= — should fail
	code, resp := doPost(t, baseURL+"/alertmanager", "test-key", alertPayload)
	if code != 400 {
		t.Fatalf("expected 400 without bot, got %d: %v", code, resp)
	}

	// With ?bot=prod
	code, resp = doPost(t, baseURL+"/alertmanager?bot=prod", "test-key", alertPayload)
	if code != 200 {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].GroupChatID != "d0000000-0000-0000-0000-000000000004" {
		t.Errorf("GroupChatID = %q, want %q", calls[0].GroupChatID, "d0000000-0000-0000-0000-000000000004")
	}
}

func TestServeIntegration_MultiBotGrafana(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  prod:
    host: %s
    id: bot-prod
    secret: secret-prod
  test:
    host: %s
    id: bot-test
    secret: secret-test
chats:
  alerts: e0000000-0000-0000-0000-000000000005
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
  grafana:
    default_chat_id: alerts
`, botxSrv.URL, botxSrv.URL, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)
	grafanaPayload := `{"version":"1","groupKey":"g","status":"firing","state":"alerting","title":"[FIRING] Test","receiver":"x","orgId":1,"groupLabels":{"alertname":"Test"},"alerts":[{"status":"firing","labels":{"alertname":"DiskFull","grafana_folder":"Prod"},"annotations":{"summary":"Disk full"},"startsAt":"2026-01-01T00:00:00Z"}]}`

	// Without ?bot= — should fail
	code, resp := doPost(t, baseURL+"/grafana", "test-key", grafanaPayload)
	if code != 400 {
		t.Fatalf("expected 400 without bot, got %d: %v", code, resp)
	}

	// With ?bot=test
	code, resp = doPost(t, baseURL+"/grafana?bot=test", "test-key", grafanaPayload)
	if code != 200 {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].GroupChatID != "e0000000-0000-0000-0000-000000000005" {
		t.Errorf("GroupChatID = %q, want %q", calls[0].GroupChatID, "e0000000-0000-0000-0000-000000000005")
	}
}

func TestServeIntegration_ChatAliasResolution(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  main:
    host: %s
    id: bot-001
    secret: secret-001
chats:
  deploy: resolved-uuid-deploy
  alerts: resolved-uuid-alerts
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
`, botxSrv.URL, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	// Alias resolves to UUID
	code, _ := doPost(t, baseURL+"/send", "test-key", `{"chat_id":"deploy","message":"hi"}`)
	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}

	calls := mock.getCalls()
	if calls[0].GroupChatID != "resolved-uuid-deploy" {
		t.Errorf("expected resolved UUID, got %q", calls[0].GroupChatID)
	}

	// Unknown alias — 400
	code, resp := doPost(t, baseURL+"/send", "test-key", `{"chat_id":"unknown-alias","message":"hi"}`)
	if code != 400 {
		t.Fatalf("expected 400 for unknown alias, got %d: %v", code, resp)
	}

	// Raw UUID passes through
	mock.resetCalls()
	code, _ = doPost(t, baseURL+"/send", "test-key", `{"chat_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890","message":"hi"}`)
	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	calls = mock.getCalls()
	if calls[0].GroupChatID != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Errorf("expected raw UUID passthrough, got %q", calls[0].GroupChatID)
	}
}

func TestServeIntegration_Auth(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  main:
    host: %s
    id: bot-001
    secret: secret-001
server:
  listen: "%s"
  api_keys:
    - name: test
      key: correct-key
`, botxSrv.URL, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	// Correct key
	code, _ := doPost(t, baseURL+"/send", "correct-key", `{"chat_id":"f0000000-0000-0000-0000-000000000006","message":"hi"}`)
	if code != 200 {
		t.Fatalf("expected 200 with correct key, got %d", code)
	}

	// Wrong key
	code, _ = doPost(t, baseURL+"/send", "wrong-key", `{"chat_id":"f0000000-0000-0000-0000-000000000006","message":"hi"}`)
	if code != 403 {
		t.Fatalf("expected 403 with wrong key, got %d", code)
	}

	// No key
	req, _ := http.NewRequest("POST", baseURL+"/send", strings.NewReader(`{"chat_id":"f0000000-0000-0000-0000-000000000006","message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 with no key, got %d", resp.StatusCode)
	}
}
