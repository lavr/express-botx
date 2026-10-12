package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
)

type resolvedMember struct {
	Ref      string `json:"ref"`
	UserHUID string `json:"user_huid"`
}

type chatCreateResult struct {
	ChatID  string           `json:"chat_id"`
	Created bool             `json:"created"`
	Members []resolvedMember `json:"members"`
}

type chatMembersResult struct {
	ChatID  string           `json:"chat_id"`
	Members []resolvedMember `json:"members"`
}

// withTokenRefresh runs op with an authenticated client. If op fails with HTTP 401
// and the bot authenticates by secret, the token is refreshed once and op rerun:
// BotX rejected the request, so repeating it cannot double a mutation.
func withTokenRefresh(cfg *config.Config, op func(*botapi.Client) error) error {
	tok, cache, err := authenticate(cfg)
	if err != nil {
		return err
	}
	client := botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout())
	err = op(client)
	if !errors.Is(err, botapi.ErrUnauthorized) || cfg.BotSecret == "" {
		return err
	}
	vlog.V1("auth: 401, refreshing token")
	if client.Token, err = refreshToken(cfg, cache); err != nil {
		return fmt.Errorf("refreshing token: %w", err)
	}
	return op(client)
}

// splitMemberRefs flattens comma-separated member lists into trimmed, non-empty refs.
func splitMemberRefs(values []string) []string {
	var refs []string
	for _, v := range values {
		for ref := range strings.SplitSeq(v, ",") {
			if ref = strings.TrimSpace(ref); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// resolveMembers maps every ref to a HUID: UUID as is, "@" via by_email,
// "DOMAIN\login" or "login" (with adDomain) via by_login. All refs are tried,
// and any failure fails the whole call so no partial action follows.
func resolveMembers(ctx context.Context, client *botapi.Client, refs []string, adDomain string) ([]resolvedMember, error) {
	var members []resolvedMember
	var failed []string
	seen := make(map[string]bool)
	for _, ref := range refs {
		huid, err := resolveMember(ctx, client, ref, adDomain)
		if errors.Is(err, botapi.ErrUnauthorized) {
			return nil, err
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", ref, err))
			continue
		}
		huid = strings.ToLower(huid)
		if seen[huid] {
			continue
		}
		seen[huid] = true
		members = append(members, resolvedMember{Ref: ref, UserHUID: huid})
	}
	if len(failed) > 0 {
		return nil, fmt.Errorf("cannot resolve members:\n  %s", strings.Join(failed, "\n  "))
	}
	return members, nil
}

func resolveMember(ctx context.Context, client *botapi.Client, ref, adDomain string) (string, error) {
	if config.IsUUID(ref) {
		return ref, nil
	}
	var user *botapi.UserInfo
	var err error
	if strings.Contains(ref, "@") {
		user, err = client.GetUserByEmail(ctx, ref)
	} else {
		login, domain := ref, adDomain
		if d, l, ok := strings.Cut(ref, `\`); ok {
			domain, login = d, l
		}
		if login == "" {
			return "", fmt.Errorf("empty AD login")
		}
		if domain == "" {
			return "", fmt.Errorf("AD domain unknown: use --ad-domain or DOMAIN\\login")
		}
		user, err = client.GetUserByADLogin(ctx, login, domain)
	}
	if err != nil {
		return "", err
	}
	if user.HUID == "" {
		return "", fmt.Errorf("user not found")
	}
	return user.HUID, nil
}

// botHUID returns the HUID of a configured bot, which is its id.
func botHUID(cfg *config.Config, name string) (string, error) {
	bot, ok := cfg.Bots[name]
	if !ok {
		return "", fmt.Errorf("unknown bot %q, available: %s", name, strings.Join(cfg.BotNames(), ", "))
	}
	if !config.IsUUID(bot.ID) {
		return "", fmt.Errorf("bot %q: id %q is not a UUID", name, bot.ID)
	}
	return strings.ToLower(bot.ID), nil
}

func runChatsCreate(args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats create", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var name, description, adDomain string
	var members, addBots stringSlice
	var sharedHistory, allowDuplicate bool

	globalFlags(fs, &flags)
	fs.StringVar(&name, "name", "", "chat name (required)")
	fs.StringVar(&description, "description", "", "chat description")
	fs.Var(&members, "members", "members: email, HUID or AD login, comma-separated (repeatable)")
	fs.Var(&addBots, "add-bot", "add a bot from config as a member by its name (repeatable)")
	fs.BoolVar(&sharedHistory, "shared-history", false, "new members see the chat history")
	fs.StringVar(&adDomain, "ad-domain", "", "AD domain for members given as plain logins")
	fs.BoolVar(&allowDuplicate, "allow-duplicate", false, "create a new chat even if this bot already created one with the same name")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats create --name <name> [options]\n\nCreate a group chat. The bot becomes its creator and only admin.\nIf this bot already created a group chat with the same name, its ID is\nprinted and nothing is changed, unless --allow-duplicate is given.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	applyVerboseFlag(flags)
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("--name is required")
	}

	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}
	if err := cfg.ValidateFormat(); err != nil {
		return err
	}

	var bots []resolvedMember
	for _, b := range addBots {
		huid, err := botHUID(cfg, b)
		if err != nil {
			return err
		}
		bots = append(bots, resolvedMember{Ref: "bot:" + b, UserHUID: huid})
	}

	ctx := context.Background()
	var result chatCreateResult
	err = withTokenRefresh(cfg, func(client *botapi.Client) error {
		resolved, err := resolveMembers(ctx, client, splitMemberRefs(members), adDomain)
		if err != nil {
			return err
		}
		for _, b := range bots {
			if !containsHUID(resolved, b.UserHUID) {
				resolved = append(resolved, b)
			}
		}

		if !allowDuplicate {
			existing, err := findOwnChatByName(ctx, client, cfg.BotID, name)
			if err != nil {
				return err
			}
			if existing != "" {
				result = chatCreateResult{ChatID: existing, Members: []resolvedMember{}}
				return nil
			}
		}

		huids := make([]string, len(resolved))
		for i, m := range resolved {
			huids[i] = m.UserHUID
		}
		chatID, err := client.CreateChat(ctx, &botapi.CreateChatRequest{
			Name:          name,
			Description:   description,
			ChatType:      chatTypeGroup,
			Members:       huids,
			SharedHistory: sharedHistory,
		})
		if err != nil {
			return fmt.Errorf("creating chat: %w", err)
		}
		result = chatCreateResult{ChatID: chatID, Created: true, Members: resolved}
		if result.Members == nil {
			result.Members = []resolvedMember{}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if !result.Created {
		fmt.Fprintf(deps.Stderr, "Chat %q already created by this bot, nothing changed (use --allow-duplicate to create another)\n", name)
	}
	return printOutput(deps.Stdout, cfg.Format, func() {
		fmt.Fprintln(deps.Stdout, result.ChatID)
	}, result)
}

// findOwnChatByName returns the ID of the group chat with exactly this name
// whose creator is botID, "" if there is none, and an error if there are several
// or if the bot is no longer an admin there and so could not manage it.
func findOwnChatByName(ctx context.Context, client *botapi.Client, botID, name string) (string, error) {
	chats, err := client.ListChats(ctx)
	if err != nil {
		return "", fmt.Errorf("looking for existing chat: %w", err)
	}
	var found []string
	for i, chat := range chats {
		if chat.GroupChatID == "" || chat.ChatType == "" || chat.ChatType == chatTypeGroup && chat.Name == "" {
			return "", fmt.Errorf("looking for existing chat: chats list entry %d is incomplete", i)
		}
		if chat.ChatType != chatTypeGroup || chat.Name != name {
			continue
		}
		details, err := client.GetChatInfo(ctx, chat.GroupChatID)
		if err != nil {
			return "", fmt.Errorf("looking for existing chat: %w", err)
		}
		if details.Creator == nil || !strings.EqualFold(*details.Creator, botID) {
			continue
		}
		if !isChatAdmin(details, botID) {
			return "", fmt.Errorf("chat %s named %q was created by this bot, but the bot is no longer its admin: a chat admin has to add it back and make it admin, or use --allow-duplicate", chat.GroupChatID, name)
		}
		found = append(found, chat.GroupChatID)
	}
	if len(found) > 1 {
		return "", fmt.Errorf("this bot created %d chats named %q: %s; pass the one you need as --chat-id to other commands, or use --allow-duplicate", len(found), name, strings.Join(found, ", "))
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", nil
}

func isChatAdmin(chat *botapi.ChatDetails, huid string) bool {
	for _, m := range chat.Members {
		if strings.EqualFold(m.UserHUID, huid) {
			return m.Admin
		}
	}
	return false
}

func containsHUID(members []resolvedMember, huid string) bool {
	for _, m := range members {
		if m.UserHUID == huid {
			return true
		}
	}
	return false
}

type chatMembersAction struct {
	name  string
	usage string
	done  string
	call  func(c *botapi.Client, ctx context.Context, chatID string, huids []string) error
}

var (
	chatsAddUserAction = chatMembersAction{
		name:  "add-user",
		usage: "Add users to a chat.",
		done:  "Added %d member(s) to chat %s:",
		call:  (*botapi.Client).AddUsers,
	}
	chatsRemoveUserAction = chatMembersAction{
		name:  "remove-user",
		usage: "Remove users from a chat.",
		done:  "Removed %d member(s) from chat %s:",
		call:  (*botapi.Client).RemoveUsers,
	}
	chatsAddAdminAction = chatMembersAction{
		name:  "add-admin",
		usage: "Make chat members admins.",
		done:  "Made %d member(s) admins in chat %s:",
		call:  (*botapi.Client).AddAdmins,
	}
)

func runChatsMembersAction(action chatMembersAction, args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats "+action.name, flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var adDomain string

	globalFlags(fs, &flags)
	fs.StringVar(&flags.ChatID, "chat-id", "", "chat UUID or alias name")
	fs.StringVar(&adDomain, "ad-domain", "", "AD domain for members given as plain logins")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats %s --chat-id <chat> <email|huid|login>...\n\n%s\nMembers: UUID is taken as HUID, a value with @ is looked up by email,\nanything else by AD login (DOMAIN\\login or --ad-domain).\n\nOptions:\n", action.name, action.usage)
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	applyVerboseFlag(flags)
	refs := splitMemberRefs(fs.Args())
	if len(refs) == 0 {
		return fmt.Errorf("at least one member is required")
	}

	if flags.ChatID == "" {
		return fmt.Errorf("--chat-id is required")
	}

	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}
	if err := cfg.RequireChatID(); err != nil {
		return err
	}
	if err := cfg.ValidateFormat(); err != nil {
		return err
	}

	ctx := context.Background()
	var resolved []resolvedMember
	err = withTokenRefresh(cfg, func(client *botapi.Client) error {
		var err error
		resolved, err = resolveMembers(ctx, client, refs, adDomain)
		if err != nil {
			return err
		}
		huids := make([]string, len(resolved))
		for i, m := range resolved {
			huids[i] = m.UserHUID
		}
		if err := action.call(client, ctx, cfg.ChatID, huids); err != nil {
			return fmt.Errorf("chats %s: %w", action.name, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		fmt.Fprintf(deps.Stdout, action.done+"\n", len(resolved), cfg.ChatID)
		for _, m := range resolved {
			if m.Ref == m.UserHUID {
				fmt.Fprintf(deps.Stdout, "  %s\n", m.UserHUID)
			} else {
				fmt.Fprintf(deps.Stdout, "  %s  (%s)\n", m.UserHUID, m.Ref)
			}
		}
	}, chatMembersResult{ChatID: cfg.ChatID, Members: resolved})
}
