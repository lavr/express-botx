package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

// ChatMember is a chat participant as returned by chats/info.
type ChatMember struct {
	UserHUID string `json:"user_huid"`
	UserKind string `json:"user_kind"`
	Admin    bool   `json:"admin"`
	ServerID string `json:"server_id,omitempty"`
}

// ChatDetails is the full chat description returned by chats/info.
type ChatDetails struct {
	GroupChatID   string       `json:"group_chat_id"`
	Name          string       `json:"name"`
	Description   *string      `json:"description"`
	ChatType      string       `json:"chat_type"`
	Creator       *string      `json:"creator"`
	InsertedAt    string       `json:"inserted_at,omitempty"`
	UpdatedAt     string       `json:"updated_at,omitempty"`
	SharedHistory bool         `json:"shared_history"`
	Members       []ChatMember `json:"members"`
}

// CreateChatRequest is the body of POST /api/v3/botx/chats/create.
type CreateChatRequest struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	ChatType      string   `json:"chat_type"`
	Members       []string `json:"members"`
	SharedHistory bool     `json:"shared_history"`
}

type chatMemberWire struct {
	UserHUID *string `json:"user_huid"`
	UserKind *string `json:"user_kind"`
	Admin    *bool   `json:"admin"`
	ServerID string  `json:"server_id"`
}

type chatDetailsWire struct {
	ChatDetails
	Creator json.RawMessage   `json:"creator"`
	Members []*chatMemberWire `json:"members"`
}

// toDetails rejects a chats/info result that would read as wrong membership:
// absent creator, null or incomplete members.
func (w *chatDetailsWire) toDetails() (*ChatDetails, error) {
	d := w.ChatDetails
	switch string(w.Creator) {
	case "":
		return nil, fmt.Errorf("response has no creator")
	case "null":
		d.Creator = nil
	default:
		var creator string
		if err := json.Unmarshal(w.Creator, &creator); err != nil {
			return nil, fmt.Errorf("creator: %w", err)
		}
		d.Creator = &creator
	}
	if w.Members == nil {
		return nil, fmt.Errorf("response has no members")
	}
	d.Members = make([]ChatMember, len(w.Members))
	for i, m := range w.Members {
		if m == nil || m.UserHUID == nil || *m.UserHUID == "" || m.UserKind == nil || *m.UserKind == "" || m.Admin == nil {
			return nil, fmt.Errorf("member %d is incomplete: user_huid, user_kind and admin are required", i)
		}
		d.Members[i] = ChatMember{UserHUID: *m.UserHUID, UserKind: *m.UserKind, Admin: *m.Admin, ServerID: m.ServerID}
	}
	return &d, nil
}

type chatMembersRequest struct {
	GroupChatID string   `json:"group_chat_id"`
	UserHUIDs   []string `json:"user_huids"`
}

// GetChatInfo returns the full description of a chat via chats/info.
func (c *Client) GetChatInfo(ctx context.Context, chatID string) (*ChatDetails, error) {
	var resp struct {
		Result *chatDetailsWire `json:"result"`
	}
	path := "/api/v3/botx/chats/info?group_chat_id=" + url.QueryEscape(chatID)
	if err := c.callJSON(ctx, "chat info", http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Result == nil {
		return nil, fmt.Errorf("chat info: response has no result")
	}
	if !strings.EqualFold(resp.Result.GroupChatID, chatID) {
		return nil, fmt.Errorf("chat info: response is for chat %q, requested %q", resp.Result.GroupChatID, chatID)
	}
	details, err := resp.Result.toDetails()
	if err != nil {
		return nil, fmt.Errorf("chat info: %w", err)
	}
	return details, nil
}

// CreateChat creates a chat and returns its ID.
func (c *Client) CreateChat(ctx context.Context, req *CreateChatRequest) (string, error) {
	var resp struct {
		Result struct {
			ChatID string `json:"chat_id"`
		} `json:"result"`
	}
	if err := c.callJSON(ctx, "create chat", http.MethodPost, "/api/v3/botx/chats/create", req, &resp); err != nil {
		return "", err
	}
	if resp.Result.ChatID == "" {
		return "", fmt.Errorf("create chat: response has no chat_id")
	}
	return resp.Result.ChatID, nil
}

// AddUsers adds users to a chat.
func (c *Client) AddUsers(ctx context.Context, chatID string, huids []string) error {
	return c.callJSON(ctx, "add user", http.MethodPost, "/api/v3/botx/chats/add_user",
		chatMembersRequest{GroupChatID: chatID, UserHUIDs: huids}, nil)
}

// RemoveUsers removes users from a chat.
func (c *Client) RemoveUsers(ctx context.Context, chatID string, huids []string) error {
	return c.callJSON(ctx, "remove user", http.MethodPost, "/api/v3/botx/chats/remove_user",
		chatMembersRequest{GroupChatID: chatID, UserHUIDs: huids}, nil)
}

// AddAdmins grants chat admin rights to chat members.
func (c *Client) AddAdmins(ctx context.Context, chatID string, huids []string) error {
	return c.callJSON(ctx, "add admin", http.MethodPost, "/api/v3/botx/chats/add_admin",
		chatMembersRequest{GroupChatID: chatID, UserHUIDs: huids}, nil)
}

// callJSON sends an optional JSON body and decodes a 2xx JSON response into out.
// A response whose "status" is present and not "ok" is an error whatever the HTTP code.
func (c *Client) callJSON(ctx context.Context, op, method, path string, in, out any) error {
	var body io.Reader
	var payload []byte
	if in != nil {
		var err error
		payload, err = json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshaling request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	url := c.BaseURL + path
	vlog.V2("chats: %s %s", method, url)
	if payload != nil {
		vlog.V3("chats: -> %s", string(payload))
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	if resp.StatusCode == http.StatusUnauthorized {
		vlog.V1("chats: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return ErrUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(resp.Body)
		logBody, _ := truncateErrorBody(respBody)
		vlog.V1("chats: <- %d (%dms): %s", resp.StatusCode, elapsed.Milliseconds(), logBody)
		return fmt.Errorf("%s failed: HTTP %d: %s", op, resp.StatusCode, logBody)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: reading response: %w", op, err)
	}
	vlog.V1("chats: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
	vlog.V3("chats: <- %s", string(respBody))

	if len(bytes.TrimSpace(respBody)) == 0 {
		if out != nil {
			return fmt.Errorf("%s: empty response", op)
		}
		return nil
	}
	var envelope struct {
		Status *string `json:"status"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("%s: decoding response: %w", op, err)
	}
	if envelope.Status != nil && *envelope.Status != "ok" {
		logBody, _ := truncateErrorBody(respBody)
		return fmt.Errorf("%s failed: HTTP %d, status %q, reason %q: %s", op, resp.StatusCode, *envelope.Status, envelope.Reason, logBody)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("%s: decoding response: %w", op, err)
	}
	return nil
}
