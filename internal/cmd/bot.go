package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/lavr/express-botx/internal/auth"
	"github.com/lavr/express-botx/internal/botapi"
	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/secret"
)

func runBot(args []string, deps Deps) error {
	if len(args) == 0 {
		printBotUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: ping, info, token")
	}

	switch args[0] {
	case "ping":
		return runBotPing(args[1:], deps)
	case "info":
		return runBotInfo(args[1:], deps)
	case "token":
		return runBotToken(args[1:], deps)
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
		fmt.Fprintf(deps.Stderr, "Usage: express-botx bot ping [options]\n\nCheck bot authentication and API connectivity.\n\nOptions:\n")
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

	var tok string
	if cfg.BotToken != "" {
		tok, err = secret.Resolve(cfg.BotToken)
		if err != nil {
			if !quiet {
				fmt.Fprintf(deps.Stdout, "FAIL token: %v\n", err)
			}
			return fmt.Errorf("ping failed: %w", err)
		}
	} else {
		secretKey, err := secret.Resolve(cfg.BotSecret)
		if err != nil {
			if !quiet {
				fmt.Fprintf(deps.Stdout, "FAIL auth: %v\n", err)
			}
			return fmt.Errorf("ping failed: %w", err)
		}

		signature := auth.BuildSignature(cfg.BotID, secretKey)
		tok, err = auth.GetToken(context.Background(), cfg.Host, cfg.BotID, signature, cfg.HTTPTimeout())
		if err != nil {
			if !quiet {
				fmt.Fprintf(deps.Stdout, "FAIL auth: %v\n", err)
			}
			return fmt.Errorf("ping failed: %w", err)
		}
	}

	client := botapi.NewClient(cfg.Host, tok, cfg.HTTPTimeout())
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
		fmt.Fprintf(deps.Stderr, "Usage: express-botx bot info [options]\n\nShow bot configuration and auth status.\n\nOptions:\n")
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
	fs := flag.NewFlagSet("config bot list", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config bot list [options]\n\nList bots configured in the config file.\n\nOptions:\n")
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

	entries := cfg.BotEntries()

	return printOutput(deps.Stdout, cfg.Format, func() {
		if len(entries) == 0 {
			fmt.Fprintln(deps.Stdout, "No bots configured.")
			fmt.Fprintln(deps.Stdout, "Add one with: express-botx config bot add --host HOST --bot-id UUID --secret SECRET")
			return
		}
		fmt.Fprintf(deps.Stdout, "Bots (%d):\n", len(entries))
		for _, e := range entries {
			fmt.Fprintf(deps.Stdout, "  %-20s %-30s %s\n", e.Name, e.Host, e.ID)
		}
	}, entries)
}

func runBotAdd(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config bot add", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var name, host, botID, secretVal, tokenVal string
	var saveSecret bool

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&name, "name", "", "bot name (auto-generated as bot1, bot2, ... if omitted)")
	fs.StringVar(&host, "host", "", "eXpress server host (required)")
	fs.StringVar(&botID, "bot-id", "", "bot ID (required)")
	fs.StringVar(&secretVal, "secret", "", "bot secret (exchanges for token by default)")
	fs.StringVar(&tokenVal, "token", "", "bot token (alternative to --secret)")
	fs.BoolVar(&saveSecret, "save-secret", false, "save secret instead of exchanging for token")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config bot add --host HOST --bot-id ID (--secret SECRET | --token TOKEN) [options]\n\nAdd or update a bot in the config file.\nWith --secret: exchanges for token via API (use --save-secret to keep secret).\nWith --token: saves token as-is.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if host == "" {
		return fmt.Errorf("--host is required")
	}
	if botID == "" {
		return fmt.Errorf("--bot-id is required")
	}
	if secretVal == "" && tokenVal == "" {
		return fmt.Errorf("--secret or --token is required")
	}
	if secretVal != "" && tokenVal != "" {
		return fmt.Errorf("--secret and --token are mutually exclusive")
	}
	if saveSecret && secretVal == "" {
		return fmt.Errorf("--save-secret requires --secret")
	}
	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	if cfg.Bots == nil {
		cfg.Bots = make(map[string]config.BotConfig)
	}

	// Check for existing bot with same host+bot_id under a different name
	for existingName, b := range cfg.Bots {
		if b.Host == host && b.ID == botID && (name == "" || existingName != name) {
			return fmt.Errorf("bot with this host and id already exists as %q; use --name %s to update it", existingName, existingName)
		}
	}

	if name == "" {
		for i := 1; ; i++ {
			candidate := fmt.Sprintf("bot%d", i)
			if _, exists := cfg.Bots[candidate]; !exists {
				name = candidate
				vlog.V1("bot: auto-generated name %q", name)
				break
			}
		}
	}

	action := "added"
	if _, exists := cfg.Bots[name]; exists {
		action = "updated"
	}

	// Exchange secret → token (used by default mode and --dry-run)
	exchangeToken := func() (string, error) {
		secretKey, err := secret.Resolve(secretVal)
		if err != nil {
			return "", fmt.Errorf("resolving secret: %w", err)
		}
		signature := auth.BuildSignature(botID, secretKey)
		tok, err := auth.GetToken(context.Background(), host, botID, signature, cfg.HTTPTimeout())
		if err != nil {
			return "", fmt.Errorf("exchanging secret for token: %w", err)
		}
		return tok, nil
	}

	var botCfg config.BotConfig
	var detail string
	switch {
	case tokenVal != "":
		botCfg = config.BotConfig{Host: host, ID: botID, Token: tokenVal}
		detail = "token saved"
	case saveSecret:
		botCfg = config.BotConfig{Host: host, ID: botID, Secret: secretVal}
		detail = "secret saved"
	default:
		tok, err := exchangeToken()
		if err != nil {
			return err
		}
		botCfg = config.BotConfig{Host: host, ID: botID, Token: tok}
		detail = "token obtained"
	}

	cfg.Bots[name] = botCfg

	if err := cfg.SaveConfig(); err != nil {
		return err
	}
	vlog.V1("bot: config saved to %s", cfg.ConfigPath())

	fmt.Fprintf(deps.Stdout, "Bot %s: %s (%s, %s, %s)\n", action, name, host, botID, detail)
	return nil
}

func runBotRm(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config bot rm", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config bot rm <name> [options]\n\nRemove a bot from the config file.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: config bot rm <name>")
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

func runBotToken(args []string, deps Deps) error {
	fs := flag.NewFlagSet("bot token", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	globalFlags(fs, &flags)
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx bot token [options]\n\nGet a bot token by exchanging the secret via eXpress API.\nPrints the token to stdout (useful for scripts).\n\nWith --bot or a single-bot config, uses the bot from config.\nWith --host/--bot-id/--secret, uses the provided credentials.\n\nOptions:\n")
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

	if cfg.BotToken != "" {
		// Already have a static token — just resolve and print
		tok, err := secret.Resolve(cfg.BotToken)
		if err != nil {
			return fmt.Errorf("resolving token: %w", err)
		}
		fmt.Fprintln(deps.Stdout, tok)
		return nil
	}

	secretKey, err := secret.Resolve(cfg.BotSecret)
	if err != nil {
		return fmt.Errorf("resolving secret: %w", err)
	}
	signature := auth.BuildSignature(cfg.BotID, secretKey)
	tok, err := auth.GetToken(context.Background(), cfg.Host, cfg.BotID, signature, cfg.HTTPTimeout())
	if err != nil {
		return fmt.Errorf("getting token: %w", err)
	}
	fmt.Fprintln(deps.Stdout, tok)
	return nil
}

func printBotUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx bot <command> [options]

Commands:
  ping    Check bot authentication and API connectivity
  info    Show bot configuration and auth status
  token   Get bot token (exchange secret via API or print static token)

Config management: use "express-botx config bot" (add, rm, list).
`)
}
