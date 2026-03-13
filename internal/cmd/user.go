package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/lavr/express-bot/internal/botapi"
	"github.com/lavr/express-bot/internal/config"
)

func runUser(args []string, deps Deps) error {
	if len(args) == 0 {
		printUserUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: search")
	}

	switch args[0] {
	case "search":
		return runUserSearch(args[1:], deps)
	case "--help", "-h":
		printUserUsage(deps.Stderr)
		return nil
	default:
		printUserUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: user %s", args[0])
	}
}

func runUserSearch(args []string, deps Deps) error {
	fs := flag.NewFlagSet("user search", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var huid, email, adLogin, adDomain string

	globalFlags(fs, &flags)
	fs.StringVar(&huid, "huid", "", "search by user HUID")
	fs.StringVar(&email, "email", "", "search by email")
	fs.StringVar(&adLogin, "ad-login", "", "search by AD login (requires --ad-domain)")
	fs.StringVar(&adDomain, "ad-domain", "", "AD domain (required with --ad-login)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-bot user search [options]\n\nSearch for a user by HUID, email, or AD login.\n\nExactly one of --huid, --email, or --ad-login is required.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// Validate: exactly one search flag
	count := 0
	if huid != "" {
		count++
	}
	if email != "" {
		count++
	}
	if adLogin != "" {
		count++
	}
	if count != 1 {
		return fmt.Errorf("exactly one of --huid, --email, or --ad-login is required")
	}
	if adLogin != "" && adDomain == "" {
		return fmt.Errorf("--ad-domain is required when using --ad-login")
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

	client := botapi.NewClient(cfg.Host, tok)
	ctx := context.Background()

	var user *botapi.UserInfo
	switch {
	case huid != "":
		user, err = client.GetUserByHUID(ctx, huid)
	case email != "":
		user, err = client.GetUserByEmail(ctx, email)
	case adLogin != "":
		user, err = client.GetUserByADLogin(ctx, adLogin, adDomain)
	}
	if err != nil {
		return fmt.Errorf("searching user: %w", err)
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		fmt.Fprintf(deps.Stdout, "HUID:       %s\n", user.HUID)
		fmt.Fprintf(deps.Stdout, "Name:       %s\n", user.Name)
		if len(user.Emails) > 0 {
			fmt.Fprintf(deps.Stdout, "Email:      %s\n", strings.Join(user.Emails, ", "))
		}
		if user.ADLogin != "" {
			fmt.Fprintf(deps.Stdout, "AD-Login:   %s\n", user.ADLogin)
			if user.ADDomain != "" {
				fmt.Fprintf(deps.Stdout, "AD-Domain:  %s\n", user.ADDomain)
			}
		}
		if user.Company != "" {
			fmt.Fprintf(deps.Stdout, "Company:    %s\n", user.Company)
		}
		if user.Department != "" {
			fmt.Fprintf(deps.Stdout, "Department: %s\n", user.Department)
		}
		if user.Title != "" {
			fmt.Fprintf(deps.Stdout, "Title:      %s\n", user.Title)
		}
		fmt.Fprintf(deps.Stdout, "Active:     %v\n", user.Active)
	}, user)
}

func printUserUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-bot user <command> [options]

Commands:
  search    Search for a user by HUID, email, or AD login

Run "express-bot user <command> --help" for details on a specific command.
`)
}
