package botapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"time"

	vlog "github.com/lavr/express-bot/internal/log"
)

// Client is a BotX API client.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a Client for the given host and token.
func NewClient(host, token string) *Client {
	return &Client{
		BaseURL:    fmt.Sprintf("https://%s", host),
		Token:      token,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ChatInfo holds information about a single chat.
type ChatInfo struct {
	GroupChatID   string   `json:"group_chat_id"`
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	ChatType      string   `json:"chat_type"`
	Members       []string `json:"members"`
	SharedHistory bool     `json:"shared_history"`
}

type listChatsResponse struct {
	Result []ChatInfo `json:"result"`
}

// ListChats returns all chats the bot is a member of.
func (c *Client) ListChats(ctx context.Context) ([]ChatInfo, error) {
	url := c.BaseURL + "/api/v3/botx/chats/list"
	vlog.V2("chats: GET %s", url)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing chats: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		vlog.V1("chats: <- %d (%dms)", resp.StatusCode, elapsed.Milliseconds())
		return nil, fmt.Errorf("list chats failed: HTTP %d", resp.StatusCode)
	}

	var result listChatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	vlog.V1("chats: <- %d %s (%dms), %d chats", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds(), len(result.Result))
	return result.Result, nil
}

// GetChatInfo returns info for a specific chat by UUID.
func (c *Client) GetChatInfo(ctx context.Context, chatID string) (*ChatInfo, error) {
	chats, err := c.ListChats(ctx)
	if err != nil {
		return nil, err
	}
	for i := range chats {
		if chats[i].GroupChatID == chatID {
			return &chats[i], nil
		}
	}
	return nil, fmt.Errorf("chat not found: %s", chatID)
}

// UserInfo holds information about a user.
type UserInfo struct {
	HUID      string   `json:"user_huid"`
	Name      string   `json:"name"`
	Emails    []string `json:"emails"`
	ADLogin   string   `json:"ad_login,omitempty"`
	ADDomain  string   `json:"ad_domain,omitempty"`
	Company   string   `json:"company,omitempty"`
	Title     string   `json:"company_position,omitempty"`
	Department string  `json:"department,omitempty"`
	Active    bool     `json:"active"`
	UserKind  string   `json:"user_kind,omitempty"`
}

type userByHUIDResponse struct {
	Status string   `json:"status"`
	Result UserInfo `json:"result"`
}

// GetUserByHUID fetches user info by HUID.
func (c *Client) GetUserByHUID(ctx context.Context, huid string) (*UserInfo, error) {
	return c.getUser(ctx, "/api/v3/botx/users/by_huid?user_huid="+huid)
}

// GetUserByEmail fetches user info by email.
func (c *Client) GetUserByEmail(ctx context.Context, email string) (*UserInfo, error) {
	return c.getUser(ctx, "/api/v3/botx/users/by_email?email="+email)
}

// GetUserByADLogin fetches user info by AD login and domain.
func (c *Client) GetUserByADLogin(ctx context.Context, login, domain string) (*UserInfo, error) {
	return c.getUser(ctx, "/api/v3/botx/users/by_login?ad_login="+login+"&ad_domain="+domain)
}

func (c *Client) getUser(ctx context.Context, path string) (*UserInfo, error) {
	url := c.BaseURL + path
	vlog.V2("user: GET %s", url)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		vlog.V1("user: <- %d (%dms)", resp.StatusCode, elapsed.Milliseconds())
		return nil, fmt.Errorf("get user failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result userByHUIDResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	vlog.V1("user: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
	return &result.Result, nil
}

// SendRequest is the unified request for POST /api/v4/botx/notifications/direct.
type SendRequest struct {
	GroupChatID   string            `json:"group_chat_id"`
	Notification  *SendNotification `json:"notification,omitempty"`
	File          *SendFile         `json:"file,omitempty"`
	Opts          *SendOpts         `json:"opts,omitempty"`
}

// SendNotification is the notification part of a send request.
type SendNotification struct {
	Status   string              `json:"status"`
	Body     string              `json:"body"`
	Metadata json.RawMessage     `json:"metadata,omitempty"`
	Opts     *NotificationMsgOpts `json:"opts,omitempty"`
}

// NotificationMsgOpts controls per-message notification behavior.
type NotificationMsgOpts struct {
	SilentResponse bool `json:"silent_response,omitempty"`
}

// SendFile is a file attachment sent inline as a base64 data URI.
type SendFile struct {
	FileName string `json:"file_name"`
	Data     string `json:"data"` // data:mime;base64,...
}

// SendOpts controls delivery-level options.
type SendOpts struct {
	StealthMode      bool          `json:"stealth_mode,omitempty"`
	NotificationOpts *DeliveryOpts `json:"notification_opts,omitempty"`
}

// DeliveryOpts controls push notification delivery.
type DeliveryOpts struct {
	Send     *bool `json:"send,omitempty"`
	ForceDND bool  `json:"force_dnd,omitempty"`
}

// ErrUnauthorized indicates the token is invalid or expired.
var ErrUnauthorized = fmt.Errorf("unauthorized (HTTP 401)")

// Send posts a notification (text and/or file) to a chat via BotX API.
func (c *Client) Send(ctx context.Context, sr *SendRequest) error {
	_, err := c.SendWithSyncID(ctx, sr)
	return err
}

type sendAPIResponse struct {
	Status string `json:"status"`
	Result struct {
		SyncID string `json:"sync_id"`
	} `json:"result"`
}

// SendWithSyncID posts a notification and returns the sync_id from the response.
func (c *Client) SendWithSyncID(ctx context.Context, sr *SendRequest) (string, error) {
	body, err := json.Marshal(sr)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := c.BaseURL + "/api/v4/botx/notifications/direct"
	vlog.V2("send: POST %s", url)
	vlog.V2("send: -> Authorization: Bearer %s", vlog.MaskBearer(c.Token))
	vlog.V3("send: -> %s", string(body))

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sending: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode == http.StatusUnauthorized {
		vlog.V1("send: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return "", ErrUnauthorized
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		vlog.V1("send: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
		var apiResp sendAPIResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			return "", nil // sent ok, but couldn't parse sync_id
		}
		vlog.V3("send: <- sync_id=%s", apiResp.Result.SyncID)
		return apiResp.Result.SyncID, nil
	default:
		respBody, _ := io.ReadAll(resp.Body)
		vlog.V1("send: <- %d (%dms)", resp.StatusCode, elapsed.Milliseconds())
		vlog.V3("send: <- %s", string(respBody))
		return "", fmt.Errorf("send failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
}

// BuildFileAttachment reads file data and returns a SendFile with base64 data URI.
func BuildFileAttachment(filename string, data []byte) *SendFile {
	mimeType, _, _ := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(filename)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))
	return &SendFile{
		FileName: filename,
		Data:     dataURI,
	}
}

// BuildFileAttachmentFromBase64 creates a SendFile from a filename and already-base64-encoded data.
func BuildFileAttachmentFromBase64(filename, base64Data string) *SendFile {
	mimeType, _, _ := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(filename)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data)
	return &SendFile{
		FileName: filename,
		Data:     dataURI,
	}
}
