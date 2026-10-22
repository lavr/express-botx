package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lavr/express-botx/botx"
	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/secret"
	"github.com/lavr/express-botx/internal/server"
)

// chatInfoGetter is the part of botx.Client a membership lookup needs.
type chatInfoGetter interface {
	GetChatInfo(ctx context.Context, chatID string) (*botx.ChatDetails, error)
}

// membershipBot is a bot whose membership is checked: a client acting as that
// bot and the HUID it appears under in a chat's member list (its bot ID).
type membershipBot struct {
	client chatInfoGetter
	huid   string
}

func hasMembershipKeys(keys []server.ResolvedKey) bool {
	for _, k := range keys {
		if k.ChatsWhereMember {
			return true
		}
	}
	return false
}

// membershipOption builds the server option that checks chat membership for
// keys with chats_where_member, one BotX client per configured bot.
func membershipOption(cfg *config.Config) (server.Option, error) {
	ttl, negativeTTL, err := cfg.Server.MembershipCache.Durations()
	if err != nil {
		return nil, err
	}
	bots := make(map[string]membershipBot)
	if cfg.IsMultiBot() {
		for _, name := range cfg.BotNames() {
			b := cfg.Bots[name]
			mb, err := newMembershipBot(b.Host, b.ID, b.Secret, b.Token, cfg)
			if err != nil {
				return nil, fmt.Errorf("bot %q: %w", name, err)
			}
			bots[name] = mb
		}
	} else {
		mb, err := newMembershipBot(cfg.Host, cfg.BotID, cfg.BotSecret, cfg.BotToken, cfg)
		if err != nil {
			return nil, err
		}
		// Single-bot mode resolves the sending bot to "".
		bots[""] = mb
	}
	return server.WithChatMembership(membershipLookup(bots), server.MembershipOptions{
		TTL:         ttl,
		NegativeTTL: negativeTTL,
	}), nil
}

func newMembershipBot(host, botID, botSecret, botToken string, cfg *config.Config) (membershipBot, error) {
	// The bot ID is what chats/info lists the bot under, so it is needed even
	// when a token alone would do for sending.
	if botID == "" {
		return membershipBot{}, errors.New("chats_where_member needs the bot id to find the bot among chat members")
	}
	timeout := botx.WithTimeout(cfg.HTTPTimeout())
	if botSecret == "" && botToken != "" {
		tok, err := secret.Resolve(botToken)
		if err != nil {
			return membershipBot{}, fmt.Errorf("resolving token: %w", err)
		}
		return membershipBot{client: botx.NewWithToken(host, tok, timeout), huid: botID}, nil
	}
	sec, err := secret.Resolve(botSecret)
	if err != nil {
		return membershipBot{}, fmt.Errorf("resolving secret: %w", err)
	}
	c, err := botx.New(host, botID, sec, timeout)
	if err != nil {
		return membershipBot{}, err
	}
	return membershipBot{client: c, huid: botID}, nil
}

// membershipLookup reports whether a bot is in a chat's member list. chats/list
// cannot answer this: a bot removed from a chat still sees it there, and also in
// chats/info, only without itself among the members. A chat BotX reports as not
// found counts as "not a member"; any other failure, a refused permission
// included, is an error, which the server answers with 503 and does not cache.
func membershipLookup(bots map[string]membershipBot) server.MembershipLookup {
	return func(ctx context.Context, bot, chatID string) (bool, error) {
		b, ok := bots[bot]
		if !ok {
			return false, fmt.Errorf("bot %q is not configured", bot)
		}
		info, err := b.client.GetChatInfo(ctx, chatID)
		if err != nil {
			if chatNotFound(err) {
				return false, nil
			}
			return false, err
		}
		for _, m := range info.Members {
			if strings.EqualFold(m.UserHUID, b.huid) {
				return true, nil
			}
		}
		return false, nil
	}
}

// chatNotFound reports a definite "no such chat" answer from chats/info.
// ErrNotFound also matches a *_not_found reason on any status and any call, so
// the answer must come from chats/info itself (GetChatInfo may fail earlier, on
// the token request, with bot_not_found) and must not be an outage or an auth
// failure: none of those may become a cached denial.
func chatNotFound(err error) bool {
	var apiErr *botx.APIError
	if !errors.As(err, &apiErr) || apiErr.Op != "chat info" {
		return false
	}
	if apiErr.StatusCode >= 500 || apiErr.StatusCode == 401 {
		return false
	}
	return errors.Is(err, botx.ErrNotFound)
}
