package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
)

func runChats(args []string, deps Deps) error {
	if len(args) == 0 {
		printChatsUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: list, info, alias")
	}

	switch args[0] {
	case "list":
		return runChatsList(args[1:], deps)
	case "info":
		return runChatsInfo(args[1:], deps)
	case "alias":
		return runChatsAlias(args[1:], deps)
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

func runChatsAlias(args []string, deps Deps) error {
	if len(args) == 0 {
		printChatsAliasUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: list, set, rm")
	}

	switch args[0] {
	case "list":
		return runChatsAliasList(args[1:], deps)
	case "set":
		return runChatsAliasSet(args[1:], deps)
	case "rm":
		return runChatsAliasRm(args[1:], deps)
	case "--help", "-h":
		printChatsAliasUsage(deps.Stderr)
		return nil
	default:
		printChatsAliasUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: chats alias %s", args[0])
	}
}

func runChatsAliasList(args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats alias list", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats alias list [options]\n\nList configured chat aliases.\n\nOptions:\n")
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

	type aliasEntry struct {
		Name string `json:"name"`
		UUID string `json:"uuid"`
	}

	entries := make([]aliasEntry, 0, len(cfg.Chats))
	names := sortedKeys(cfg.Chats)
	for _, name := range names {
		entries = append(entries, aliasEntry{Name: name, UUID: cfg.Chats[name]})
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		if len(entries) == 0 {
			fmt.Fprintln(deps.Stdout, "No chat aliases configured.")
			fmt.Fprintf(deps.Stdout, "Add one with: express-botx chats alias set <name> <uuid>\n")
			return
		}
		fmt.Fprintf(deps.Stdout, "Chat aliases (%d):\n", len(entries))
		for _, e := range entries {
			fmt.Fprintf(deps.Stdout, "  %-20s %s\n", e.Name, e.UUID)
		}
	}, entries)
}

func runChatsAliasSet(args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats alias set", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats alias set <name> <uuid> [options]\n\nAdd or update a chat alias in the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 2 {
		return fmt.Errorf("usage: chats alias set <name> <uuid>")
	}
	name := fs.Arg(0)
	uuid := fs.Arg(1)

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	if cfg.Chats == nil {
		cfg.Chats = make(map[string]string)
	}

	action := "added"
	if _, exists := cfg.Chats[name]; exists {
		action = "updated"
	}
	cfg.Chats[name] = uuid

	if err := cfg.SaveConfig(); err != nil {
		return err
	}

	fmt.Fprintf(deps.Stdout, "Alias %s: %s -> %s\n", action, name, uuid)
	return nil
}

func runChatsAliasRm(args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats alias rm", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats alias rm <name> [options]\n\nRemove a chat alias from the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: chats alias rm <name>")
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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func printChatsAliasUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx chats alias <command> [options]

Commands:
  list    List configured chat aliases
  set     Add or update a chat alias
  rm      Remove a chat alias

Run "express-botx chats alias <command> --help" for details on a specific command.
`)
}

func printChatsUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx chats <command> [options]

Commands:
  list    List chats the bot is a member of
  info    Show detailed information about a chat
  alias   Manage chat aliases (set, list, rm)

Run "express-botx chats <command> --help" for details on a specific command.
`)
}
