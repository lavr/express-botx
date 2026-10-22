package botapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lavr/express-botx/botx"
	"github.com/lavr/express-botx/internal/httputil"
	vlog "github.com/lavr/express-botx/internal/log"
)

// Client is a BotX API client with a token the caller manages.
// Requests go through botx; Token can be replaced between calls.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient creates a Client for the given host and token.
// Host can be a hostname ("express.company.ru") or a full URL ("http://localhost:8080").
func NewClient(host, token string, timeout time.Duration) *Client {
	return &Client{
		BaseURL:    ResolveBaseURL(host),
		Token:      token,
		HTTPClient: httputil.NewClient(timeout),
	}
}

// ResolveBaseURL normalizes a host into a full base URL.
// If host starts with http:// or https://, it is used as-is (with trailing slash removed).
// Otherwise, https:// is prepended.
func ResolveBaseURL(host string) string {
	return httputil.ResolveBaseURL(host)
}

func (c *Client) api() *botx.Client {
	return botx.NewWithToken(c.BaseURL, c.Token, botx.WithHTTPClient(c.HTTPClient))
}

type (
	ChatInfo            = botx.Chat
	UserInfo            = botx.User
	SendRequest         = botx.SendRequest
	SendNotification    = botx.SendNotification
	NotificationMsgOpts = botx.NotificationMsgOpts
	SendFile            = botx.SendFile
	SendOpts            = botx.SendOpts
	DeliveryOpts        = botx.DeliveryOpts
)

// ListChats returns all chats the bot is a member of.
func (c *Client) ListChats(ctx context.Context) ([]ChatInfo, error) {
	return c.api().ListChats(ctx)
}

// GetUserByHUID fetches user info by HUID.
func (c *Client) GetUserByHUID(ctx context.Context, huid string) (*UserInfo, error) {
	return c.api().GetUserByHUID(ctx, huid)
}

// GetUserByEmail fetches user info by email.
func (c *Client) GetUserByEmail(ctx context.Context, email string) (*UserInfo, error) {
	return c.api().GetUserByEmail(ctx, email)
}

// GetUserByADLogin fetches user info by AD login and domain.
func (c *Client) GetUserByADLogin(ctx context.Context, login, domain string) (*UserInfo, error) {
	return c.api().GetUserByADLogin(ctx, login, domain)
}

// SendParams is a high-level description of a message to send.
// Both the CLI send command and the HTTP /send endpoint use this
// to build a SendRequest, avoiding duplicated construction logic.
type SendParams struct {
	ChatID   string
	Message  string
	Status   string // "ok" or "error"
	File     *SendFile
	Metadata json.RawMessage
	Mentions json.RawMessage
	Silent   bool
	Stealth  bool
	ForceDND bool
	NoNotify bool
	// MaxMessageLength caps the notification body in runes; 0 disables capping.
	// TruncateSuffix is appended to a cut body and counts inside the cap.
	MaxMessageLength int
	TruncateSuffix   string
}

// capBody applies the configured length cap to the notification body and keeps
// the mentions array consistent with it. A body that fits, or a cap of zero,
// passes through untouched along with the original mentions.
func capBody(p *SendParams) (string, json.RawMessage) {
	body, truncated := TruncateMessage(p.Message, p.TruncateSuffix, p.MaxMessageLength)
	if !truncated {
		return p.Message, p.Mentions
	}

	vlog.V1("send: message truncated from %d runes (%d bytes) to %d runes (%d bytes), %d lines dropped",
		utf8.RuneCountInString(p.Message), len(p.Message),
		utf8.RuneCountInString(body), len(body),
		strings.Count(p.Message, "\n")-strings.Count(body, "\n"))

	mentions := p.Mentions
	if len(mentions) > 0 {
		filtered, err := filterOrphanMentions(mentions, p.Message, body)
		if err != nil {
			vlog.V1("send: mentions left untouched, cannot filter after truncation: %v", err)
		} else {
			mentions = filtered
		}
	}
	return body, mentions
}

// BuildSendRequest converts high-level SendParams into a BotX API SendRequest.
func BuildSendRequest(p *SendParams) *SendRequest {
	sr := &SendRequest{
		GroupChatID: p.ChatID,
	}

	if p.Message != "" {
		body, mentions := capBody(p)
		sr.Notification = &SendNotification{
			Status:   p.Status,
			Body:     body,
			Metadata: p.Metadata,
			Mentions: mentions,
		}
		if p.Silent {
			sr.Notification.Opts = &NotificationMsgOpts{
				SilentResponse: true,
			}
		}
	}

	sr.File = p.File

	if p.Stealth || p.ForceDND || p.NoNotify {
		sr.Opts = &SendOpts{
			StealthMode: p.Stealth,
		}
		if p.ForceDND || p.NoNotify {
			sr.Opts.NotificationOpts = &DeliveryOpts{
				ForceDND: p.ForceDND,
			}
			if p.NoNotify {
				f := false
				sr.Opts.NotificationOpts.Send = &f
			}
		}
	}

	// File-only with metadata/mentions: still need a notification to carry them
	if sr.Notification == nil && (len(p.Metadata) > 0 || len(p.Mentions) > 0) {
		sr.Notification = &SendNotification{
			Status:   p.Status,
			Metadata: p.Metadata,
			Mentions: p.Mentions,
		}
		if p.Silent {
			sr.Notification.Opts = &NotificationMsgOpts{
				SilentResponse: true,
			}
		}
	}

	return sr
}

// ErrUnauthorized indicates the token is invalid or expired.
var ErrUnauthorized = botx.ErrUnauthorized

// SendWithSyncID posts a notification and returns the sync_id from the response.
// Unlike botx.Send it treats HTTP 200, 201 and 202 as sent whatever the body
// says, as the server and worker always have.
func (c *Client) SendWithSyncID(ctx context.Context, sr *SendRequest) (string, error) {
	syncID, err := c.api().Send(ctx, sr)
	var apiErr *botx.APIError
	if errors.As(err, &apiErr) && apiErr.Op == "send" {
		switch apiErr.StatusCode {
		case http.StatusOK, http.StatusCreated, http.StatusAccepted:
			return syncID, nil
		}
	}
	return syncID, err
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
