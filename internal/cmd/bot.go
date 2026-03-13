package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/lavr/express-bot/internal/auth"
	"github.com/lavr/express-bot/internal/botapi"
	"github.com/lavr/express-bot/internal/config"
	"github.com/lavr/express-bot/internal/secret"
)

func runBot(args []string, deps Deps) error {
	if len(args) == 0 {
		printBotUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: ping, info, list, add, rm")
	}

	switch args[0] {
	case "ping":
		return runBotPing(args[1:], deps)
	case "info":
		return runBotInfo(args[1:], deps)
	case "list":
		return runBotList(args[1:], deps)
	case "add":
		return runBotAdd(args[1:], deps)
	case "rm":
		return runBotRm(args[1:], deps)
	case "--help", "-h":
		printBotUsage(deps.Stderr)
		return nil
	default:
		printBotUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: bot %s", args[0])
	}
}

func runBotPing(args []string, deps Deps) error {
	fs := flag.NewFlagSet("bot ping", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var quiet bool

	globalFlags(fs, &flags)
	fs.BoolVar(&quiet, "quiet", false, "only exit code, no output")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-bot bot ping [options]\n\nCheck bot authentication and API connectivity.\n\nOptions:\n")
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

	start := time.Now()

	secretKey, err := secret.Resolve(cfg.BotSecret)
	if err != nil {
		if !quiet {
			fmt.Fprintf(deps.Stdout, "FAIL auth: %v\n", err)
		}
		return fmt.Errorf("ping failed: %w", err)
	}

	signature := auth.BuildSignature(cfg.BotID, secretKey)
	tok, err := auth.GetToken(context.Background(), cfg.Host, cfg.BotID, signature)
	if err != nil {
		if !quiet {
			fmt.Fprintf(deps.Stdout, "FAIL auth: %v\n", err)
		}
		return fmt.Errorf("ping failed: %w", err)
	}

	client := botapi.NewClient(cfg.Host, tok)
	_, err = client.ListChats(context.Background())
	if err != nil {
		if !quiet {
			fmt.Fprintf(deps.Stdout, "FAIL api: %v\n", err)
		}
		return fmt.Errorf("ping failed: %w", err)
	}

	elapsed := time.Since(start)
	if !quiet {
		fmt.Fprintf(deps.Stdout, "OK %dms\n", elapsed.Milliseconds())
	}
	return nil
}

type botInfoResult struct {
	Name       string `json:"name,omitempty"`
	BotID      string `json:"bot_id"`
	Host       string `json:"host"`
	CacheMode  string `json:"cache_mode"`
	AuthStatus string `json:"auth_status"`
	Token      string `json:"token,omitempty"`
}

func runBotInfo(args []string, deps Deps) error {
	fs := flag.NewFlagSet("bot info", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var showToken bool

	globalFlags(fs, &flags)
	fs.BoolVar(&showToken, "show-token", false, "include token in output (dangerous!)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-bot bot info [options]\n\nShow bot configuration and auth status.\n\nOptions:\n")
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

	authStatus := "ok"
	tok, _, authErr := authenticate(cfg)
	if authErr != nil {
		authStatus = authErr.Error()
	}

	info := botInfoResult{
		Name:       cfg.BotName,
		BotID:      cfg.BotID,
		Host:       cfg.Host,
		CacheMode:  cfg.Cache.Type,
		AuthStatus: authStatus,
	}
	if showToken && tok != "" {
		info.Token = tok
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		if info.Name != "" {
			fmt.Fprintf(deps.Stdout, "Name:    %s\n", info.Name)
		}
		fmt.Fprintf(deps.Stdout, "Bot ID:  %s\n", info.BotID)
		fmt.Fprintf(deps.Stdout, "Host:    %s\n", info.Host)
		fmt.Fprintf(deps.Stdout, "Cache:   %s\n", info.CacheMode)
		fmt.Fprintf(deps.Stdout, "Auth:    %s\n", info.AuthStatus)
		if info.Token != "" {
			fmt.Fprintf(deps.Stdout, "Token:   %s\n", info.Token)
		}
	}, info)
}

func runBotList(args []string, deps Deps) error {
	fs := flag.NewFlagSet("bot list", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-bot bot list [options]\n\nList bots configured in the config file.\n\nOptions:\n")
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

	type botEntry struct {
		Name string `json:"name"`
		Host string `json:"host"`
		ID   string `json:"id"`
	}

	names := make([]string, 0, len(cfg.Bots))
	for k := range cfg.Bots {
		names = append(names, k)
	}
	sort.Strings(names)

	entries := make([]botEntry, 0, len(names))
	for _, name := range names {
		b := cfg.Bots[name]
		entries = append(entries, botEntry{Name: name, Host: b.Host, ID: b.ID})
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		if len(entries) == 0 {
			fmt.Fprintln(deps.Stdout, "No bots configured.")
			fmt.Fprintln(deps.Stdout, "Add one with: express-bot bot add <name> --host HOST --bot-id ID --secret SECRET")
			return
		}
		fmt.Fprintf(deps.Stdout, "Bots (%d):\n", len(entries))
		for _, e := range entries {
			fmt.Fprintf(deps.Stdout, "  %-20s %-30s %s\n", e.Name, e.Host, e.ID)
		}
	}, entries)
}

func runBotAdd(args []string, deps Deps) error {
	fs := flag.NewFlagSet("bot add", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var host, botID, secretVal string

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&host, "host", "", "eXpress server host (required)")
	fs.StringVar(&botID, "bot-id", "", "bot UUID (required)")
	fs.StringVar(&secretVal, "secret", "", "bot secret (required)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-bot bot add <name> --host HOST --bot-id ID --secret SECRET [options]\n\nAdd or update a bot in the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: bot add <name> --host HOST --bot-id ID --secret SECRET")
	}
	name := fs.Arg(0)

	if host == "" {
		return fmt.Errorf("--host is required")
	}
	if botID == "" {
		return fmt.Errorf("--bot-id is required")
	}
	if secretVal == "" {
		return fmt.Errorf("--secret is required")
	}

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	if cfg.Bots == nil {
		cfg.Bots = make(map[string]config.BotConfig)
	}

	action := "added"
	if _, exists := cfg.Bots[name]; exists {
		action = "updated"
	}
	cfg.Bots[name] = config.BotConfig{
		Host:   host,
		ID:     botID,
		Secret: secretVal,
	}

	if err := cfg.SaveConfig(); err != nil {
		return err
	}

	fmt.Fprintf(deps.Stdout, "Bot %s: %s (%s, %s)\n", action, name, host, botID)
	return nil
}

func runBotRm(args []string, deps Deps) error {
	fs := flag.NewFlagSet("bot rm", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-bot bot rm <name> [options]\n\nRemove a bot from the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: bot rm <name>")
	}
	name := fs.Arg(0)

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	if _, exists := cfg.Bots[name]; !exists {
		return fmt.Errorf("bot %q not found", name)
	}

	delete(cfg.Bots, name)
	if len(cfg.Bots) == 0 {
		cfg.Bots = nil
	}

	if err := cfg.SaveConfig(); err != nil {
		return err
	}

	fmt.Fprintf(deps.Stdout, "Bot removed: %s\n", name)
	return nil
}

func printBotUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-bot bot <command> [options]

Commands:
  ping    Check bot authentication and API connectivity
  info    Show bot configuration and auth status
  list    List bots configured in the config file
  add     Add or update a bot in the config file
  rm      Remove a bot from the config file

Run "express-bot bot <command> --help" for details on a specific command.
`)
}
