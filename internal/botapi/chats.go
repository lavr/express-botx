package botapi

import (
	"context"

	"github.com/lavr/express-botx/botx"
)

type (
	ChatMember        = botx.ChatMember
	ChatDetails       = botx.ChatDetails
	CreateChatRequest = botx.CreateChatRequest
)

// GetChatInfo returns the full description of a chat via chats/info.
func (c *Client) GetChatInfo(ctx context.Context, chatID string) (*ChatDetails, error) {
	return c.api().GetChatInfo(ctx, chatID)
}

// CreateChat creates a chat and returns its ID.
func (c *Client) CreateChat(ctx context.Context, req *CreateChatRequest) (string, error) {
	return c.api().CreateChat(ctx, req)
}

// AddUsers adds users to a chat.
func (c *Client) AddUsers(ctx context.Context, chatID string, huids []string) error {
	return c.api().AddUsers(ctx, chatID, huids)
}

// RemoveUsers removes users from a chat.
func (c *Client) RemoveUsers(ctx context.Context, chatID string, huids []string) error {
	return c.api().RemoveUsers(ctx, chatID, huids)
}

// AddAdmins grants chat admin rights to chat members.
func (c *Client) AddAdmins(ctx context.Context, chatID string, huids []string) error {
	return c.api().AddAdmins(ctx, chatID, huids)
}
