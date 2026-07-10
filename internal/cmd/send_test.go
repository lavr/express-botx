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

// mockBotxSendMulti returns a per-chat sync_id ("sync-<group_chat_id>") and can be
// told to fail specific group_chat_ids (HTTP 500) to exercise partial/total fan-out.
type mockBotxSendMulti struct {
	mu      sync.Mutex
	chats   []string // group_chat_ids received, in call order
	failFor map[string]bool
	srv     *httptest.Server
}

func newMockBotxSendMulti(failFor map[string]bool) *mockBotxSendMulti {
	m := &mockBotxSendMulti{failFor: failFor}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v4/botx/notifications/direct", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			GroupChatID string `json:"group_chat_id"`
		}
		_ = json.Unmarshal(body, &req)
		m.mu.Lock()
		m.chats = append(m.chats, req.GroupChatID)
		m.mu.Unlock()
		if m.failFor[req.GroupChatID] {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"status":"error"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"status":"ok","result":{"sync_id":"sync-%s"}}`, req.GroupChatID)
	})
	m.srv = httptest.NewServer(mux)
	return m
}

func (m *mockBotxSendMulti) close() { m.srv.Close() }

func (m *mockBotxSendMulti) receivedChats() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.chats))
	copy(out, m.chats)
	return out
}

const (
	chatA = "00000000-0000-0000-0000-00000000000a"
	chatB = "00000000-0000-0000-0000-00000000000b"
	chatC = "00000000-0000-0000-0000-00000000000c"
)

func multiSendConfig(t *testing.T, host string) string {
	return writeTestConfig(t, fmt.Sprintf(`
bots:
  default:
    host: %s
    id: 00000000-0000-0000-0000-000000000001
    token: test-token
`, host))
}

func TestSend_SingleChat_SilentSuccess(t *testing.T) {
	mock := newMockBotxSendMulti(nil)
	defer mock.close()

	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA,
		"hello",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Single-chat success stays silent in human output (previous behavior).
	if out := stdout.String(); out != "" {
		t.Errorf("expected empty stdout for single-chat success, got %q", out)
	}
	if chats := mock.receivedChats(); len(chats) != 1 || chats[0] != chatA {
		t.Errorf("received chats = %v, want [%s]", chats, chatA)
	}
}

func TestSend_SingleChat_Failure(t *testing.T) {
	mock := newMockBotxSendMulti(map[string]bool{chatA: true}) // chatA delivery fails
	defer mock.close()

	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA,
		"hello",
	}, deps)
	// Single-chat failure surfaces via a non-nil error (non-zero exit), not stdout —
	// the legacy CLI contract for one chat.
	if err == nil {
		t.Fatal("expected error for single-chat delivery failure, got nil")
	}
	if out := stdout.String(); out != "" {
		t.Errorf("expected empty stdout on single-chat failure, got %q", out)
	}
	if chats := mock.receivedChats(); len(chats) != 1 || chats[0] != chatA {
		t.Errorf("received chats = %v, want [%s]", chats, chatA)
	}
}

func TestSend_MultiChat_FanOut(t *testing.T) {
	mock := newMockBotxSendMulti(nil)
	defer mock.close()

	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA + "," + chatB,
		"fanned out",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chats := mock.receivedChats()
	if len(chats) != 2 || chats[0] != chatA || chats[1] != chatB {
		t.Fatalf("received chats = %v, want [%s %s]", chats, chatA, chatB)
	}

	out := stdout.String()
	if !strings.Contains(out, chatA+": sync-"+chatA) {
		t.Errorf("missing line for chat A in %q", out)
	}
	if !strings.Contains(out, chatB+": sync-"+chatB) {
		t.Errorf("missing line for chat B in %q", out)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 1 {
		t.Errorf("expected 2 output lines, got %d: %q", n+1, out)
	}
}

func TestSend_MultiChat_Dedup(t *testing.T) {
	mock := newMockBotxSendMulti(nil)
	defer mock.close()

	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA + " , " + chatA + "," + chatB,
		"dedup me",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chats := mock.receivedChats(); len(chats) != 2 {
		t.Errorf("expected 2 deduped sends, got %d: %v", len(chats), chats)
	}
}

func TestSend_MultiChat_PartialFail(t *testing.T) {
	mock := newMockBotxSendMulti(map[string]bool{chatB: true})
	defer mock.close()

	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA + "," + chatB,
		"partial",
	}, deps)
	// At least one chat delivered -> exit code zero (best-effort).
	if err != nil {
		t.Fatalf("partial failure should not fail the command, got: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, chatA+": sync-"+chatA) {
		t.Errorf("missing success line for chat A in %q", out)
	}
	if !strings.Contains(out, chatB+": ERROR") {
		t.Errorf("missing error line for chat B in %q", out)
	}
}

func TestSend_MultiChat_AllFail(t *testing.T) {
	mock := newMockBotxSendMulti(map[string]bool{chatA: true, chatB: true})
	defer mock.close()

	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA + "," + chatB,
		"all fail",
	}, deps)
	// Every chat failed -> non-zero exit (returned error).
	if err == nil {
		t.Fatal("expected error when all chats fail")
	}

	out := stdout.String()
	if !strings.Contains(out, chatA+": ERROR") || !strings.Contains(out, chatB+": ERROR") {
		t.Errorf("expected error lines for both chats in %q", out)
	}
}

func TestSend_MultiChat_JSONOutput(t *testing.T) {
	mock := newMockBotxSendMulti(map[string]bool{chatB: true})
	defer mock.close()

	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA + "," + chatB,
		"--format", "json",
		"json multi",
	}, deps)
	// Partial failure still exits zero, but emits the uniform response body.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp struct {
		OK      bool `json:"ok"`
		Results []struct {
			Chat   string `json:"chat"`
			SyncID string `json:"sync_id"`
		} `json:"results"`
		Errors []struct {
			Chat  string `json:"chat"`
			Error string `json:"error"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, stdout.String())
	}
	if !resp.OK {
		t.Errorf("ok = false, want true (chat A delivered)")
	}
	if len(resp.Results) != 1 || resp.Results[0].Chat != chatA || resp.Results[0].SyncID != "sync-"+chatA {
		t.Errorf("results = %+v, want single success for chat A", resp.Results)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Chat != chatB {
		t.Errorf("errors = %+v, want single error for chat B", resp.Errors)
	}
}

func TestSend_MultiChat_UnknownAliasPerChat(t *testing.T) {
	mock := newMockBotxSendMulti(nil)
	defer mock.close()

	// chatA is a valid UUID; "nope" is an unknown alias -> per-chat resolve error,
	// but chatA still delivers (best-effort), so exit code stays zero.
	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiSendConfig(t, mock.srv.URL),
		"--chat-id", chatA + ",nope",
		"mixed",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chats := mock.receivedChats(); len(chats) != 1 || chats[0] != chatA {
		t.Errorf("received chats = %v, want only [%s]", chats, chatA)
	}
	out := stdout.String()
	if !strings.Contains(out, chatA+": sync-"+chatA) {
		t.Errorf("missing success line for chat A in %q", out)
	}
	if !strings.Contains(out, "nope: ERROR") {
		t.Errorf("missing resolve-error line for alias nope in %q", out)
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

// --- Bug fix: CLI multi-chat honours per-chat bot binding in multi-bot configs ---

// mockBotxMultiBot accepts several bearer tokens and records, per group_chat_id,
// which token was used — so a test can assert each chat was delivered via its
// bound bot's credentials.
type mockBotxMultiBot struct {
	mu     sync.Mutex
	byChat map[string]string // group_chat_id -> bearer token seen
	tokens map[string]bool   // accepted bearer tokens
	srv    *httptest.Server
}

func newMockBotxMultiBot(tokens map[string]bool) *mockBotxMultiBot {
	m := &mockBotxMultiBot{byChat: map[string]string{}, tokens: tokens}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v4/botx/notifications/direct", func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !m.tokens[tok] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			GroupChatID string `json:"group_chat_id"`
		}
		_ = json.Unmarshal(body, &req)
		m.mu.Lock()
		m.byChat[req.GroupChatID] = tok
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"status":"ok","result":{"sync_id":"sync-%s"}}`, req.GroupChatID)
	})
	m.srv = httptest.NewServer(mux)
	return m
}

func (m *mockBotxMultiBot) close() { m.srv.Close() }

func (m *mockBotxMultiBot) tokenFor(chat string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byChat[chat]
}

// multiBotConfig has two bots and two chat aliases, each bound to a different bot.
func multiBotConfig(t *testing.T, host string) string {
	return writeTestConfig(t, fmt.Sprintf(`
bots:
  bota:
    host: %s
    id: 00000000-0000-0000-0000-0000000000a1
    token: token-a
  botb:
    host: %s
    id: 00000000-0000-0000-0000-0000000000b1
    token: token-b
chats:
  deploy:
    id: %s
    bot: bota
  alerts:
    id: %s
    bot: botb
`, host, host, chatA, chatB))
}

// A multi-bot config with --chat-id deploy,alerts must (1) not fail at config
// load (the previous bug rejected the comma value as "multiple bots configured")
// and (2) deliver each chat via its own bound bot's token.
func TestSend_MultiBot_PerChatBot(t *testing.T) {
	mock := newMockBotxMultiBot(map[string]bool{"token-a": true, "token-b": true})
	defer mock.close()

	deps, _, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiBotConfig(t, mock.srv.URL),
		"--chat-id", "deploy,alerts",
		"hi",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := mock.tokenFor(chatA); got != "token-a" {
		t.Errorf("chat deploy (%s) delivered with token %q, want token-a", chatA, got)
	}
	if got := mock.tokenFor(chatB); got != "token-b" {
		t.Errorf("chat alerts (%s) delivered with token %q, want token-b", chatB, got)
	}
}

// In multi-bot mode a target with no bound bot (here a raw UUID) fails only that
// chat; bound chats still deliver and the command does not error overall.
func TestSend_MultiBot_UnboundChatFailsOnlyThatChat(t *testing.T) {
	mock := newMockBotxMultiBot(map[string]bool{"token-a": true, "token-b": true})
	defer mock.close()

	deps, stdout, _ := testDeps()
	deps.IsTerminal = true

	err := runSend([]string{
		"--config", multiBotConfig(t, mock.srv.URL),
		"--chat-id", "deploy," + chatC, // chatC: raw UUID, no bot binding
		"hi",
	}, deps)
	if err != nil {
		t.Fatalf("partial success must not return an error, got: %v", err)
	}
	if got := mock.tokenFor(chatA); got != "token-a" {
		t.Errorf("deploy should deliver via token-a, got %q", got)
	}
	if got := mock.tokenFor(chatC); got != "" {
		t.Errorf("unbound chatC must not be delivered, but saw token %q", got)
	}
	out := stdout.String()
	if !strings.Contains(out, chatC+": ERROR") {
		t.Errorf("expected an ERROR line for unbound chatC, got %q", out)
	}
}
