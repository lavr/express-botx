package cmd

import (
	"context"
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

	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/mentions"
	"github.com/lavr/express-botx/internal/queue"
	"github.com/lavr/express-botx/internal/server"
)

// --- mock BotX API ---

type mockBotxAPI struct {
	mu       sync.Mutex
	calls    []capturedSend // captured /notifications/direct calls
	tokenVal string         // token to return
	users    map[string]struct{ huid, name string } // email -> user info for lookup
}

type capturedSend struct {
	GroupChatID  string `json:"group_chat_id"`
	Notification *struct {
		Status   string          `json:"status"`
		Body     string          `json:"body"`
		Metadata json.RawMessage `json:"metadata,omitempty"`
		Mentions json.RawMessage `json:"mentions,omitempty"`
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

	// User lookup endpoint: GET /api/v3/botx/users/by_email
	mux.HandleFunc("GET /api/v3/botx/users/by_email", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+m.tokenVal {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		email := r.URL.Query().Get("email")
		if m.users == nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"status":"error","reason":"not_found"}`)
			return
		}
		u, ok := m.users[email]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"status":"error","reason":"not_found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","result":{"user_huid":%q,"name":%q,"emails":[%q],"active":true}}`, u.huid, u.name, email)
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

func TestServeIntegration_ChatBoundBot(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  deploy-bot:
    host: %s
    id: bot-deploy
    secret: secret-deploy
  alert-bot:
    host: %s
    id: bot-alert
    secret: secret-alert
chats:
  deploy:
    id: a0000000-0000-0000-0000-000000000001
    bot: deploy-bot
  alerts:
    id: b0000000-0000-0000-0000-000000000002
    bot: alert-bot
  general: c0000000-0000-0000-0000-000000000003
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

	// 1. Chat with bound bot — no "bot" needed
	code, resp := doPost(t, baseURL+"/send", "test-key", `{"chat_id":"deploy","message":"via chat binding"}`)
	if code != 200 {
		t.Fatalf("expected 200 for chat-bound bot, got %d: %v", code, resp)
	}

	// 2. Chat without bound bot — "bot" is required
	code, resp = doPost(t, baseURL+"/send", "test-key", `{"chat_id":"general","message":"hi"}`)
	if code != 400 {
		t.Fatalf("expected 400 for unbound chat without bot, got %d: %v", code, resp)
	}

	// 3. Explicit "bot" overrides chat binding
	mock.resetCalls()
	code, resp = doPost(t, baseURL+"/send", "test-key", `{"bot":"alert-bot","chat_id":"deploy","message":"override"}`)
	if code != 200 {
		t.Fatalf("expected 200 for explicit bot override, got %d: %v", code, resp)
	}

	// 4. Alertmanager uses chat-bound bot from default_chat_id
	mock.resetCalls()
	alertPayload := `{"version":"4","groupKey":"g","status":"firing","receiver":"x","groupLabels":{"alertname":"Test"},"alerts":[{"status":"firing","labels":{"alertname":"CPU","severity":"critical","instance":"x"},"annotations":{"summary":"hi"},"startsAt":"2026-01-01T00:00:00Z"}]}`
	code, resp = doPost(t, baseURL+"/alertmanager", "test-key", alertPayload)
	if code != 200 {
		t.Fatalf("expected 200 for alertmanager with chat-bound bot, got %d: %v", code, resp)
	}
}

func TestServeIntegration_ConfigEndpoints(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  deploy-bot:
    host: %s
    id: bot-deploy
    secret: secret-deploy
  alert-bot:
    host: %s
    id: bot-alert
    secret: secret-alert
chats:
  deploy:
    id: a0000000-0000-0000-0000-000000000001
    bot: deploy-bot
  general: b0000000-0000-0000-0000-000000000002
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
`, botxSrv.URL, botxSrv.URL, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})
	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	// GET /bot/list
	req, _ := http.NewRequest("GET", baseURL+"/bot/list", nil)
	req.Header.Set("X-API-Key", "test-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bot/list request error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for /bot/list, got %d", resp.StatusCode)
	}
	var bots []map[string]string
	json.NewDecoder(resp.Body).Decode(&bots)
	if len(bots) != 2 {
		t.Fatalf("expected 2 bots, got %d", len(bots))
	}

	// GET /chats/alias/list
	req, _ = http.NewRequest("GET", baseURL+"/chats/alias/list", nil)
	req.Header.Set("X-API-Key", "test-key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("chats/alias/list request error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for /chats/alias/list, got %d", resp.StatusCode)
	}
	var chats []map[string]string
	json.NewDecoder(resp.Body).Decode(&chats)
	if len(chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chats))
	}

	// Verify no auth → 401
	req, _ = http.NewRequest("GET", baseURL+"/bot/list", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}
}

func TestServeIntegration_StaticToken(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Bot uses static token — no /token API call needed
	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  main:
    host: %s
    id: bot-001
    token: %s
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
`, botxSrv.URL, mock.tokenVal, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	code, resp := doPost(t, baseURL+"/send", "test-key",
		`{"chat_id":"a0000000-0000-0000-0000-000000000001","message":"via static token"}`)
	if code != 200 {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Notification.Body != "via static token" {
		t.Errorf("unexpected body: %q", calls[0].Notification.Body)
	}
}

func TestServeIntegration_MixedSecretAndToken(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	port := freePort(t)
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// One bot with secret, another with token
	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  secret-bot:
    host: %s
    id: bot-s
    secret: secret-s
  token-bot:
    host: %s
    id: bot-t
    token: %s
server:
  listen: "%s"
  api_keys:
    - name: test
      key: test-key
`, botxSrv.URL, botxSrv.URL, mock.tokenVal, listenAddr))

	startServe(t, []string{"--config", cfgPath, "--listen", listenAddr, "--no-cache"})

	baseURL := fmt.Sprintf("http://%s/api/v1", listenAddr)

	// Send via secret-bot
	code, resp := doPost(t, baseURL+"/send", "test-key",
		`{"bot":"secret-bot","chat_id":"a0000000-0000-0000-0000-000000000001","message":"via secret"}`)
	if code != 200 {
		t.Fatalf("expected 200 via secret-bot, got %d: %v", code, resp)
	}

	// Send via token-bot
	code, resp = doPost(t, baseURL+"/send", "test-key",
		`{"bot":"token-bot","chat_id":"a0000000-0000-0000-0000-000000000001","message":"via token"}`)
	if code != 200 {
		t.Fatalf("expected 200 via token-bot, got %d: %v", code, resp)
	}
}

// TestBotSender_NilCache verifies that botSender.Send does not panic when
// cache is nil (the scenario from the Grafana webhook 502 bug).
func TestBotSender_NilCache(t *testing.T) {
	mock := newMockBotxAPI()
	botxSrv := httptest.NewServer(mock.handler())
	defer botxSrv.Close()

	cfg := &config.Config{
		Host:      botxSrv.URL,
		BotID:     "bot-001",
		BotSecret: "secret-001",
	}

	bs := &botSender{
		cfg:    cfg,
		client: botapi.NewClient(cfg.Host, "", cfg.HTTPTimeout()),
		cache:  nil, // simulate failFast=false with nil cache
	}

	ctx := context.Background()
	payload := &server.SendPayload{
		ChatID:  "a0000000-0000-0000-0000-000000000001",
		Message: "nil cache test",
	}

	// Must not panic; should succeed because refreshToken handles nil cache.
	syncID, err := bs.Send(ctx, payload)
	if err != nil {
		t.Fatalf("Send() returned error: %v", err)
	}
	if syncID == "" {
		t.Error("expected non-empty sync_id")
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}
	if calls[0].Notification == nil || calls[0].Notification.Body != "nil cache test" {
		t.Errorf("unexpected notification: %+v", calls[0].Notification)
	}
}

// TestBuildSendRequest_SyncPathMentions verifies that mentions from
// server.SendPayload are preserved in the BotX SendRequest (sync path).
func TestBuildSendRequest_SyncPathMentions(t *testing.T) {
	mentions := json.RawMessage(`[{"mention_id":"aaa","mention_type":"user","mention_data":{"user_huid":"xxx"}}]`)
	payload := &server.SendPayload{
		ChatID:   "chat-001",
		Message:  "@{mention:aaa} hello",
		Mentions: mentions,
	}

	sr := buildSendRequest(payload)

	if sr.Notification == nil {
		t.Fatal("expected notification to be set")
	}
	if string(sr.Notification.Mentions) != string(mentions) {
		t.Errorf("Mentions = %s, want %s", sr.Notification.Mentions, mentions)
	}
	if sr.Notification.Body != "@{mention:aaa} hello" {
		t.Errorf("Body = %q, want %q", sr.Notification.Body, "@{mention:aaa} hello")
	}
}

// TestBuildSendRequest_AsyncPathMentions verifies that mentions survive
// the full async pipeline: SendPayload -> queue.WorkMessage -> buildSendRequestFromWork.
func TestBuildSendRequest_AsyncPathMentions(t *testing.T) {
	mentions := json.RawMessage(`[{"mention_id":"bbb","mention_type":"contact","mention_data":{"user_huid":"yyy"}}]`)

	// Simulate what runServeEnqueue does: build WorkMessage from SendPayload
	msg := &queue.WorkMessage{
		RequestID: "req-async-mentions",
		Routing: queue.Routing{
			BotID:  "bot-001",
			ChatID: "chat-001",
		},
		Payload: queue.Payload{
			Message:  "@{mention:bbb} привет",
			Status:   "ok",
			Mentions: mentions,
		},
	}

	sr := buildSendRequestFromWork(msg)

	if sr.Notification == nil {
		t.Fatal("expected notification to be set")
	}
	if string(sr.Notification.Mentions) != string(mentions) {
		t.Errorf("Mentions = %s, want %s", sr.Notification.Mentions, mentions)
	}
	if sr.Notification.Body != "@{mention:bbb} привет" {
		t.Errorf("Body = %q, want %q", sr.Notification.Body, "@{mention:bbb} привет")
	}
}

// testUserResolver is a simple mentions.UserResolver for tests.
type testUserResolver struct {
	users map[string]struct{ huid, name string }
}

func (r *testUserResolver) GetUserByEmail(_ context.Context, email string) (string, string, error) {
	u, ok := r.users[email]
	if !ok {
		return "", "", fmt.Errorf("user not found: %s", email)
	}
	return u.huid, u.name, nil
}

func TestServeIntegration_SyncPath_InlineMentionNormalized(t *testing.T) {
	mock := newMockBotxAPI()
	mock.users = map[string]struct{ huid, name string }{
		"alice@example.com": {huid: "aaaa1111-1111-1111-1111-111111111111", name: "Alice"},
	}
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
		`{"chat_id":"c0000000-0000-0000-0000-000000000001","message":"Hi @mention[email:alice@example.com]!"}`)
	if code != 200 {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 BotX API call, got %d", len(calls))
	}

	call := calls[0]
	if call.Notification == nil {
		t.Fatal("expected notification in BotX call")
	}
	// Body should have BotX placeholder, not inline token
	if strings.Contains(call.Notification.Body, "@mention[") {
		t.Errorf("BotX body still contains inline token: %q", call.Notification.Body)
	}
	if !strings.Contains(call.Notification.Body, "@{mention:") {
		t.Errorf("BotX body missing placeholder: %q", call.Notification.Body)
	}
	// Mentions array should contain the parsed mention with correct user_huid
	if call.Notification.Mentions == nil {
		t.Fatal("expected mentions in BotX call")
	}
	var mentions []map[string]interface{}
	if err := json.Unmarshal(call.Notification.Mentions, &mentions); err != nil {
		t.Fatalf("failed to unmarshal mentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentions))
	}
	data, _ := mentions[0]["mention_data"].(map[string]interface{})
	if data == nil || data["user_huid"] != "aaaa1111-1111-1111-1111-111111111111" {
		t.Errorf("expected user_huid 'aaaa1111-...', got %v", data)
	}
}

func TestAsyncPath_InlineMentionNormalized(t *testing.T) {
	// Simulate the async pipeline: parse -> enqueue -> worker builds BotX request.
	// This verifies normalized data survives queue serialization.
	resolver := &testUserResolver{users: map[string]struct{ huid, name string }{
		"bob@example.com": {huid: "bbbb2222-2222-2222-2222-222222222222", name: "Bob"},
	}}

	// Step 1: Parse inline mentions (as handler_send.go:110 does)
	parseResult := mentions.Parse(context.Background(),
		"Hi @mention[email:bob@example.com]!", nil, true, resolver)

	// Verify parser output before enqueue
	if strings.Contains(parseResult.Message, "@mention[") {
		t.Fatalf("parser should have replaced inline token: %q", parseResult.Message)
	}
	if !strings.Contains(parseResult.Message, "@{mention:") {
		t.Fatalf("parser should have inserted BotX placeholder: %q", parseResult.Message)
	}

	// Step 2: Build WorkMessage (as sendFn does at serve.go:824-828)
	msg := &queue.WorkMessage{
		RequestID: "req-async-test",
		Routing:   queue.Routing{BotID: "bot-001", ChatID: "chat-001"},
		Payload: queue.Payload{
			Message:  parseResult.Message,
			Status:   "ok",
			Mentions: parseResult.Mentions,
		},
		ReplyTo:    "replies",
		EnqueuedAt: time.Now().UTC(),
	}

	// Step 3: Simulate queue serialization round-trip
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal WorkMessage: %v", err)
	}
	var restored queue.WorkMessage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal WorkMessage: %v", err)
	}

	// Step 4: Build BotX request from restored message (as worker does)
	sr := buildSendRequestFromWork(&restored)

	// Verify: body has BotX placeholder, not inline token
	if strings.Contains(sr.Notification.Body, "@mention[") {
		t.Errorf("BotX body still contains inline token after queue: %q", sr.Notification.Body)
	}
	if !strings.Contains(sr.Notification.Body, "@{mention:") {
		t.Errorf("BotX body missing placeholder after queue: %q", sr.Notification.Body)
	}

	// Verify: mentions survived queue round-trip
	if sr.Notification.Mentions == nil {
		t.Fatal("expected mentions in BotX request after queue")
	}
	var m []map[string]interface{}
	if err := json.Unmarshal(sr.Notification.Mentions, &m); err != nil {
		t.Fatalf("unmarshal mentions: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 mention after queue, got %d", len(m))
	}
	mentionData, _ := m[0]["mention_data"].(map[string]interface{})
	if mentionData == nil || mentionData["user_huid"] != "bbbb2222-2222-2222-2222-222222222222" {
		t.Errorf("expected user_huid 'bbbb2222-...', got %v", mentionData)
	}
}

// TestAuthenticate_ReturnsCacheOnGetTokenError verifies that authenticate
// returns a non-nil cache even when GetToken fails (the root cause fix).
func TestAuthenticate_ReturnsCacheOnGetTokenError(t *testing.T) {
	// Use a listener on a random port, then close it to get a refused connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := &config.Config{
		Host:      "http://" + addr,
		BotID:     "bot-001",
		BotSecret: "secret-001",
	}

	_, cache, err := authenticate(cfg)
	if err == nil {
		t.Fatal("expected error from authenticate with unreachable host")
	}
	if cache == nil {
		t.Fatal("authenticate returned nil cache on GetToken error; this causes panic in refreshToken")
	}
}
