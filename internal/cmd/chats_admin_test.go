package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lavr/express-botx/internal/botapi"
	vlog "github.com/lavr/express-botx/internal/log"
)

const (
	testChatID      = "aaaaaaaa-1111-4111-8111-000000000001"
	testFatherID    = "bbbbbbbb-2222-4222-8222-000000000002"
	testAlertsBotID = "cccccccc-3333-4333-8333-000000000003"
	testUserHUID    = "dddddddd-4444-4444-8444-000000000004"
	testUser2HUID   = "11111111-2222-3333-4444-555555555555"
)

type chatsAdminCall struct {
	Path string
	Body map[string]any
}

type existingChat struct {
	ID, Name, Type, Creator string
	BotLeft                 bool
}

type chatsAdminServer struct {
	*httptest.Server
	mu       sync.Mutex
	calls    []chatsAdminCall
	existing []existingChat
}

func (s *chatsAdminServer) addExisting(chats ...existingChat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.existing = append(s.existing, chats...)
}

func (s *chatsAdminServer) findExisting(id string) (existingChat, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.existing {
		if c.ID == id {
			return c, true
		}
	}
	return existingChat{}, false
}

func (s *chatsAdminServer) callsTo(path string) []chatsAdminCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []chatsAdminCall
	for _, c := range s.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func newChatsAdminServer(t *testing.T) *chatsAdminServer {
	t.Helper()
	s := &chatsAdminServer{}
	users := map[string]string{
		"email:user@example.com":   testUserHUID,
		"login:example.com/ivanov": testUser2HUID,
	}
	record := func(r *http.Request) {
		var body map[string]any
		if r.Method == http.MethodPost {
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("%s: expected application/json, got %q", r.URL.Path, ct)
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("%s: decode body: %v", r.URL.Path, err)
			}
		}
		s.mu.Lock()
		s.calls = append(s.calls, chatsAdminCall{Path: r.URL.Path, Body: body})
		s.mu.Unlock()
	}
	writeUser := func(w http.ResponseWriter, huid string) {
		if huid == "" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"status":"error","reason":"user_not_found"}`)
			return
		}
		fmt.Fprintf(w, `{"status":"ok","result":{"user_huid":%q,"name":"User"}}`, huid)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		wantMethod := http.MethodGet
		switch r.URL.Path {
		case "/api/v3/botx/chats/create", "/api/v3/botx/chats/add_user", "/api/v3/botx/chats/remove_user", "/api/v3/botx/chats/add_admin":
			wantMethod = http.MethodPost
		}
		if r.Method != wantMethod {
			t.Errorf("%s: expected %s, got %s", r.URL.Path, wantMethod, r.Method)
		}
		record(r)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/botx/users/by_email":
			writeUser(w, users["email:"+r.URL.Query().Get("email")])
		case "/api/v3/botx/users/by_login":
			q := r.URL.Query()
			writeUser(w, users["login:"+q.Get("ad_domain")+"/"+q.Get("ad_login")])
		case "/api/v3/botx/chats/create":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"status":"ok","result":{"chat_id":%q}}`, testChatID)
		case "/api/v3/botx/chats/add_user", "/api/v3/botx/chats/remove_user", "/api/v3/botx/chats/add_admin":
			fmt.Fprint(w, `{"status":"ok","result":true}`)
		case "/api/v3/botx/chats/list":
			s.mu.Lock()
			list := make([]map[string]any, len(s.existing))
			for i, c := range s.existing {
				list[i] = map[string]any{"group_chat_id": c.ID, "name": c.Name, "chat_type": c.Type, "members": []string{}}
			}
			s.mu.Unlock()
			if err := json.NewEncoder(w).Encode(map[string]any{"status": "ok", "result": list}); err != nil {
				t.Errorf("encode chats list: %v", err)
			}
		case "/api/v3/botx/chats/info":
			if c, ok := s.findExisting(r.URL.Query().Get("group_chat_id")); ok {
				members := "[]"
				if !c.BotLeft {
					members = fmt.Sprintf(`[{"user_huid":%q,"user_kind":"botx","admin":true}]`, testFatherID)
				}
				fmt.Fprintf(w, `{"status":"ok","result":{"group_chat_id":%q,"name":%q,"chat_type":%q,"creator":%q,"members":%s}}`,
					c.ID, c.Name, c.Type, c.Creator, members)
				return
			}
			if r.URL.Query().Get("group_chat_id") != testChatID {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"status":"error","reason":"chat_not_found"}`)
				return
			}
			fmt.Fprintf(w, `{"status":"ok","result":{
				"group_chat_id":%q,"name":"routing-test-chat","description":"test",
				"chat_type":"group_chat","creator":%q,"shared_history":false,
				"inserted_at":"2026-10-12T10:39:44.975219Z","updated_at":"2026-10-12T10:39:44.975219Z",
				"members":[
					{"server_id":"s1","user_huid":%q,"user_kind":"cts_user","admin":false},
					{"server_id":"s1","user_huid":%q,"user_kind":"botx","admin":false},
					{"server_id":"s1","user_huid":%q,"user_kind":"botx","admin":true}
				]}}`, testChatID, testFatherID, testUserHUID, testAlertsBotID, testFatherID)
		default:
			http.NotFound(w, r)
		}
	})
	s.Server = httptest.NewServer(mux)
	return s
}

func writeChatsAdminConfig(t *testing.T, host string) string {
	t.Helper()
	return writeTestConfig(t, fmt.Sprintf(`
bots:
  father-bot:
    host: %s
    id: %s
    token: test-token
  alerts-bot:
    host: %s
    id: %s
    token: test-token
chats:
  routing-test: %s
`, host, testFatherID, host, testAlertsBotID, testChatID))
}

func huidsOf(body map[string]any, key string) []string {
	raw, _ := body[key].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}
	return out
}

// --- chats info ---

func TestChatsInfo_ShowsCreatorAndMembers(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsInfo([]string{"--config", cfgPath, "--bot", "alerts-bot", "--chat-id", "routing-test"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"creator:        " + testFatherID + " (bot: father-bot)",
		"inserted_at:    2026-10-12T10:39:44.975219Z",
		"members (3):",
		testUserHUID + "  cts_user",
		testFatherID + "  botx        admin  father-bot",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
	if calls := srv.callsTo("/api/v3/botx/chats/list"); len(calls) != 0 {
		t.Errorf("chats info must not use chats/list, got %d calls", len(calls))
	}
}

func TestChatsInfo_JSON(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsInfo([]string{"--config", cfgPath, "--bot", "alerts-bot", "--chat-id", testChatID, "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		GroupChatID string `json:"group_chat_id"`
		Creator     string `json:"creator"`
		InsertedAt  string `json:"inserted_at"`
		Members     []struct {
			UserHUID string `json:"user_huid"`
			UserKind string `json:"user_kind"`
			Admin    bool   `json:"admin"`
		} `json:"members"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got.GroupChatID != testChatID || got.Creator != testFatherID || got.InsertedAt == "" {
		t.Errorf("unexpected chat fields: %+v", got)
	}
	if len(got.Members) != 3 {
		t.Fatalf("expected 3 members, got %+v", got.Members)
	}
	last := got.Members[2]
	if last.UserHUID != testFatherID || last.UserKind != "botx" || !last.Admin {
		t.Errorf("unexpected member: %+v", last)
	}
}

func TestChatsInfo_NotFound(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsInfo([]string{"--config", cfgPath, "--bot", "alerts-bot", "--chat-id", testUser2HUID}, deps)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "chat_not_found") {
		t.Errorf("expected 404 chat_not_found error, got: %v", err)
	}
}

// --- chats create ---

func TestChatsCreate_ResolvesMembersAndBots(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsCreate([]string{
		"--config", cfgPath, "--bot", "father-bot",
		"--name", "alerts-demo", "--description", "Test alerts",
		"--members", `user@example.com,example.com\ivanov`,
		"--members", testUserHUID,
		"--add-bot", "alerts-bot",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != testChatID {
		t.Errorf("expected chat id on stdout, got %q", got)
	}

	calls := srv.callsTo("/api/v3/botx/chats/create")
	if len(calls) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(calls))
	}
	body := calls[0].Body
	if body["name"] != "alerts-demo" || body["description"] != "Test alerts" || body["chat_type"] != "group_chat" || body["shared_history"] != false {
		t.Errorf("unexpected create body: %v", body)
	}
	want := []string{testUserHUID, testUser2HUID, testAlertsBotID}
	if got := huidsOf(body, "members"); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("members = %v, want %v", got, want)
	}
}

func TestChatsCreate_JSONAndSharedHistory(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsCreate([]string{
		"--config", cfgPath, "--bot", "father-bot",
		"--name", "x", "--members", testUserHUID, "--shared-history", "--format", "json",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got chatCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got.ChatID != testChatID || !got.Created || len(got.Members) != 1 || got.Members[0].UserHUID != testUserHUID {
		t.Errorf("unexpected result: %+v", got)
	}
	if body := srv.callsTo("/api/v3/botx/chats/create")[0].Body; body["shared_history"] != true {
		t.Errorf("expected shared_history=true, got %v", body["shared_history"])
	}
}

func TestChatsCreate_UnresolvedMemberAbortsCreate(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsCreate([]string{
		"--config", cfgPath, "--bot", "father-bot",
		"--name", "x", "--members", "user@example.com,nobody@example.com,petrov",
	}, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "nobody@example.com") || !strings.Contains(msg, "petrov: AD domain unknown") {
		t.Errorf("error should name every unresolved member, got: %v", err)
	}
	if strings.Contains(msg, "user@example") {
		t.Errorf("resolved member must not be reported, got: %v", err)
	}
	if calls := srv.callsTo("/api/v3/botx/chats/create"); len(calls) != 0 {
		t.Errorf("create must not be called, got %d calls", len(calls))
	}
}

func TestChatsCreate_UnknownBot(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsCreate([]string{
		"--config", cfgPath, "--bot", "father-bot",
		"--name", "x", "--add-bot", "ghost-bot",
	}, deps)
	if err == nil || !strings.Contains(err.Error(), `unknown bot "ghost-bot"`) {
		t.Errorf("expected unknown bot error, got: %v", err)
	}
	if len(srv.callsTo("/api/v3/botx/chats/create")) != 0 {
		t.Error("create must not be called")
	}
}

func TestChatsCreate_ReturnsExistingOwnChat(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	srv.addExisting(
		existingChat{ID: testUser2HUID, Name: "alerts-demo", Type: chatTypeGroup, Creator: testUserHUID},
		existingChat{ID: testChatID, Name: "alerts-demo", Type: chatTypeGroup, Creator: strings.ToUpper(testFatherID)},
		existingChat{ID: testAlertsBotID, Name: "alerts-demo", Type: chatTypeVoexCall, Creator: testFatherID},
	)
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, stderr := testDeps()
	err := runChatsCreate([]string{
		"--config", cfgPath, "--bot", "father-bot",
		"--name", " alerts-demo ", "--members", "user@example.com", "--format", "json",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got chatCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got.ChatID != testChatID || got.Created || len(got.Members) != 0 {
		t.Errorf("expected existing chat, got %+v", got)
	}
	if !strings.Contains(stderr.String(), "already created by this bot") {
		t.Errorf("expected notice on stderr, got: %s", stderr.String())
	}
	if len(srv.callsTo("/api/v3/botx/chats/create")) != 0 {
		t.Error("create must not be called")
	}
	if n := len(srv.callsTo("/api/v3/botx/chats/info")); n != 2 {
		t.Errorf("expected chats/info only for group chats with the name, got %d calls", n)
	}
}

func TestChatsCreate_IgnoresSameNameByOtherCreator(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	srv.addExisting(existingChat{ID: testUser2HUID, Name: "alerts-demo", Type: chatTypeGroup, Creator: testUserHUID})
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsCreate([]string{"--config", cfgPath, "--bot", "father-bot", "--name", "alerts-demo", "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got chatCreateResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got.ChatID != testChatID || !got.Created {
		t.Errorf("expected a new chat, got %+v", got)
	}
}

func TestChatsCreate_SeveralOwnChatsWithName(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	srv.addExisting(
		existingChat{ID: testUser2HUID, Name: "alerts-demo", Type: chatTypeGroup, Creator: testFatherID},
		existingChat{ID: testAlertsBotID, Name: "alerts-demo", Type: chatTypeGroup, Creator: testFatherID},
	)
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsCreate([]string{"--config", cfgPath, "--bot", "father-bot", "--name", "alerts-demo"}, deps)
	if err == nil || !strings.Contains(err.Error(), testUser2HUID) || !strings.Contains(err.Error(), testAlertsBotID) {
		t.Errorf("expected error listing both chats, got: %v", err)
	}
	if stdout.Len() != 0 || len(srv.callsTo("/api/v3/botx/chats/create")) != 0 {
		t.Error("nothing must be printed or created")
	}
}

func TestChatsCreate_AllowDuplicate(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	srv.addExisting(existingChat{ID: testUser2HUID, Name: "alerts-demo", Type: chatTypeGroup, Creator: testFatherID})
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsCreate([]string{"--config", cfgPath, "--bot", "father-bot", "--name", "alerts-demo", "--allow-duplicate"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != testChatID {
		t.Errorf("expected new chat id, got %q", got)
	}
	if len(srv.callsTo("/api/v3/botx/chats/list")) != 0 {
		t.Error("--allow-duplicate must skip the lookup")
	}
	if len(srv.callsTo("/api/v3/botx/chats/create")) != 1 {
		t.Error("expected one create call")
	}
}

func TestChatsCreate_NameRequired(t *testing.T) {
	deps, _, _ := testDeps()
	err := runChatsCreate([]string{"--members", testUserHUID}, deps)
	if err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Errorf("expected --name error, got: %v", err)
	}
}

// --- chats add-user / remove-user / add-admin ---

func TestChatsMembersActions(t *testing.T) {
	cases := []struct {
		action chatMembersAction
		path   string
		done   string
	}{
		{chatsAddUserAction, "/api/v3/botx/chats/add_user", "Added 2 member(s) to chat"},
		{chatsRemoveUserAction, "/api/v3/botx/chats/remove_user", "Removed 2 member(s) from chat"},
		{chatsAddAdminAction, "/api/v3/botx/chats/add_admin", "Made 2 member(s) admins in chat"},
	}
	for _, tc := range cases {
		t.Run(tc.action.name, func(t *testing.T) {
			srv := newChatsAdminServer(t)
			defer srv.Close()
			cfgPath := writeChatsAdminConfig(t, srv.URL)

			deps, stdout, _ := testDeps()
			err := runChatsMembersAction(tc.action, []string{
				"--config", cfgPath, "--bot", "father-bot",
				"--chat-id", "routing-test", "--ad-domain", "example.com",
				"user@example.com", "ivanov", testUserHUID,
			}, deps)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			calls := srv.callsTo(tc.path)
			if len(calls) != 1 {
				t.Fatalf("expected 1 call to %s, got %d", tc.path, len(calls))
			}
			body := calls[0].Body
			if body["group_chat_id"] != testChatID {
				t.Errorf("group_chat_id = %v, want %s (alias resolved)", body["group_chat_id"], testChatID)
			}
			want := []string{testUserHUID, testUser2HUID}
			if got := huidsOf(body, "user_huids"); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("user_huids = %v, want %v", got, want)
			}
			out := stdout.String()
			if !strings.Contains(out, tc.done) || !strings.Contains(out, testUser2HUID+"  (ivanov)") {
				t.Errorf("unexpected output:\n%s", out)
			}
		})
	}
}

func TestChatsMembersAction_JSON(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", cfgPath, "--bot", "father-bot",
		"--chat-id", testChatID, "--format", "json", "user@example.com",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got chatMembersResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got.ChatID != testChatID || len(got.Members) != 1 || got.Members[0].Ref != "user@example.com" || got.Members[0].UserHUID != testUserHUID {
		t.Errorf("unexpected result: %+v", got)
	}
}

func TestChatsMembersAction_UnresolvedMemberAborts(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsRemoveUserAction, []string{
		"--config", cfgPath, "--bot", "father-bot",
		"--chat-id", testChatID, "user@example.com", "nobody@example.com",
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "nobody@example.com") {
		t.Errorf("expected unresolved member error, got: %v", err)
	}
	if len(srv.callsTo("/api/v3/botx/chats/remove_user")) != 0 {
		t.Error("remove_user must not be called")
	}
}

func TestChatsMembersAction_RequiresChatIDAndMembers(t *testing.T) {
	deps, _, _ := testDeps()
	if err := runChatsMembersAction(chatsAddUserAction, []string{"--chat-id", testChatID}, deps); err == nil || !strings.Contains(err.Error(), "at least one member") {
		t.Errorf("expected member error, got: %v", err)
	}
	if err := runChatsMembersAction(chatsAddUserAction, []string{testUserHUID}, deps); err == nil || !strings.Contains(err.Error(), "--chat-id is required") {
		t.Errorf("expected --chat-id error, got: %v", err)
	}
}

func TestChatsMembersAction_APIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v3/botx/chats/add_admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"status":"error","reason":"admins_not_changed"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddAdminAction, []string{
		"--config", cfgPath, "--bot", "father-bot", "--chat-id", testChatID, testUserHUID,
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "admins_not_changed") {
		t.Errorf("expected BotX reason in error, got: %v", err)
	}
}

func TestRunChats_DispatchesAdminCommands(t *testing.T) {
	for _, sub := range []string{"create", "add-user", "remove-user", "add-admin"} {
		deps, _, stderr := testDeps()
		if err := runChats([]string{sub, "--help"}, deps); err != nil {
			t.Errorf("%s --help: %v", sub, err)
		}
		if !strings.Contains(stderr.String(), "express-botx chats "+sub) {
			t.Errorf("%s: usage not printed:\n%s", sub, stderr.String())
		}
	}
}

func TestChatsInfo_MalformedResponseFailsWithoutOutput(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/botx/chats/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"ok","result":null}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsInfo([]string{"--config", cfgPath, "--bot", "alerts-bot", "--chat-id", testChatID, "--format", "json"}, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	if stdout.Len() != 0 {
		t.Errorf("expected no stdout, got: %s", stdout.String())
	}
}

func TestChatsMembersAction_DedupesHUIDCaseInsensitive(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", cfgPath, "--bot", "father-bot", "--chat-id", testChatID,
		strings.ToUpper(testUserHUID), "user@example.com",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := huidsOf(srv.callsTo("/api/v3/botx/chats/add_user")[0].Body, "user_huids")
	if len(got) != 1 || got[0] != testUserHUID {
		t.Errorf("expected single lowercase HUID, got %v", got)
	}
}

func TestChatsMembersAction_EmptyLoginAfterDomain(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", cfgPath, "--bot", "father-bot", "--chat-id", testChatID, `example.com\`,
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "empty AD login") {
		t.Errorf("expected empty login error, got: %v", err)
	}
	if len(srv.callsTo("/api/v3/botx/users/by_login")) != 0 {
		t.Error("by_login must not be called")
	}
}

func newRefreshingServer(t *testing.T) (*httptest.Server, *int, map[string]int) {
	t.Helper()
	var mu sync.Mutex
	tokenVersion := 0
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokenVersion++
			fmt.Fprintf(w, `{"status":"ok","result":"token-v%d"}`, tokenVersion)
			return
		}
		hits[r.URL.Path]++
		if r.Header.Get("Authorization") != "Bearer token-v2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v3/botx/users/by_email":
			fmt.Fprintf(w, `{"status":"ok","result":{"user_huid":%q}}`, testUserHUID)
		case "/api/v3/botx/chats/add_user":
			fmt.Fprint(w, `{"status":"ok","result":true}`)
		case "/api/v3/botx/chats/create":
			fmt.Fprintf(w, `{"status":"ok","result":{"chat_id":%q}}`, testChatID)
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &tokenVersion, hits
}

func writeSecretConfig(t *testing.T, host string) string {
	t.Helper()
	return writeTestConfig(t, fmt.Sprintf(`
bots:
  father-bot:
    host: %s
    id: %s
    secret: test-secret
cache:
  type: none
`, host, testFatherID))
}

func TestChatsMembersAction_RefreshesTokenOn401(t *testing.T) {
	srv, tokenVersion, hits := newRefreshingServer(t)
	defer srv.Close()

	deps, stdout, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", writeSecretConfig(t, srv.URL), "--chat-id", testChatID, testUserHUID,
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *tokenVersion != 2 || hits["/api/v3/botx/chats/add_user"] != 2 {
		t.Errorf("expected one refresh and one retry, got tokens=%d calls=%d", *tokenVersion, hits["/api/v3/botx/chats/add_user"])
	}
	if !strings.Contains(stdout.String(), "Added 1 member(s)") {
		t.Errorf("unexpected output: %s", stdout.String())
	}
}

func TestChatsCreate_RefreshesTokenOn401(t *testing.T) {
	srv, tokenVersion, hits := newRefreshingServer(t)
	defer srv.Close()

	deps, stdout, _ := testDeps()
	err := runChatsCreate([]string{
		"--config", writeSecretConfig(t, srv.URL), "--name", "x", "--members", testUserHUID, "--allow-duplicate",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *tokenVersion != 2 || hits["/api/v3/botx/chats/create"] != 2 {
		t.Errorf("expected one refresh and one retry, got tokens=%d calls=%d", *tokenVersion, hits["/api/v3/botx/chats/create"])
	}
	if got := strings.TrimSpace(stdout.String()); got != testChatID {
		t.Errorf("expected chat id, got %q", got)
	}
}

func TestChatsMembersAction_StaticToken401NoRefresh(t *testing.T) {
	srv, tokenVersion, hits := newRefreshingServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", cfgPath, "--bot", "father-bot", "--chat-id", testChatID, "user@example.com",
	}, deps)
	if !errors.Is(err, botapi.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
	if *tokenVersion != 0 || hits["/api/v3/botx/users/by_email"] != 1 {
		t.Errorf("static token must not refresh: tokens=%d lookups=%d", *tokenVersion, hits["/api/v3/botx/users/by_email"])
	}
}

func TestChatsMembersAction_VerboseFlag(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeChatsAdminConfig(t, srv.URL)
	prev := vlog.Level
	vlog.Level = 0
	t.Cleanup(func() { vlog.Level = prev })

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", cfgPath, "--bot", "father-bot", "--chat-id", testChatID, "--verbose", "2", testUserHUID,
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vlog.Level != 2 {
		t.Errorf("expected --verbose to set level 2, got %d", vlog.Level)
	}
}

func TestChatsInfo_BotNamesIgnoreUUIDCase(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  father-bot:
    host: %s
    id: %s
    token: test-token
`, srv.URL, strings.ToUpper(testFatherID)))

	deps, stdout, _ := testDeps()
	if err := runChatsInfo([]string{"--config", cfgPath, "--chat-id", testChatID}, deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), testFatherID+" (bot: father-bot)") {
		t.Errorf("expected bot name for creator, got:\n%s", stdout.String())
	}
}

func TestChatsCreate_UnreliableChatsListBlocksCreate(t *testing.T) {
	bodies := map[string]string{
		"status error":     `{"status":"error","reason":"unavailable"}`,
		"empty object":     `{}`,
		"null":             `null`,
		"null result":      `{"status":"ok","result":null}`,
		"incomplete entry": fmt.Sprintf(`{"status":"ok","result":[{"group_chat_id":%q}]}`, testUser2HUID),
		"no name":          fmt.Sprintf(`{"status":"ok","result":[{"group_chat_id":%q,"chat_type":"group_chat"}]}`, testUser2HUID),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var creates int
			var mu sync.Mutex
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v3/botx/chats/list", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, body)
			})
			mux.HandleFunc("POST /api/v3/botx/chats/create", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				creates++
				mu.Unlock()
				fmt.Fprintf(w, `{"status":"ok","result":{"chat_id":%q}}`, testChatID)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			deps, stdout, _ := testDeps()
			err := runChatsCreate([]string{"--config", writeChatsAdminConfig(t, srv.URL), "--bot", "father-bot", "--name", "alerts-demo"}, deps)
			if err == nil {
				t.Error("expected error")
			}
			if creates != 0 || stdout.Len() != 0 {
				t.Errorf("create must not run: creates=%d stdout=%q", creates, stdout.String())
			}
		})
	}
}

func TestChatsMembersAction_RefreshesTokenOn401DuringLookup(t *testing.T) {
	srv, tokenVersion, hits := newRefreshingServer(t)
	defer srv.Close()

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", writeSecretConfig(t, srv.URL), "--chat-id", testChatID, "user@example.com",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *tokenVersion != 2 || hits["/api/v3/botx/users/by_email"] != 2 || hits["/api/v3/botx/chats/add_user"] != 1 {
		t.Errorf("expected refresh after lookup 401 and a single mutation, got tokens=%d lookups=%d adds=%d",
			*tokenVersion, hits["/api/v3/botx/users/by_email"], hits["/api/v3/botx/chats/add_user"])
	}
}

func TestChatsMembersAction_RetriesOnlyOnce(t *testing.T) {
	var tokens, adds int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokens++
			fmt.Fprintf(w, `{"status":"ok","result":"token-v%d"}`, tokens)
			return
		}
		adds++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", writeSecretConfig(t, srv.URL), "--chat-id", testChatID, testUserHUID,
	}, deps)
	if !errors.Is(err, botapi.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got: %v", err)
	}
	if tokens != 2 || adds != 2 {
		t.Errorf("expected exactly one refresh and one retry, got tokens=%d adds=%d", tokens, adds)
	}
}

func TestChatsMembersAction_NoRetryOn5xx(t *testing.T) {
	var tokens, adds int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			tokens++
			fmt.Fprint(w, `{"status":"ok","result":"token-v1"}`)
			return
		}
		adds++
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	deps, _, _ := testDeps()
	err := runChatsMembersAction(chatsAddUserAction, []string{
		"--config", writeSecretConfig(t, srv.URL), "--chat-id", testChatID, testUserHUID,
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("expected 502 error, got: %v", err)
	}
	if tokens != 1 || adds != 1 {
		t.Errorf("5xx must not be retried, got tokens=%d adds=%d", tokens, adds)
	}
}

func TestChatsCreate_OwnChatWithoutBotAdminFails(t *testing.T) {
	srv := newChatsAdminServer(t)
	defer srv.Close()
	srv.addExisting(existingChat{ID: testUser2HUID, Name: "alerts-demo", Type: chatTypeGroup, Creator: testFatherID, BotLeft: true})
	cfgPath := writeChatsAdminConfig(t, srv.URL)

	deps, stdout, _ := testDeps()
	err := runChatsCreate([]string{"--config", cfgPath, "--bot", "father-bot", "--name", "alerts-demo"}, deps)
	if err == nil || !strings.Contains(err.Error(), "no longer its admin") || !strings.Contains(err.Error(), testUser2HUID) {
		t.Errorf("expected error about lost admin rights, got: %v", err)
	}
	if stdout.Len() != 0 || len(srv.callsTo("/api/v3/botx/chats/create")) != 0 {
		t.Error("nothing must be printed or created")
	}
}
