package botx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

// Chat is a chat the bot belongs to, as listed by chats/list.
type Chat struct {
	GroupChatID   string   `json:"group_chat_id"`
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	ChatType      string   `json:"chat_type"`
	Members       []string `json:"members"`
	SharedHistory bool     `json:"shared_history"`
}

// ChatMember is a chat participant as returned by chats/info.
type ChatMember struct {
	UserHUID string `json:"user_huid"`
	// UserKind is "user", "cts_user", "botx" (a bot) and so on.
	UserKind string `json:"user_kind"`
	Admin    bool   `json:"admin"`
	ServerID string `json:"server_id,omitempty"`
}

// ChatDetails is the full chat description returned by chats/info.
type ChatDetails struct {
	GroupChatID string  `json:"group_chat_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	ChatType    string  `json:"chat_type"`
	// Creator is the HUID of the user or bot that created the chat, nil when BotX reports none.
	Creator       *string      `json:"creator"`
	InsertedAt    string       `json:"inserted_at,omitempty"`
	UpdatedAt     string       `json:"updated_at,omitempty"`
	SharedHistory bool         `json:"shared_history"`
	Members       []ChatMember `json:"members"`
}

// Chat types accepted by CreateChat.
const (
	ChatTypeGroup   = "group_chat"
	ChatTypeChannel = "channel"
)

// CreateChatRequest is the body of chats/create.
type CreateChatRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ChatType is ChatTypeGroup or ChatTypeChannel.
	ChatType string `json:"chat_type"`
	// Members are user and bot HUIDs. The creating bot becomes the only admin.
	Members []string `json:"members"`
	// SharedHistory lets members added later read the earlier messages.
	SharedHistory bool `json:"shared_history"`
}

// ListChats returns the chats the bot belongs to. A bot removed from a chat may
// still see it here; ChatDetails.Members tells whether it is a member.
func (c *Client) ListChats(ctx context.Context) ([]Chat, error) {
	vlog.V2("chats: GET %s", c.baseURL+"/api/v3/botx/chats/list")

	start := time.Now()
	resp, err := c.do(ctx, http.MethodGet, "/api/v3/botx/chats/list", "", nil)
	if err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	respBody, readErr := readResponse("list chats", resp)
	if resp.StatusCode == http.StatusUnauthorized {
		vlog.V1("chats: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return nil, unauthorized("list chats", respBody)
	}
	if resp.StatusCode != http.StatusOK {
		vlog.V1("chats: <- %d (%dms)", resp.StatusCode, elapsed.Milliseconds())
		return nil, newAPIError("list chats", resp.StatusCode, respBody, "list chats failed: HTTP %d", resp.StatusCode)
	}
	if readErr != nil {
		return nil, readErr
	}

	var result struct {
		Status *string `json:"status"`
		Result *[]Chat `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, newAPIError("list chats", resp.StatusCode, respBody, "decoding response: %v", err)
	}
	if result.Status != nil && *result.Status != "ok" {
		return nil, newAPIError("list chats", resp.StatusCode, respBody, "list chats failed: status %q", *result.Status)
	}
	if result.Result == nil {
		return nil, newAPIError("list chats", resp.StatusCode, respBody, "list chats failed: response has no result")
	}
	for i, chat := range *result.Result {
		if chat.GroupChatID == "" {
			return nil, newAPIError("list chats", resp.StatusCode, respBody, "list chats failed: chat %d has no group_chat_id", i)
		}
	}

	vlog.V1("chats: <- %d %s (%dms), %d chats", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds(), len(*result.Result))
	return *result.Result, nil
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

// GetChatInfo returns the full description of a chat via chats/info. A result
// for another chat, without creator, or with members lacking user_huid,
// user_kind or admin fails with ErrInvalidResponse.
func (c *Client) GetChatInfo(ctx context.Context, chatID string) (*ChatDetails, error) {
	var resp struct {
		Result *chatDetailsWire `json:"result"`
	}
	path := "/api/v3/botx/chats/info?group_chat_id=" + url.QueryEscape(chatID)
	code, raw, err := c.callJSON(ctx, "chat info", http.MethodGet, path, nil, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Result == nil {
		return nil, newAPIError("chat info", code, raw, "chat info: response has no result")
	}
	if !strings.EqualFold(resp.Result.GroupChatID, chatID) {
		return nil, newAPIError("chat info", code, raw, "chat info: response is for chat %q, requested %q", resp.Result.GroupChatID, chatID)
	}
	details, err := resp.Result.toDetails()
	if err != nil {
		return nil, newAPIError("chat info", code, raw, "chat info: %v", err)
	}
	return details, nil
}

// CreateChat creates a chat and returns its ID. BotX has no call to delete a chat.
func (c *Client) CreateChat(ctx context.Context, req *CreateChatRequest) (string, error) {
	var resp struct {
		Result struct {
			ChatID string `json:"chat_id"`
		} `json:"result"`
	}
	code, raw, err := c.callJSON(ctx, "create chat", http.MethodPost, "/api/v3/botx/chats/create", req, &resp)
	if err != nil {
		return "", err
	}
	if resp.Result.ChatID == "" {
		return "", newAPIError("create chat", code, raw, "create chat: response has no chat_id")
	}
	return resp.Result.ChatID, nil
}

// AddUsers adds users to a chat. Adding a member again succeeds.
func (c *Client) AddUsers(ctx context.Context, chatID string, huids []string) error {
	_, _, err := c.callJSON(ctx, "add user", http.MethodPost, "/api/v3/botx/chats/add_user",
		chatMembersRequest{GroupChatID: chatID, UserHUIDs: huids}, nil)
	return err
}

// RemoveUsers removes users from a chat. A bot that removes itself can no
// longer manage the chat: later calls fail with ErrNoPermission.
func (c *Client) RemoveUsers(ctx context.Context, chatID string, huids []string) error {
	_, _, err := c.callJSON(ctx, "remove user", http.MethodPost, "/api/v3/botx/chats/remove_user",
		chatMembersRequest{GroupChatID: chatID, UserHUIDs: huids}, nil)
	return err
}

// AddAdmins grants chat admin rights to chat members. It fails with
// ErrAdminsNotChanged when all of them already are admins, so compare the
// Admin flags from GetChatInfo first. BotX has no call to revoke admin rights.
func (c *Client) AddAdmins(ctx context.Context, chatID string, huids []string) error {
	_, _, err := c.callJSON(ctx, "add admin", http.MethodPost, "/api/v3/botx/chats/add_admin",
		chatMembersRequest{GroupChatID: chatID, UserHUIDs: huids}, nil)
	return err
}

// callJSON sends an optional JSON body and decodes a 2xx JSON response into out.
// A response whose "status" is present and not "ok" is an error whatever the HTTP code.
// It returns the HTTP status and the raw body for further validation.
func (c *Client) callJSON(ctx context.Context, op, method, path string, in, out any) (int, []byte, error) {
	var payload []byte
	var contentType string
	if in != nil {
		var err error
		payload, err = json.Marshal(in)
		if err != nil {
			return 0, nil, fmt.Errorf("marshaling request: %w", err)
		}
		contentType = "application/json"
	}

	vlog.V2("chats: %s %s", method, c.baseURL+path)
	if payload != nil {
		vlog.V3("chats: -> %s", string(payload))
	}

	start := time.Now()
	resp, err := c.do(ctx, method, path, contentType, payload)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	respBody, readErr := readResponse(op, resp)
	if resp.StatusCode == http.StatusUnauthorized {
		vlog.V1("chats: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return resp.StatusCode, respBody, unauthorized(op, respBody)
	}
	if readErr != nil {
		return resp.StatusCode, respBody, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		logBody, _ := truncateErrorBody(respBody)
		vlog.V1("chats: <- %d (%dms): %s", resp.StatusCode, elapsed.Milliseconds(), logBody)
		return resp.StatusCode, respBody, newAPIError(op, resp.StatusCode, respBody, "%s failed: HTTP %d: %s", op, resp.StatusCode, logBody)
	}

	vlog.V1("chats: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
	vlog.V3("chats: <- %s", string(respBody))

	if len(bytes.TrimSpace(respBody)) == 0 {
		if out != nil {
			return resp.StatusCode, respBody, newAPIError(op, resp.StatusCode, respBody, "%s: empty response", op)
		}
		return resp.StatusCode, respBody, nil
	}
	var envelope struct {
		Status *string `json:"status"`
		Reason string  `json:"reason"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return resp.StatusCode, respBody, newAPIError(op, resp.StatusCode, respBody, "%s: decoding response: %v", op, err)
	}
	if envelope.Status != nil && *envelope.Status != "ok" {
		logBody, _ := truncateErrorBody(respBody)
		return resp.StatusCode, respBody, newAPIError(op, resp.StatusCode, respBody,
			"%s failed: HTTP %d, status %q, reason %q: %s", op, resp.StatusCode, *envelope.Status, envelope.Reason, logBody)
	}
	if out == nil {
		return resp.StatusCode, respBody, nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return resp.StatusCode, respBody, newAPIError(op, resp.StatusCode, respBody, "%s: decoding response: %v", op, err)
	}
	return resp.StatusCode, respBody, nil
}
