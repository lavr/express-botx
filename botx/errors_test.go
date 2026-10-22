package botx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateErrorBody(t *testing.T) {
	tests := []struct {
		name          string
		body          []byte
		wantTruncated bool
		wantMarker    string
	}{
		{
			name:          "short body is passed through",
			body:          []byte(`{"reason":"nope"}`),
			wantTruncated: false,
		},
		{
			name:          "oversized body is marked",
			body:          bytes.Repeat([]byte("x"), maxErrorBodyBytes+100),
			wantTruncated: true,
			wantMarker:    "truncated to",
		},
		{
			// A three-byte rune offset by one ASCII byte: a raw data[:N] cut
			// would land mid-rune, which a two-byte rune on an even boundary
			// could not reveal.
			name:          "cut lands on a rune boundary",
			body:          append([]byte("x"), bytes.Repeat([]byte("→"), maxErrorBodyBytes)...),
			wantTruncated: true,
			wantMarker:    "truncated to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := truncateErrorBody(tt.body)
			if truncated != tt.wantTruncated {
				t.Fatalf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if tt.wantMarker != "" && !strings.Contains(got, tt.wantMarker) {
				t.Errorf("missing truncation marker in %q", got[max(0, len(got)-60):])
			}
			if !utf8.ValidString(got) {
				t.Errorf("log line is not valid UTF-8")
			}
		})
	}
}

func TestAPIError_TypedErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		call       func(*Client) error
		want       []error
		notWant    []error
		wantReason string
	}{
		{
			name:       "chat not found",
			statusCode: 404,
			body:       `{"status":"error","reason":"chat_not_found","errors":[]}`,
			call:       func(c *Client) error { _, err := c.GetChatInfo(context.Background(), testChatID); return err },
			want:       []error{ErrNotFound},
			notWant:    []error{ErrInvalidResponse, ErrUnauthorized},
			wantReason: "chat_not_found",
		},
		{
			name:       "user not found",
			statusCode: 404,
			body:       `{"status":"error","reason":"user_not_found"}`,
			call: func(c *Client) error {
				_, err := c.GetUserByEmail(context.Background(), "nobody@example.com")
				return err
			},
			want:       []error{ErrNotFound},
			wantReason: "user_not_found",
		},
		{
			name:       "bot is not a chat admin",
			statusCode: 403,
			body:       `{"status":"error","reason":"no_permission_for_operation","errors":["Sender is not chat admin"]}`,
			call:       func(c *Client) error { return c.RemoveUsers(context.Background(), testChatID, []string{testUserID}) },
			want:       []error{ErrNoPermission},
			notWant:    []error{ErrNotFound, ErrInvalidResponse},
			wantReason: "no_permission_for_operation",
		},
		{
			name:       "admins not changed",
			statusCode: 400,
			body:       `{"status":"error","reason":"admins_not_changed"}`,
			call:       func(c *Client) error { return c.AddAdmins(context.Background(), testChatID, []string{testUserID}) },
			want:       []error{ErrAdminsNotChanged},
			notWant:    []error{ErrInvalidResponse},
			wantReason: "admins_not_changed",
		},
		{
			name:       "status error with HTTP 200",
			statusCode: 200,
			body:       `{"status":"error","reason":"chat_creation_is_prohibited"}`,
			call: func(c *Client) error {
				_, err := c.CreateChat(context.Background(), &CreateChatRequest{Name: "alerts-demo", ChatType: ChatTypeGroup})
				return err
			},
			want:       []error{ErrInvalidResponse},
			notWant:    []error{ErrNotFound},
			wantReason: "chat_creation_is_prohibited",
		},
		{
			name:       "bare 403 is not ErrNoPermission",
			statusCode: 403,
			body:       `{"status":"error","reason":"bot_is_not_a_chat_member"}`,
			call:       func(c *Client) error { return c.AddUsers(context.Background(), testChatID, []string{testUserID}) },
			notWant:    []error{ErrNoPermission, ErrNotFound},
			wantReason: "bot_is_not_a_chat_member",
		},
		{
			name:       "send without permission",
			statusCode: 200,
			body:       `{"status":"error","reason":"no_permission_for_operation"}`,
			call: func(c *Client) error {
				_, err := c.Send(context.Background(), &SendRequest{GroupChatID: testChatID})
				return err
			},
			want:       []error{ErrNoPermission, ErrInvalidResponse},
			wantReason: "no_permission_for_operation",
		},
		{
			name:       "server error",
			statusCode: 502,
			body:       `<html>bad gateway</html>`,
			call:       func(c *Client) error { _, err := c.ListChats(context.Background()); return err },
			notWant:    []error{ErrInvalidResponse, ErrNotFound, ErrUnauthorized, ErrNoPermission, ErrAdminsNotChanged},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				io.WriteString(w, tt.body) //nolint:errcheck
			}))
			defer srv.Close()

			err := tt.call(NewWithToken(srv.URL, "tok"))
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want *APIError", err)
			}
			if apiErr.StatusCode != tt.statusCode || apiErr.Reason != tt.wantReason {
				t.Errorf("StatusCode, Reason = %d, %q; want %d, %q", apiErr.StatusCode, apiErr.Reason, tt.statusCode, tt.wantReason)
			}
			if apiErr.Body != tt.body {
				t.Errorf("Body = %q, want %q", apiErr.Body, tt.body)
			}
			for _, target := range tt.want {
				if !errors.Is(err, target) {
					t.Errorf("errors.Is(%v, %v) = false", err, target)
				}
			}
			for _, target := range tt.notWant {
				if errors.Is(err, target) {
					t.Errorf("errors.Is(%v, %v) = true", err, target)
				}
			}
		})
	}
}

func TestInvalidResponses(t *testing.T) {
	member := `{"user_huid":"` + testUserID + `","user_kind":"user","admin":false}`
	info := func(fields string) string {
		return `{"status":"ok","result":{"group_chat_id":"` + testChatID + `","name":"alerts-demo"` + fields + `}}`
	}
	tests := []struct {
		name string
		body string
		call func(*Client) error
	}{
		{"chat info without result", `{"status":"ok"}`, getInfo},
		{"chat info for another chat", strings.Replace(info(`,"creator":null,"members":[]`), testChatID, testUserID, 1), getInfo},
		{"chat info without creator", info(`,"members":[` + member + `]`), getInfo},
		{"chat info without members", info(`,"creator":null`), getInfo},
		{"chat info with members null", info(`,"creator":null,"members":null`), getInfo},
		{"chat info member without admin", info(`,"creator":null,"members":[{"user_huid":"` + testUserID + `","user_kind":"user"}]`), getInfo},
		{"chat info that is not JSON", `<html>gateway</html>`, getInfo},
		{"chat info with an empty body", ``, getInfo},
		{"create chat without chat_id", `{"status":"ok","result":{}}`, func(c *Client) error {
			_, err := c.CreateChat(context.Background(), &CreateChatRequest{Name: "alerts-demo", ChatType: ChatTypeGroup})
			return err
		}},
		{"add user that is not JSON", `<html>gateway</html>`, func(c *Client) error {
			return c.AddUsers(context.Background(), testChatID, []string{testUserID})
		}},
		{"list without result", `{"status":"ok"}`, func(c *Client) error { _, err := c.ListChats(context.Background()); return err }},
		{"list with status error", `{"status":"error","result":[]}`, func(c *Client) error { _, err := c.ListChats(context.Background()); return err }},
		{"user with status error", `{"status":"error","reason":"x"}`, func(c *Client) error {
			_, err := c.GetUserByHUID(context.Background(), testUserID)
			return err
		}},
		{"user with result null", `{"status":"ok","result":null}`, func(c *Client) error {
			_, err := c.GetUserByEmail(context.Background(), "user@example.com")
			return err
		}},
		{"user without user_huid", `{"status":"ok","result":{"name":"Test User"}}`, func(c *Client) error {
			_, err := c.GetUserByADLogin(context.Background(), "user", "EXAMPLE")
			return err
		}},
		{"user lookup by HUID answered for another user", `{"status":"ok","result":{"user_huid":"` + testChatID + `"}}`, func(c *Client) error {
			_, err := c.GetUserByHUID(context.Background(), testUserID)
			return err
		}},
		{"list with a chat without group_chat_id", `{"status":"ok","result":[{}]}`, func(c *Client) error {
			_, err := c.ListChats(context.Background())
			return err
		}},
		{"send that is not JSON", `<html>ok</html>`, func(c *Client) error {
			_, err := c.Send(context.Background(), &SendRequest{GroupChatID: testChatID})
			return err
		}},
		{"send with status error", `{"status":"error","reason":"x"}`, func(c *Client) error {
			_, err := c.Send(context.Background(), &SendRequest{GroupChatID: testChatID})
			return err
		}},
		{"edit with status error", `{"status":"error"}`, func(c *Client) error {
			return c.EditMessage(context.Background(), &EditRequest{SyncID: "sync-1"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tt.body) //nolint:errcheck
			}))
			defer srv.Close()

			err := tt.call(NewWithToken(srv.URL, "tok"))
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v, want ErrInvalidResponse", err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusOK {
				t.Errorf("error = %#v, want *APIError with HTTP 200", err)
			}
		})
	}
}

func getInfo(c *Client) error {
	_, err := c.GetChatInfo(context.Background(), testChatID)
	return err
}

func TestCutResponseBodyKeepsHTTPStatus(t *testing.T) {
	calls := map[string]func(*Client) error{
		"list chats": func(c *Client) error { _, err := c.ListChats(context.Background()); return err },
		"chat info":  getInfo,
		"get user": func(c *Client) error {
			_, err := c.GetUserByEmail(context.Background(), "user@example.com")
			return err
		},
		"send": func(c *Client) error {
			_, err := c.Send(context.Background(), &SendRequest{GroupChatID: testChatID})
			return err
		},
		"edit": func(c *Client) error { return c.EditMessage(context.Background(), &EditRequest{SyncID: "sync-1"}) },
	}
	statuses := []struct {
		code int
		want error
	}{
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusOK, ErrInvalidResponse},
	}
	for name, call := range calls {
		for _, st := range statuses {
			t.Run(fmt.Sprintf("%s %d", name, st.code), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(st.code)
					_, _ = io.WriteString(w, "short")
				}))
				defer srv.Close()

				err := call(NewWithToken(srv.URL, "tok"))
				if !errors.Is(err, st.want) {
					t.Errorf("error = %v, want %v", err, st.want)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != st.code {
					t.Errorf("error = %#v, want *APIError with HTTP %d", err, st.code)
				}
			})
		}
	}
}
