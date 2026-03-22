package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	iuliia "github.com/mehanizm/iuliia-go"
)

const (
	chatTypeGroup    = "group_chat"
	chatTypeVoexCall = "voex_call"
)

type importedChat struct {
	BotName string `json:"bot_name,omitempty"`
	Alias   string `json:"alias"`
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Type    string `json:"type,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type chatImportResult struct {
	Added   []importedChat `json:"added"`
	Skipped []importedChat `json:"skipped"`
	DryRun  bool           `json:"dry_run"`
}

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

type chatsListEntry struct {
	BotName     string `json:"bot_name,omitempty"`
	GroupChatID string `json:"group_chat_id,omitempty"`
	Name        string `json:"name,omitempty"`
	ChatType    string `json:"chat_type,omitempty"`
	Members     int    `json:"members,omitempty"`
	Error       string `json:"error,omitempty"`
}

func runChatsList(args []string, deps Deps) error {
	fs := flag.NewFlagSet("chats list", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var all bool

	globalFlags(fs, &flags)
	fs.BoolVar(&all, "all", false, "list chats for all configured bots")
	fs.BoolVar(&all, "A", false, "list chats for all configured bots (shorthand)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx chats list [options]\n\nList chats the bot is a member of.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if all {
		return runChatsListAll(flags, deps)
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

func runChatsListAll(flags config.Flags, deps Deps) error {
	if perBotFlagsSet(flags) {
		return fmt.Errorf("--all is mutually exclusive with --bot, --host, --bot-id, --secret, --token")
	}

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}
	if err := cfg.ValidateFormat(); err != nil {
		return err
	}

	names := cfg.BotNames()
	if len(names) == 0 {
		return fmt.Errorf("no bots configured")
	}

	var entries []chatsListEntry
	var anyFailed bool

	for _, name := range names {
		botCfg, err := cfg.ConfigForBot(name)
		if err != nil {
			anyFailed = true
			entries = append(entries, chatsListEntry{
				BotName: name,
				Error:   err.Error(),
			})
			continue
		}

		tok, _, authErr := authenticate(botCfg)
		if authErr != nil {
			anyFailed = true
			entries = append(entries, chatsListEntry{
				BotName: name,
				Error:   authErr.Error(),
			})
			continue
		}

		client := botapi.NewClient(botCfg.Host, tok, botCfg.HTTPTimeout())
		chats, apiErr := client.ListChats(context.Background())
		if apiErr != nil {
			anyFailed = true
			entries = append(entries, chatsListEntry{
				BotName: name,
				Error:   apiErr.Error(),
			})
			continue
		}

		for _, chat := range chats {
			entries = append(entries, chatsListEntry{
				BotName:     name,
				GroupChatID: chat.GroupChatID,
				Name:        chat.Name,
				ChatType:    chat.ChatType,
				Members:     len(chat.Members),
			})
		}
	}

	printErr := printOutput(deps.Stdout, cfg.Format, func() {
		if len(entries) == 0 {
			fmt.Fprintln(deps.Stdout, "No chats found.")
			return
		}

		currentBot := ""
		for _, e := range entries {
			if e.BotName != currentBot {
				if currentBot != "" {
					fmt.Fprintln(deps.Stdout)
				}
				fmt.Fprintf(deps.Stdout, "%s:\n", e.BotName)
				currentBot = e.BotName
			}
			if e.Error != "" {
				fmt.Fprintf(deps.Stdout, "  ERROR: %s\n", e.Error)
				continue
			}
			fmt.Fprintf(deps.Stdout, "  %-36s  %-20s  %s  (%d members)\n",
				e.GroupChatID, e.Name, e.ChatType, e.Members)
		}
	}, entries)
	if printErr != nil {
		return printErr
	}

	if anyFailed {
		return fmt.Errorf("one or more bots failed")
	}
	return nil
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

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
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

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
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

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
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

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
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

func runChatsImport(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config chat import", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var (
		all          bool
		dryRun       bool
		onlyType     string
		prefix       string
		skipExisting bool
		overwrite    bool
	)

	globalFlags(fs, &flags)
	fs.BoolVar(&all, "all", false, "import chats for all configured bots")
	fs.BoolVar(&all, "A", false, "import chats for all configured bots (shorthand)")
	fs.BoolVar(&dryRun, "dry-run", false, "show planned changes without writing config")
	fs.StringVar(&onlyType, "only-type", "", "import only chats of this type: group_chat or voex_call")
	fs.StringVar(&prefix, "prefix", "", "prefix for generated aliases")
	fs.BoolVar(&skipExisting, "skip-existing", false, "skip alias conflicts instead of failing")
	fs.BoolVar(&overwrite, "overwrite", false, "overwrite conflicting aliases with imported chat IDs")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config chat import [options]\n\nImport all bot chats into the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 0 {
		return fmt.Errorf("usage: config chat import [options]")
	}
	if skipExisting && overwrite {
		return fmt.Errorf("--overwrite and --skip-existing are mutually exclusive")
	}

	resolvedType, err := resolveImportChatType(onlyType)
	if err != nil {
		return err
	}

	if all {
		return runChatsImportAll(flags, resolvedType, prefix, dryRun, skipExisting, overwrite, deps)
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

	saveCfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}
	if err := saveCfg.ValidateFormat(); err != nil {
		return err
	}
	if saveCfg.Chats == nil {
		saveCfg.Chats = make(map[string]config.ChatConfig)
	}

	result := chatImportResult{DryRun: dryRun}
	generatedAliases := make(map[string]struct{})
	idIndex := buildChatIDIndex(saveCfg.Chats)

	for _, chat := range importableChats(chats, resolvedType) {
		if existingAlias, ok := idIndex[chat.GroupChatID]; ok {
			reason := "already exists"
			if existingAlias != "" {
				reason = fmt.Sprintf("already exists as %s", existingAlias)
			}
			result.Skipped = append(result.Skipped, importedChat{
				Alias:  existingAlias,
				ID:     chat.GroupChatID,
				Name:   chat.Name,
				Type:   chat.ChatType,
				Reason: reason,
			})
			continue
		}

		alias := generateChatAlias(chat.Name, chat.GroupChatID, prefix, generatedAliases)
		existing, exists := saveCfg.Chats[alias]
		if exists {
			if existing.ID == chat.GroupChatID {
				result.Skipped = append(result.Skipped, importedChat{
					Alias:  alias,
					ID:     chat.GroupChatID,
					Name:   chat.Name,
					Type:   chat.ChatType,
					Reason: "already exists",
				})
				continue
			}
			if skipExisting {
				result.Skipped = append(result.Skipped, importedChat{
					Alias:  alias,
					ID:     chat.GroupChatID,
					Name:   chat.Name,
					Type:   chat.ChatType,
					Reason: fmt.Sprintf("alias conflict with %s", existing.ID),
				})
				continue
			}
			if !overwrite {
				return fmt.Errorf("alias %q already points to %s, use --skip-existing or --overwrite", alias, existing.ID)
			}

			updated := existing
			updated.ID = chat.GroupChatID
			if flags.Bot != "" {
				updated.Bot = flags.Bot
			}
			saveCfg.Chats[alias] = updated
			delete(idIndex, existing.ID)
			idIndex[chat.GroupChatID] = alias
			generatedAliases[alias] = struct{}{}
			result.Added = append(result.Added, importedChat{
				Alias: alias,
				ID:    chat.GroupChatID,
				Name:  chat.Name,
				Type:  chat.ChatType,
			})
			continue
		}

		saveCfg.Chats[alias] = config.ChatConfig{ID: chat.GroupChatID, Bot: flags.Bot}
		generatedAliases[alias] = struct{}{}
		idIndex[chat.GroupChatID] = alias
		result.Added = append(result.Added, importedChat{
			Alias: alias,
			ID:    chat.GroupChatID,
			Name:  chat.Name,
			Type:  chat.ChatType,
		})
	}

	if !dryRun && len(result.Added) > 0 {
		if err := saveCfg.SaveConfig(); err != nil {
			return err
		}
	}

	return printOutput(deps.Stdout, saveCfg.Format, func() {
		fmt.Fprintf(deps.Stdout, "Imported chats: %d\n", len(result.Added))
		fmt.Fprintf(deps.Stdout, "Skipped chats: %d\n", len(result.Skipped))
		if len(result.Added) > 0 {
			fmt.Fprintln(deps.Stdout)
			fmt.Fprintln(deps.Stdout, "Added:")
			for _, item := range result.Added {
				fmt.Fprintf(deps.Stdout, "  %-20s %s", item.Alias, item.ID)
				if item.Name != "" {
					fmt.Fprintf(deps.Stdout, "  (%s)", item.Name)
				}
				fmt.Fprintln(deps.Stdout)
			}
		}
		if len(result.Skipped) > 0 {
			fmt.Fprintln(deps.Stdout)
			fmt.Fprintln(deps.Stdout, "Skipped:")
			for _, item := range result.Skipped {
				fmt.Fprintf(deps.Stdout, "  %-20s %s  %s\n", item.Alias, item.ID, item.Reason)
			}
		}
	}, result)
}

func runChatsImportAll(flags config.Flags, resolvedType, prefix string, dryRun, skipExisting, overwrite bool, deps Deps) error {
	if perBotFlagsSet(flags) {
		return fmt.Errorf("--all is mutually exclusive with --bot, --host, --bot-id, --secret, --token")
	}

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}
	if err := cfg.ValidateFormat(); err != nil {
		return err
	}

	names := cfg.BotNames()
	if len(names) == 0 {
		return fmt.Errorf("no bots configured")
	}

	// Use cfg for saving — same minimal config used for iteration.
	if cfg.Chats == nil {
		cfg.Chats = make(map[string]config.ChatConfig)
	}
	saveCfg := cfg

	result := chatImportResult{DryRun: dryRun}
	generatedAliases := make(map[string]struct{})
	idIndex := buildChatIDIndex(saveCfg.Chats)
	var anyFailed bool

	for _, name := range names {
		botCfg, err := cfg.ConfigForBot(name)
		if err != nil {
			anyFailed = true
			result.Skipped = append(result.Skipped, importedChat{
				BotName: name,
				Reason:  fmt.Sprintf("config error: %s", err.Error()),
			})
			continue
		}

		tok, _, authErr := authenticate(botCfg)
		if authErr != nil {
			anyFailed = true
			result.Skipped = append(result.Skipped, importedChat{
				BotName: name,
				Reason:  fmt.Sprintf("auth error: %s", authErr.Error()),
			})
			continue
		}

		client := botapi.NewClient(botCfg.Host, tok, botCfg.HTTPTimeout())
		chats, apiErr := client.ListChats(context.Background())
		if apiErr != nil {
			anyFailed = true
			result.Skipped = append(result.Skipped, importedChat{
				BotName: name,
				Reason:  fmt.Sprintf("API error: %s", apiErr.Error()),
			})
			continue
		}

		// Use bot name as prefix to avoid cross-bot alias collisions.
		botPrefix := name
		if prefix != "" {
			botPrefix = prefix + "-" + name
		}

		for _, chat := range importableChats(chats, resolvedType) {
			if existingAlias, ok := idIndex[chat.GroupChatID]; ok {
				reason := "already exists"
				if existingAlias != "" {
					reason = fmt.Sprintf("already exists as %s", existingAlias)
				}
				result.Skipped = append(result.Skipped, importedChat{
					BotName: name,
					Alias:   existingAlias,
					ID:      chat.GroupChatID,
					Name:    chat.Name,
					Type:    chat.ChatType,
					Reason:  reason,
				})
				continue
			}

			alias := generateChatAlias(chat.Name, chat.GroupChatID, botPrefix, generatedAliases)
			existing, exists := saveCfg.Chats[alias]
			if exists {
				if existing.ID == chat.GroupChatID {
					result.Skipped = append(result.Skipped, importedChat{
						BotName: name,
						Alias:   alias,
						ID:      chat.GroupChatID,
						Name:    chat.Name,
						Type:    chat.ChatType,
						Reason:  "already exists",
					})
					continue
				}
				if skipExisting {
					result.Skipped = append(result.Skipped, importedChat{
						BotName: name,
						Alias:   alias,
						ID:      chat.GroupChatID,
						Name:    chat.Name,
						Type:    chat.ChatType,
						Reason:  fmt.Sprintf("alias conflict with %s", existing.ID),
					})
					continue
				}
				if !overwrite {
					anyFailed = true
					result.Skipped = append(result.Skipped, importedChat{
						BotName: name,
						Alias:   alias,
						ID:      chat.GroupChatID,
						Name:    chat.Name,
						Type:    chat.ChatType,
						Reason:  fmt.Sprintf("alias conflict with %s, use --skip-existing or --overwrite", existing.ID),
					})
					continue
				}

				updated := existing
				updated.ID = chat.GroupChatID
				updated.Bot = name
				saveCfg.Chats[alias] = updated
				delete(idIndex, existing.ID)
				idIndex[chat.GroupChatID] = alias
				generatedAliases[alias] = struct{}{}
				result.Added = append(result.Added, importedChat{
					BotName: name,
					Alias:   alias,
					ID:      chat.GroupChatID,
					Name:    chat.Name,
					Type:    chat.ChatType,
				})
				continue
			}

			saveCfg.Chats[alias] = config.ChatConfig{ID: chat.GroupChatID, Bot: name}
			generatedAliases[alias] = struct{}{}
			idIndex[chat.GroupChatID] = alias
			result.Added = append(result.Added, importedChat{
				BotName: name,
				Alias:   alias,
				ID:      chat.GroupChatID,
				Name:    chat.Name,
				Type:    chat.ChatType,
			})
		}
	}

	if !dryRun && len(result.Added) > 0 {
		if err := saveCfg.SaveConfig(); err != nil {
			return err
		}
	}

	printErr := printOutput(deps.Stdout, saveCfg.Format, func() {
		fmt.Fprintf(deps.Stdout, "Imported chats: %d\n", len(result.Added))
		fmt.Fprintf(deps.Stdout, "Skipped chats: %d\n", len(result.Skipped))
		if len(result.Added) > 0 {
			fmt.Fprintln(deps.Stdout)
			fmt.Fprintln(deps.Stdout, "Added:")
			for _, item := range result.Added {
				fmt.Fprintf(deps.Stdout, "  %-20s %-20s %s", item.BotName, item.Alias, item.ID)
				if item.Name != "" {
					fmt.Fprintf(deps.Stdout, "  (%s)", item.Name)
				}
				fmt.Fprintln(deps.Stdout)
			}
		}
		if len(result.Skipped) > 0 {
			fmt.Fprintln(deps.Stdout)
			fmt.Fprintln(deps.Stdout, "Skipped:")
			for _, item := range result.Skipped {
				if item.Alias != "" {
					fmt.Fprintf(deps.Stdout, "  %-20s %-20s %s  %s\n", item.BotName, item.Alias, item.ID, item.Reason)
				} else {
					fmt.Fprintf(deps.Stdout, "  %-20s %s\n", item.BotName, item.Reason)
				}
			}
		}
	}, result)
	if printErr != nil {
		return printErr
	}

	if anyFailed {
		return fmt.Errorf("one or more bots failed")
	}
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

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
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
		// Reload minimal config for saving (Load resolved runtime fields we don't want to persist)
		saveCfg, err := config.LoadMinimal(flags)
		if err != nil {
			return err
		}
		if saveCfg.Chats == nil {
			saveCfg.Chats = make(map[string]config.ChatConfig)
		}
		if alias == "" {
			if existingAlias, ok := buildChatIDIndex(saveCfg.Chats)[chat.GroupChatID]; ok {
				alias = existingAlias
			} else {
				alias = generateChatAlias(chat.Name, chat.GroupChatID, "", map[string]struct{}{})
			}
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

		// Preserve existing bot binding if --bot was not explicitly provided.
		bot := flags.Bot
		if bot == "" && exists {
			bot = existing.Bot
		}

		// Resolve default: --default sets, --no-default clears, otherwise preserve existing
		isDefault := setDefault
		if !setDefault && !unsetDefault && exists {
			isDefault = existing.Default
		}
		saveCfg.Chats[alias] = config.ChatConfig{ID: chat.GroupChatID, Bot: bot, Default: isDefault}
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

func resolveImportChatType(onlyType string) (string, error) {
	switch onlyType {
	case "", chatTypeGroup:
		return chatTypeGroup, nil
	case chatTypeVoexCall:
		return chatTypeVoexCall, nil
	default:
		return "", fmt.Errorf("unsupported --only-type %q, use group_chat or voex_call", onlyType)
	}
}

func importableChats(chats []botapi.ChatInfo, onlyType string) []botapi.ChatInfo {
	filtered := make([]botapi.ChatInfo, 0, len(chats))
	for _, chat := range chats {
		if chat.ChatType == onlyType {
			filtered = append(filtered, chat)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		left := strings.ToLower(filtered[i].Name)
		right := strings.ToLower(filtered[j].Name)
		if left != right {
			return left < right
		}
		return filtered[i].GroupChatID < filtered[j].GroupChatID
	})
	return filtered
}

func buildChatIDIndex(chats map[string]config.ChatConfig) map[string]string {
	index := make(map[string]string, len(chats))
	for alias, chat := range chats {
		index[chat.ID] = alias
	}
	return index
}

func generateChatAlias(name, uuid, prefix string, taken map[string]struct{}) string {
	base := slugifyChatAlias(name)
	if base == "" {
		base = "chat-" + shortChatID(uuid)
	}
	if normalizedPrefix := slugifyChatAlias(prefix); normalizedPrefix != "" {
		base = normalizedPrefix + "-" + base
	}
	if _, exists := taken[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if _, exists := taken[candidate]; !exists {
			return candidate
		}
	}
}

func slugifyChatAlias(name string) string {
	name = transliterateToASCII(strings.ToLower(name))
	var b strings.Builder
	lastHyphen := true
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

func shortChatID(uuid string) string {
	if len(uuid) < 8 {
		return uuid
	}
	return uuid[:8]
}

func transliterateToASCII(s string) string {
	return iuliia.Wikipedia.Translate(s)
}

func printChatsUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx chats <command> [options]

Commands:
  list    List chats the bot is a member of
  info    Show detailed information about a chat

Config management: use "express-botx config chat" (add, set, rm, list).
`)
}
