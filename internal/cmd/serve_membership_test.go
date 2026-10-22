package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lavr/express-botx/botx"
	"github.com/lavr/express-botx/internal/config"
)

const (
	testBotHUID  = "2a8e7c41-5b0d-4f3e-9c6a-1d2e3f405162"
	testChatUUID = "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"
)

type fakeChatInfo struct {
	details *botx.ChatDetails
	err     error
}

func (f fakeChatInfo) GetChatInfo(context.Context, string) (*botx.ChatDetails, error) {
	return f.details, f.err
}

func TestMembershipLookup_Outcomes(t *testing.T) {
	withMembers := func(huids ...string) *botx.ChatDetails {
		d := &botx.ChatDetails{GroupChatID: testChatUUID}
		for _, h := range huids {
			d.Members = append(d.Members, botx.ChatMember{UserHUID: h, UserKind: "botx"})
		}
		return d
	}
	apiErr := func(status int, reason string) error {
		return &botx.APIError{Op: "chat info", StatusCode: status, Reason: reason}
	}

	tests := []struct {
		name       string
		info       fakeChatInfo
		wantMember bool
		wantErr    bool
	}{
		{"bot among members", fakeChatInfo{details: withMembers("user-huid", strings.ToUpper(testBotHUID))}, true, false},
		// A removed bot still gets chats/info, only without itself in members.
		{"bot removed from the chat", fakeChatInfo{details: withMembers("user-huid")}, false, false},
		{"chat not found by status", fakeChatInfo{err: apiErr(404, "")}, false, false},
		{"chat not found by reason", fakeChatInfo{err: apiErr(400, "chat_not_found")}, false, false},
		{"permission refused", fakeChatInfo{err: apiErr(403, "no_permission_for_operation")}, false, true},
		{"outage with a not-found reason", fakeChatInfo{err: apiErr(503, "chat_not_found")}, false, true},
		{"rejected token", fakeChatInfo{err: apiErr(401, "")}, false, true},
		{"transport error", fakeChatInfo{err: errors.New("dial tcp: i/o timeout")}, false, true},
		// GetChatInfo fetches a token first; its failure is not an answer
		// about the chat, whatever its status or reason.
		{"token request 404", fakeChatInfo{err: &botx.APIError{Op: "token request", StatusCode: 404}}, false, true},
		{"token request bot_not_found", fakeChatInfo{err: fmt.Errorf("getting chat info: %w", &botx.APIError{Op: "token request", StatusCode: 400, Reason: "bot_not_found"})}, false, true},
		{"wrapped chat not found", fakeChatInfo{err: fmt.Errorf("getting chat info: %w", apiErr(404, "chat_not_found"))}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookup := membershipLookup(map[string]membershipBot{"": {client: tc.info, huid: testBotHUID}})
			member, err := lookup(context.Background(), "", testChatUUID)
			if member != tc.wantMember || (err != nil) != tc.wantErr {
				t.Errorf("member = %v, err = %v; want member %v, error %v", member, err, tc.wantMember, tc.wantErr)
			}
		})
	}
}

func TestMembershipLookup_UsesTheNamedBot(t *testing.T) {
	members := &botx.ChatDetails{Members: []botx.ChatMember{{UserHUID: testBotHUID}}}
	lookup := membershipLookup(map[string]membershipBot{
		"alpha": {client: fakeChatInfo{details: members}, huid: testBotHUID},
		"beta":  {client: fakeChatInfo{details: members}, huid: "7ee8aaa9-c6cb-5ee6-8445-7d654819b285"},
	})
	if member, err := lookup(context.Background(), "alpha", testChatUUID); !member || err != nil {
		t.Errorf("alpha: member %v, err %v", member, err)
	}
	if member, err := lookup(context.Background(), "beta", testChatUUID); member || err != nil {
		t.Errorf("beta: member %v, err %v", member, err)
	}
	if _, err := lookup(context.Background(), "gamma", testChatUUID); err == nil {
		t.Error("unknown bot: want an error")
	}
}

// serve never runs Config.Validate, so the combination is refused where keys
// are resolved too.
func TestResolveAPIKeys_ChatsWhereMember(t *testing.T) {
	chats := map[string]config.ChatConfig{"known": {ID: testChatUUID}}

	if _, err := resolveAPIKeys([]config.APIKeyConfig{{Name: "k", Key: "v", ChatsWhereMember: true, Chats: []string{"known"}}}, chats); err == nil {
		t.Error("chats_where_member with chats accepted")
	}
	keys, err := resolveAPIKeys([]config.APIKeyConfig{{Name: "k", Key: "v", ChatsWhereMember: true}}, chats)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !keys[0].ChatsWhereMember || len(keys[0].Chats) != 0 || keys[0].DefaultChat != "" {
		t.Errorf("key = %+v, want membership scope without chats or own default", keys[0])
	}
	if !hasMembershipKeys(keys) {
		t.Error("hasMembershipKeys = false")
	}
}

func TestMembershipOption(t *testing.T) {
	single := func(id string, mc *config.MembershipCacheConfig) *config.Config {
		return &config.Config{
			Host:       "express.example.com",
			BotID:      id,
			BotToken:   "static-token",
			Server:     config.ServerConfig{MembershipCache: mc},
			BotTimeout: 5,
		}
	}
	if _, err := membershipOption(single(testBotHUID, nil)); err != nil {
		t.Errorf("token-only bot: %v", err)
	}
	if _, err := membershipOption(single("", nil)); err == nil {
		t.Error("bot without an id accepted: it could never be found among chat members")
	}
	if _, err := membershipOption(single(testBotHUID, &config.MembershipCacheConfig{TTL: "10s", NegativeTTL: "1m"})); err == nil {
		t.Error("negative_ttl longer than ttl accepted at startup")
	}
}
