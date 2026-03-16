package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/lavr/express-botx/internal/config"
)

func runConfig(args []string, deps Deps) error {
	if len(args) == 0 {
		printConfigUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: bot, chat, apikey, show")
	}

	switch args[0] {
	case "bot":
		return runConfigBot(args[1:], deps)
	case "chat":
		return runConfigChat(args[1:], deps)
	case "apikey":
		return runConfigAPIKey(args[1:], deps)
	case "show":
		return runConfigShow(args[1:], deps)
	case "--help", "-h":
		printConfigUsage(deps.Stderr)
		return nil
	default:
		printConfigUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: config %s", args[0])
	}
}

func runConfigBot(args []string, deps Deps) error {
	if len(args) == 0 {
		printConfigBotUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: add, rm, list")
	}

	switch args[0] {
	case "add":
		return runBotAdd(args[1:], deps)
	case "rm":
		return runBotRm(args[1:], deps)
	case "list":
		return runBotList(args[1:], deps)
	case "--help", "-h":
		printConfigBotUsage(deps.Stderr)
		return nil
	default:
		printConfigBotUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: config bot %s", args[0])
	}
}

func runConfigChat(args []string, deps Deps) error {
	if len(args) == 0 {
		printConfigChatUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: add, set, rm, list")
	}

	switch args[0] {
	case "add":
		return runChatsAdd(args[1:], deps)
	case "set":
		return runChatsAliasSet(args[1:], deps)
	case "rm":
		return runChatsAliasRm(args[1:], deps)
	case "list":
		return runChatsAliasList(args[1:], deps)
	case "--help", "-h":
		printConfigChatUsage(deps.Stderr)
		return nil
	default:
		printConfigChatUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: config chat %s", args[0])
	}
}

func runConfigAPIKey(args []string, deps Deps) error {
	if len(args) == 0 {
		printConfigAPIKeyUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: add, rm, list")
	}

	switch args[0] {
	case "add":
		return runServerAPIKeyAdd(args[1:], deps)
	case "rm":
		return runServerAPIKeyRm(args[1:], deps)
	case "list":
		return runServerAPIKeyList(args[1:], deps)
	case "--help", "-h":
		printConfigAPIKeyUsage(deps.Stderr)
		return nil
	default:
		printConfigAPIKeyUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: config apikey %s", args[0])
	}
}

func runConfigShow(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config show [options]\n\nShow config file location and summary.\n\nOptions:\n")
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

	type configSummary struct {
		Path    string `json:"path"`
		Bots    int    `json:"bots"`
		Chats   int    `json:"chats"`
		APIKeys int    `json:"api_keys"`
	}
	summary := configSummary{
		Path:    cfg.ConfigPath(),
		Bots:    len(cfg.Bots),
		Chats:   len(cfg.Chats),
		APIKeys: len(cfg.Server.APIKeys),
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		fmt.Fprintf(deps.Stdout, "Config:   %s\n", summary.Path)
		fmt.Fprintf(deps.Stdout, "Bots:     %d\n", summary.Bots)
		fmt.Fprintf(deps.Stdout, "Chats:    %d\n", summary.Chats)
		fmt.Fprintf(deps.Stdout, "API keys: %d\n", summary.APIKeys)
	}, summary)
}

func printConfigUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx config <command> [options]

Commands:
  bot     Manage bots (add, rm, list)
  chat    Manage chat aliases (add, set, rm, list)
  apikey  Manage server API keys (add, rm, list)
  show    Show config file location and summary

Run "express-botx config <command> --help" for details on a specific command.
`)
}

func printConfigBotUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx config bot <command> [options]

Commands:
  add     Add or update a bot in the config file
  rm      Remove a bot from the config file
  list    List bots configured in the config file

Run "express-botx config bot <command> --help" for details on a specific command.
`)
}

func printConfigChatUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx config chat <command> [options]

Commands:
  add     Find a chat and add it to config
  set     Add or update a chat alias
  rm      Remove a chat alias
  list    List configured chat aliases

Run "express-botx config chat <command> --help" for details on a specific command.
`)
}

func printConfigAPIKeyUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx config apikey <command> [options]

Commands:
  add     Add an API key
  rm      Remove an API key
  list    List configured API keys

Run "express-botx config apikey <command> --help" for details on a specific command.
`)
}
