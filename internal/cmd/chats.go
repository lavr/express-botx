package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
)

// checkDefaultConflict returns an error if another chat (other than exclude) is already the default.
func checkDefaultConflict(chats map[string]config.ChatConfig, exclude string) error {
	for name, ch := range chats {
		if ch.Default && name != exclude {
			return fmt.Errorf("chat %q is already marked as default; to change default, first run: config chat set %s <uuid> --no-default", name, name)
		}
	}
	return nil
}

func runChats(args []string, deps Deps) error {
	if len(args) == 0 {
		printChatsUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: list, info")
	}

	switch args[0] {
	case "list":
		return runChatsList(args[1:], deps)
	case "info":
		return runChatsInfo(args[1:], deps)
	case "--help", "-h":
		printChatsUsage(deps.Stderr)
		return nil
	default:
		printChatsUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: chats %s", args[0])
	}
}

func runChatsList(args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats list", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	globalFlags(fs, &flags)
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats list [options]\n\nList chats the bot is a member of.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}
	if err := cfg.ValidateFormat(); err != nil {
		return err
	}

	tok, _, err := authenticate(cfg)
	if err != nil {
		return err
	}

	client := botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout())
	chats, err := client.ListChats(context.Background())
	if err != nil {
		return fmt.Errorf("listing chats: %w", err)
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		if len(chats) == 0 {
			fmt.Fprintln(deps.Stdout, "No chats found. Add the bot to a chat first.")
			return
		}

		fmt.Fprintf(deps.Stdout, "Chats (%d):\n", len(chats))
		fmt.Fprintln(deps.Stdout, "------------------------------------------------------------------------")

		for _, chat := range chats {
			fmt.Fprintf(deps.Stdout, "  %s\n", chat.GroupChatID)
			fmt.Fprintf(deps.Stdout, "    name:    %s\n", chat.Name)
			fmt.Fprintf(deps.Stdout, "    type:    %s\n", chat.ChatType)
			fmt.Fprintf(deps.Stdout, "    members: %d\n", len(chat.Members))
			fmt.Fprintln(deps.Stdout)
		}
	}, chats)
}

func runChatsInfo(args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats info", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	globalFlags(fs, &flags)
	fs.StringVar(&flags.ChatID, "chat-id", "", "chat UUID or alias name")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats info [options]\n\nShow detailed information about a chat.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
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

	tok, _, err := authenticate(cfg)
	if err != nil {
		return err
	}

	client := botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout())
	chat, err := client.GetChatInfo(context.Background(), cfg.ChatID)
	if err != nil {
		return fmt.Errorf("getting chat info: %w", err)
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		desc := "-"
		if chat.Description != nil && *chat.Description != "" {
			desc = *chat.Description
		}
		fmt.Fprintf(deps.Stdout, "Chat: %s\n", chat.GroupChatID)
		fmt.Fprintf(deps.Stdout, "  name:           %s\n", chat.Name)
		fmt.Fprintf(deps.Stdout, "  type:           %s\n", chat.ChatType)
		fmt.Fprintf(deps.Stdout, "  description:    %s\n", desc)
		fmt.Fprintf(deps.Stdout, "  shared_history: %v\n", chat.SharedHistory)
		fmt.Fprintf(deps.Stdout, "  members (%d):\n", len(chat.Members))
		for _, m := range chat.Members {
			fmt.Fprintf(deps.Stdout, "    %s\n", m)
		}
	}, chat)
}

func runChatsAliasList(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config chat list", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config chat list [options]\n\nList configured chat aliases.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}
	if err := cfg.ValidateFormat(); err != nil {
		return err
	}

	entries := cfg.ChatEntries()

	return printOutput(deps.Stdout, cfg.Format, func() {
		if len(entries) == 0 {
			fmt.Fprintln(deps.Stdout, "No chat aliases configured.")
			fmt.Fprintf(deps.Stdout, "Add one with: express-botx config chat set <name> <uuid>\n")
			return
		}
		fmt.Fprintf(deps.Stdout, "Chat aliases (%d):\n", len(entries))
		for _, e := range entries {
			var tags []string
			if e.Bot != "" {
				tags = append(tags, "bot: "+e.Bot)
			}
			if e.Default {
				tags = append(tags, "default")
			}
			if len(tags) > 0 {
				fmt.Fprintf(deps.Stdout, "  %-20s %s  (%s)\n", e.Name, e.ID, strings.Join(tags, ", "))
			} else {
				fmt.Fprintf(deps.Stdout, "  %-20s %s\n", e.Name, e.ID)
			}
		}
	}, entries)
}

func runChatsAliasSet(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config chat set", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	var botFlag string
	var setDefault, unsetDefault bool
	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&botFlag, "bot", "", "default bot for this chat")
	fs.BoolVar(&setDefault, "default", false, "mark this chat as the default")
	fs.BoolVar(&unsetDefault, "no-default", false, "remove the default flag from this chat")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config chat set <name> <uuid> [options]\n\nAdd or update a chat alias in the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if setDefault && unsetDefault {
		return fmt.Errorf("--default and --no-default are mutually exclusive")
	}

	if fs.NArg() != 2 {
		return fmt.Errorf("usage: config chat set <name> <uuid> [--bot <bot>]")
	}
	name := fs.Arg(0)
	uuid := fs.Arg(1)

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	if cfg.Chats == nil {
		cfg.Chats = make(map[string]config.ChatConfig)
	}

	action := "added"
	existing, exists := cfg.Chats[name]
	if exists {
		action = "updated"
	}

	if setDefault {
		if err := checkDefaultConflict(cfg.Chats, name); err != nil {
			return err
		}
	}

	// Preserve existing bot binding if --bot not explicitly provided
	bot := botFlag
	if bot == "" && exists {
		bot = existing.Bot
	}
	// Resolve default: --default sets, --no-default clears, otherwise preserve existing
	isDefault := setDefault
	if !setDefault && !unsetDefault && exists {
		isDefault = existing.Default
	}
	cfg.Chats[name] = config.ChatConfig{ID: uuid, Bot: bot, Default: isDefault}

	if err := cfg.SaveConfig(); err != nil {
		return err
	}

	out := fmt.Sprintf("Alias %s: %s -> %s", action, name, uuid)
	if botFlag != "" {
		out += fmt.Sprintf(" (bot: %s)", botFlag)
	}
	fmt.Fprintln(deps.Stdout, out)
	return nil
}

func runChatsAliasRm(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config chat rm", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config chat rm <name> [options]\n\nRemove a chat alias from the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: config chat rm <name>")
	}
	name := fs.Arg(0)

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	if _, exists := cfg.Chats[name]; !exists {
		return fmt.Errorf("alias %q not found", name)
	}

	delete(cfg.Chats, name)
	if len(cfg.Chats) == 0 {
		cfg.Chats = nil
	}

	if err := cfg.SaveConfig(); err != nil {
		return err
	}

	fmt.Fprintf(deps.Stdout, "Alias removed: %s\n", name)
	return nil
}

func runChatsAdd(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config chat add", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var nameFilter, alias string
	var setDefault, unsetDefault bool

	globalFlags(fs, &flags)
	fs.StringVar(&flags.ChatID, "chat-id", "", "chat UUID (skip API lookup)")
	fs.StringVar(&nameFilter, "name", "", "chat name to search for (substring match)")
	fs.StringVar(&alias, "alias", "", "alias name (auto-generated from chat name if omitted)")
	fs.BoolVar(&setDefault, "default", false, "mark this chat as the default")
	fs.BoolVar(&unsetDefault, "no-default", false, "remove the default flag from this chat")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config chat add [options]\n\nFind a chat by name via API and add it as an alias to the config.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if setDefault && unsetDefault {
		return fmt.Errorf("--default and --no-default are mutually exclusive")
	}

	if nameFilter == "" && flags.ChatID == "" {
		return fmt.Errorf("--name or --chat-id is required")
	}

	// Direct UUID mode — no API call needed
	if flags.ChatID != "" {
		if alias == "" {
			return fmt.Errorf("--alias is required with --chat-id")
		}
		cfg, err := config.LoadMinimal(flags)
		if err != nil {
			return err
		}
		if cfg.Chats == nil {
			cfg.Chats = make(map[string]config.ChatConfig)
		}

		if setDefault {
			if err := checkDefaultConflict(cfg.Chats, alias); err != nil {
				return err
			}
		}

		action := "added"
		existing, exists := cfg.Chats[alias]
		if exists {
			action = "updated"
		}

		// Resolve default: --default sets, --no-default clears, otherwise preserve existing
		isDefault := setDefault
		if !setDefault && !unsetDefault && exists {
			isDefault = existing.Default
		}
		cfg.Chats[alias] = config.ChatConfig{ID: flags.ChatID, Bot: flags.Bot, Default: isDefault}
		if err := cfg.SaveConfig(); err != nil {
			return err
		}

		out := fmt.Sprintf("Chat %s: %s -> %s", action, alias, flags.ChatID)
		if flags.Bot != "" {
			out += fmt.Sprintf(" (bot: %s)", flags.Bot)
		}
		fmt.Fprintln(deps.Stdout, out)
		return nil
	}

	// Search mode — find chat via API
	cfg, err := config.Load(flags)
	if err != nil {
		return err
	}

	tok, _, err := authenticate(cfg)
	if err != nil {
		return err
	}

	client := botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout())
	chats, err := client.ListChats(context.Background())
	if err != nil {
		return fmt.Errorf("listing chats: %w", err)
	}

	var matched []botapi.ChatInfo
	lowerFilter := strings.ToLower(nameFilter)
	for _, c := range chats {
		if strings.Contains(strings.ToLower(c.Name), lowerFilter) {
			matched = append(matched, c)
		}
	}

	switch len(matched) {
	case 0:
		return fmt.Errorf("no chats matching %q", nameFilter)
	case 1:
		chat := matched[0]
		if alias == "" {
			alias = slugify(chat.Name)
		}

		// Reload minimal config for saving (Load resolved runtime fields we don't want to persist)
		saveCfg, err := config.LoadMinimal(flags)
		if err != nil {
			return err
		}
		if saveCfg.Chats == nil {
			saveCfg.Chats = make(map[string]config.ChatConfig)
		}

		if setDefault {
			if err := checkDefaultConflict(saveCfg.Chats, alias); err != nil {
				return err
			}
		}

		action := "added"
		existing, exists := saveCfg.Chats[alias]
		if exists {
			action = "updated"
		}

		// Resolve default: --default sets, --no-default clears, otherwise preserve existing
		isDefault := setDefault
		if !setDefault && !unsetDefault && exists {
			isDefault = existing.Default
		}
		saveCfg.Chats[alias] = config.ChatConfig{ID: chat.GroupChatID, Bot: flags.Bot, Default: isDefault}
		if err := saveCfg.SaveConfig(); err != nil {
			return err
		}

		out := fmt.Sprintf("Chat %s: %s -> %s (%s)", action, alias, chat.GroupChatID, chat.Name)
		if flags.Bot != "" {
			out += fmt.Sprintf(" (bot: %s)", flags.Bot)
		}
		fmt.Fprintln(deps.Stdout, out)
		return nil
	default:
		fmt.Fprintf(deps.Stderr, "Multiple chats match %q:\n", nameFilter)
		for _, c := range matched {
			fmt.Fprintf(deps.Stderr, "  %s  %s (%s)\n", c.GroupChatID, c.Name, c.ChatType)
		}
		return fmt.Errorf("multiple matches, use --chat-id to specify")
	}
}

// slugify converts a chat name to a URL-friendly alias.
// "Deploy Alerts" → "deploy-alerts"
// "CI/CD notifications" → "ci-cd-notifications"
func slugify(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastHyphen := true // treat start as hyphen to avoid leading hyphen
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func printChatsUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx chats <command> [options]

Commands:
  list    List chats the bot is a member of
  info    Show detailed information about a chat

Config management: use "express-botx config chat" (add, set, rm, list).
`)
}
