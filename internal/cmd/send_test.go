package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockBotxSend captures requests sent to /api/v4/botx/notifications/direct.
type mockBotxSend struct {
	mu    sync.Mutex
	calls []json.RawMessage
	srv   *httptest.Server
}

func newMockBotxSend() *mockBotxSend {
	m := &mockBotxSend{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v4/botx/notifications/direct", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.calls = append(m.calls, json.RawMessage(body))
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"status":"ok","result":{"sync_id":"sync-1"}}`)
	})

	m.srv = httptest.NewServer(mux)
	return m
}

func (m *mockBotxSend) close() { m.srv.Close() }

func (m *mockBotxSend) lastCall() json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return m.calls[len(m.calls)-1]
}

func TestSend_WithMentions(t *testing.T) {
	mock := newMockBotxSend()
	defer mock.close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  default:
    host: %s
    id: 00000000-0000-0000-0000-000000000001
    token: test-token
`, mock.srv.URL))

	deps, _, _ := testDeps()
	deps.IsTerminal = true

	mentions := `[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Ivan"}}]`
	err := runSend([]string{
		"--config", cfgPath,
		"--chat-id", "00000000-0000-0000-0000-00000000c001",
		"--mentions", mentions,
		"@{mention:aaa-bbb} hello",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := mock.lastCall()
	if raw == nil {
		t.Fatal("expected a captured request, got none")
	}

	var req struct {
		GroupChatID  string `json:"group_chat_id"`
		Notification *struct {
			Body     string          `json:"body"`
			Mentions json.RawMessage `json:"mentions"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal captured request: %v", err)
	}

	if req.GroupChatID != "00000000-0000-0000-0000-00000000c001" {
		t.Errorf("GroupChatID = %q, want %q", req.GroupChatID, "00000000-0000-0000-0000-00000000c001")
	}
	if req.Notification == nil {
		t.Fatal("expected notification, got nil")
	}
	if req.Notification.Body != "@{mention:aaa-bbb} hello" {
		t.Errorf("Body = %q, want %q", req.Notification.Body, "@{mention:aaa-bbb} hello")
	}

	// Verify mentions passed through unchanged
	if string(req.Notification.Mentions) != mentions {
		t.Errorf("Mentions = %s, want %s", string(req.Notification.Mentions), mentions)
	}
}

func TestSend_MentionsInvalidJSON(t *testing.T) {
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	cfgPath := writeTestConfig(t, `
bots:
  default:
    host: https://example.com
    id: 00000000-0000-0000-0000-000000000001
    token: test-token
`)

	err := runSend([]string{
		"--config", cfgPath,
		"--chat-id", "00000000-0000-0000-0000-00000000c001",
		"--mentions", `{not valid json`,
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "--mentions is not valid JSON") {
		t.Errorf("expected '--mentions is not valid JSON' error, got: %v", err)
	}
}

func TestSend_MentionsNotArray(t *testing.T) {
	deps, _, _ := testDeps()
	deps.IsTerminal = true

	cfgPath := writeTestConfig(t, `
bots:
  default:
    host: https://example.com
    id: 00000000-0000-0000-0000-000000000001
    token: test-token
`)

	err := runSend([]string{
		"--config", cfgPath,
		"--chat-id", "00000000-0000-0000-0000-00000000c001",
		"--mentions", `{"mention_id":"aaa"}`,
		"hello",
	}, deps)
	if err == nil {
		t.Fatal("expected error for non-array JSON")
	}
	if !strings.Contains(err.Error(), "--mentions must be a JSON array") {
		t.Errorf("expected '--mentions must be a JSON array' error, got: %v", err)
	}
}
