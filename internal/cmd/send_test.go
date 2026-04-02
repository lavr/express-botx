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

// mockBotxSendWithLookup extends mockBotxSend with user lookup support.
type mockBotxSendWithLookup struct {
	*mockBotxSend
	// users maps email -> {user_huid, name}
	users map[string]struct{ huid, name string }
}

func newMockBotxSendWithLookup(users map[string]struct{ huid, name string }) *mockBotxSendWithLookup {
	m := &mockBotxSendWithLookup{
		mockBotxSend: &mockBotxSend{},
		users:        users,
	}
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

	mux.HandleFunc("GET /api/v3/botx/users/by_email", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		email := r.URL.Query().Get("email")
		u, ok := m.users[email]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"status":"error","reason":"not_found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","result":{"user_huid":%q,"name":%q,"emails":[%q],"active":true}}`, u.huid, u.name, email)
	})

	m.srv = httptest.NewServer(mux)
	return m
}

func TestSend_InlineMentionEmail(t *testing.T) {
	mock := newMockBotxSendWithLookup(map[string]struct{ huid, name string }{
		"user@example.com": {huid: "11111111-1111-1111-1111-111111111111", name: "John Doe"},
	})
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

	err := runSend([]string{
		"--config", cfgPath,
		"--chat-id", "00000000-0000-0000-0000-00000000c001",
		"Hello @mention[email:user@example.com]!",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := mock.lastCall()
	if raw == nil {
		t.Fatal("expected a captured request, got none")
	}

	var req struct {
		Notification *struct {
			Body     string          `json:"body"`
			Mentions json.RawMessage `json:"mentions"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Notification == nil {
		t.Fatal("expected notification, got nil")
	}

	// Body should have the placeholder, not the original token
	if strings.Contains(req.Notification.Body, "@mention[") {
		t.Errorf("Body still contains inline token: %q", req.Notification.Body)
	}
	if !strings.Contains(req.Notification.Body, "@{mention:") {
		t.Errorf("Body missing BotX placeholder: %q", req.Notification.Body)
	}

	// Mentions should contain the resolved user
	var mentionsList []struct {
		MentionType string `json:"mention_type"`
		MentionData *struct {
			UserHUID string `json:"user_huid"`
			Name     string `json:"name"`
		} `json:"mention_data"`
	}
	if err := json.Unmarshal(req.Notification.Mentions, &mentionsList); err != nil {
		t.Fatalf("unmarshal mentions: %v", err)
	}
	if len(mentionsList) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(mentionsList))
	}
	if mentionsList[0].MentionType != "user" {
		t.Errorf("mention_type = %q, want %q", mentionsList[0].MentionType, "user")
	}
	if mentionsList[0].MentionData == nil || mentionsList[0].MentionData.UserHUID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("unexpected mention_data: %+v", mentionsList[0].MentionData)
	}
}

func TestSend_RawAndInlineMentionsMerge(t *testing.T) {
	mock := newMockBotxSendWithLookup(map[string]struct{ huid, name string }{
		"user@example.com": {huid: "22222222-2222-2222-2222-222222222222", name: "Jane Doe"},
	})
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

	rawMentions := `[{"mention_id":"raw-1","mention_type":"user","mention_data":{"user_huid":"aaaa","name":"Raw User"}}]`
	err := runSend([]string{
		"--config", cfgPath,
		"--chat-id", "00000000-0000-0000-0000-00000000c001",
		"--mentions", rawMentions,
		"@{mention:raw-1} and @mention[email:user@example.com]",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := mock.lastCall()
	if raw == nil {
		t.Fatal("expected a captured request, got none")
	}

	var req struct {
		Notification *struct {
			Mentions json.RawMessage `json:"mentions"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var mentionsList []struct {
		MentionID   string `json:"mention_id"`
		MentionType string `json:"mention_type"`
	}
	if err := json.Unmarshal(req.Notification.Mentions, &mentionsList); err != nil {
		t.Fatalf("unmarshal mentions: %v", err)
	}
	if len(mentionsList) != 2 {
		t.Fatalf("expected 2 mentions (raw + parsed), got %d", len(mentionsList))
	}
	// Raw mention comes first
	if mentionsList[0].MentionID != "raw-1" {
		t.Errorf("first mention should be raw, got mention_id=%q", mentionsList[0].MentionID)
	}
	// Parsed mention comes second
	if mentionsList[1].MentionType != "user" {
		t.Errorf("second mention should be parsed user, got type=%q", mentionsList[1].MentionType)
	}
}

func TestSend_NoParse(t *testing.T) {
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

	err := runSend([]string{
		"--config", cfgPath,
		"--chat-id", "00000000-0000-0000-0000-00000000c001",
		"--no-parse",
		"Hello @mention[email:user@example.com]!",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw := mock.lastCall()
	if raw == nil {
		t.Fatal("expected a captured request, got none")
	}

	var req struct {
		Notification *struct {
			Body     string          `json:"body"`
			Mentions json.RawMessage `json:"mentions"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Notification == nil {
		t.Fatal("expected notification, got nil")
	}

	// With --no-parse, the token should remain as-is
	if !strings.Contains(req.Notification.Body, "@mention[email:user@example.com]") {
		t.Errorf("Body should contain original token with --no-parse: %q", req.Notification.Body)
	}
	// No mentions should be generated
	if len(req.Notification.Mentions) > 0 {
		t.Errorf("expected no mentions with --no-parse, got: %s", string(req.Notification.Mentions))
	}
}

func TestSend_ParseErrorDoesNotFail(t *testing.T) {
	mock := newMockBotxSendWithLookup(nil) // no users -> lookup will fail
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

	// Use an email that won't resolve and a malformed token
	err := runSend([]string{
		"--config", cfgPath,
		"--chat-id", "00000000-0000-0000-0000-00000000c001",
		"Hello @mention[email:nobody@example.com] and @mention[bad syntax",
	}, deps)
	if err != nil {
		t.Fatalf("parse/lookup error should not fail the command, got: %v", err)
	}

	raw := mock.lastCall()
	if raw == nil {
		t.Fatal("expected a captured request, got none")
	}

	var req struct {
		Notification *struct {
			Body string `json:"body"`
		} `json:"notification"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Notification == nil {
		t.Fatal("expected notification, got nil")
	}

	// Tokens with errors should remain as literal text
	if !strings.Contains(req.Notification.Body, "@mention[email:nobody@example.com]") {
		t.Errorf("failed lookup token should stay as literal text: %q", req.Notification.Body)
	}
	if !strings.Contains(req.Notification.Body, "@mention[bad syntax") {
		t.Errorf("parse error token should stay as literal text: %q", req.Notification.Body)
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
