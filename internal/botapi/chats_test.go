package botapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const chatsTestID = "aaaaaaaa-1111-4111-8111-000000000001"

func newStaticServer(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "tok", 5*time.Second)
}

func TestChatMembersMethods_StatusErrorWithHTTP200(t *testing.T) {
	methods := map[string]func(*Client) error{
		"add user":    func(c *Client) error { return c.AddUsers(context.Background(), chatsTestID, []string{"u"}) },
		"remove user": func(c *Client) error { return c.RemoveUsers(context.Background(), chatsTestID, []string{"u"}) },
		"add admin":   func(c *Client) error { return c.AddAdmins(context.Background(), chatsTestID, []string{"u"}) },
	}
	for name, call := range methods {
		t.Run(name, func(t *testing.T) {
			c := newStaticServer(t, http.StatusOK, `{"status":"error","reason":"admins_not_changed"}`)
			err := call(c)
			if err == nil || !strings.Contains(err.Error(), "admins_not_changed") {
				t.Errorf("expected status error with reason, got: %v", err)
			}
		})
	}
}

func TestChatMembersMethods_InvalidBodyWithHTTP200(t *testing.T) {
	c := newStaticServer(t, http.StatusOK, `<html>maintenance</html>`)
	if err := c.AddAdmins(context.Background(), chatsTestID, []string{"u"}); err == nil {
		t.Error("expected error for non-JSON 200 body")
	}
}

func TestChatMembersMethods_SuccessBodies(t *testing.T) {
	for _, body := range []string{`{"status":"ok","result":true}`, ``, `{}`} {
		c := newStaticServer(t, http.StatusOK, body)
		if err := c.AddUsers(context.Background(), chatsTestID, []string{"u"}); err != nil {
			t.Errorf("body %q: unexpected error: %v", body, err)
		}
	}
	c := newStaticServer(t, http.StatusNoContent, "")
	if err := c.RemoveUsers(context.Background(), chatsTestID, []string{"u"}); err != nil {
		t.Errorf("204: unexpected error: %v", err)
	}
}

func TestGetChatInfo_RejectsUnreliableResponses(t *testing.T) {
	cases := map[string]string{
		"empty body":     ``,
		"empty object":   `{}`,
		"null":           `null`,
		"null result":    `{"status":"ok","result":null}`,
		"status error":   `{"status":"error","reason":"chat_not_found"}`,
		"other chat":     `{"status":"ok","result":{"group_chat_id":"11111111-2222-3333-4444-555555555555","members":[]}}`,
		"no members":     fmt.Sprintf(`{"status":"ok","result":{"group_chat_id":%q,"creator":"b"}}`, chatsTestID),
		"null members":   fmt.Sprintf(`{"status":"ok","result":{"group_chat_id":%q,"creator":"b","members":null}}`, chatsTestID),
		"no creator":     fmt.Sprintf(`{"status":"ok","result":{"group_chat_id":%q,"members":[]}}`, chatsTestID),
		"not json":       `<html>`,
		"result not obj": `{"status":"ok","result":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			c := newStaticServer(t, http.StatusOK, body)
			if chat, err := c.GetChatInfo(context.Background(), chatsTestID); err == nil {
				t.Errorf("expected error, got %+v", chat)
			}
		})
	}
}

func TestGetChatInfo_EmptyMembersIsValid(t *testing.T) {
	c := newStaticServer(t, http.StatusOK, fmt.Sprintf(`{"status":"ok","result":{"group_chat_id":%q,"creator":null,"members":[]}}`, strings.ToUpper(chatsTestID)))
	chat, err := c.GetChatInfo(context.Background(), chatsTestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.Members == nil || len(chat.Members) != 0 {
		t.Errorf("expected confirmed empty members, got %#v", chat.Members)
	}
	if chat.Creator != nil {
		t.Errorf("expected null creator, got %q", *chat.Creator)
	}
}

func TestGetChatInfo_RejectsIncompleteMembers(t *testing.T) {
	members := map[string]string{
		"null element":  `[null]`,
		"empty object":  `[{}]`,
		"no admin":      `[{"user_huid":"u1","user_kind":"cts_user"}]`,
		"null admin":    `[{"user_huid":"u1","user_kind":"cts_user","admin":null}]`,
		"no user_kind":  `[{"user_huid":"u1","admin":false}]`,
		"empty huid":    `[{"user_huid":"","user_kind":"cts_user","admin":false}]`,
		"second broken": `[{"user_huid":"u1","user_kind":"botx","admin":true},{"user_huid":"u2"}]`,
	}
	for name, m := range members {
		t.Run(name, func(t *testing.T) {
			c := newStaticServer(t, http.StatusOK, fmt.Sprintf(`{"status":"ok","result":{"group_chat_id":%q,"creator":"b","members":%s}}`, chatsTestID, m))
			if chat, err := c.GetChatInfo(context.Background(), chatsTestID); err == nil {
				t.Errorf("expected error, got %+v", chat)
			}
		})
	}
}

func TestGetChatInfo_ParsesMembersAndCreator(t *testing.T) {
	c := newStaticServer(t, http.StatusOK, fmt.Sprintf(`{"status":"ok","result":{"group_chat_id":%q,"creator":"b",
		"members":[{"user_huid":"u1","user_kind":"cts_user","admin":false,"server_id":"s"},{"user_huid":"b","user_kind":"botx","admin":true}]}}`, chatsTestID))
	chat, err := c.GetChatInfo(context.Background(), chatsTestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat.Creator == nil || *chat.Creator != "b" {
		t.Errorf("unexpected creator: %v", chat.Creator)
	}
	want := []ChatMember{{UserHUID: "u1", UserKind: "cts_user", ServerID: "s"}, {UserHUID: "b", UserKind: "botx", Admin: true}}
	if fmt.Sprint(chat.Members) != fmt.Sprint(want) {
		t.Errorf("members = %+v, want %+v", chat.Members, want)
	}
}

func TestListChats_RejectsUnreliableResponses(t *testing.T) {
	for _, body := range []string{
		`{"status":"error","reason":"unavailable"}`,
		`{}`,
		`null`,
		`{"status":"ok","result":null}`,
	} {
		c := newStaticServer(t, http.StatusOK, body)
		if chats, err := c.ListChats(context.Background()); err == nil {
			t.Errorf("body %s: expected error, got %+v", body, chats)
		}
	}
	c := newStaticServer(t, http.StatusOK, `{"status":"ok","result":[]}`)
	if chats, err := c.ListChats(context.Background()); err != nil || chats == nil || len(chats) != 0 {
		t.Errorf("empty result must be valid, got %v, %v", chats, err)
	}
}

func TestListChats_UnauthorizedIsTyped(t *testing.T) {
	c := newStaticServer(t, http.StatusUnauthorized, ``)
	if _, err := c.ListChats(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestCreateChat_StatusErrorWithHTTP200(t *testing.T) {
	c := newStaticServer(t, http.StatusOK, `{"status":"error","reason":"chat_creation_is_prohibited"}`)
	_, err := c.CreateChat(context.Background(), &CreateChatRequest{Name: "x", ChatType: "group_chat"})
	if err == nil || !strings.Contains(err.Error(), "chat_creation_is_prohibited") {
		t.Errorf("expected status error, got: %v", err)
	}
}

func TestGetUserByEmail_StatusError(t *testing.T) {
	c := newStaticServer(t, http.StatusOK, `{"status":"error","result":{"user_huid":"dddddddd-4444-4444-8444-000000000004"}}`)
	if user, err := c.GetUserByEmail(context.Background(), "a@b"); err == nil {
		t.Errorf("expected error, got %+v", user)
	}
}
