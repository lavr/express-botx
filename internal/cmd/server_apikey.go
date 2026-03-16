package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/lavr/express-botx/internal/config"
)

func runServer(args []string, deps Deps) error {
	if len(args) == 0 {
		printServerUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: apikey")
	}

	switch args[0] {
	case "apikey":
		return runServerAPIKey(args[1:], deps)
	case "--help", "-h":
		printServerUsage(deps.Stderr)
		return nil
	default:
		printServerUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: server %s", args[0])
	}
}

func runServerAPIKey(args []string, deps Deps) error {
	if len(args) == 0 {
		printServerAPIKeyUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: list, add, rm")
	}

	switch args[0] {
	case "list":
		return runServerAPIKeyList(args[1:], deps)
	case "add":
		return runServerAPIKeyAdd(args[1:], deps)
	case "rm":
		return runServerAPIKeyRm(args[1:], deps)
	case "--help", "-h":
		printServerAPIKeyUsage(deps.Stderr)
		return nil
	default:
		printServerAPIKeyUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: server apikey %s", args[0])
	}
}

func runServerAPIKeyList(args []string, deps Deps) error {
	fs := flag.NewFlagSet("server apikey list", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx server apikey list [options]\n\nList configured server API keys.\n\nOptions:\n")
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

	type apiKeyInfo struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	}
	info := make([]apiKeyInfo, len(cfg.Server.APIKeys))
	for i, k := range cfg.Server.APIKeys {
		info[i] = apiKeyInfo{Name: k.Name, Source: describeKeySource(k.Key)}
	}

	return printOutput(deps.Stdout, cfg.Format, func() {
		if len(info) == 0 {
			fmt.Fprintln(deps.Stdout, "No API keys configured.")
			fmt.Fprintln(deps.Stdout, "Add one with: express-botx server apikey add --name NAME")
			return
		}
		fmt.Fprintf(deps.Stdout, "API keys (%d):\n", len(info))
		for _, k := range info {
			fmt.Fprintf(deps.Stdout, "  %-20s %s\n", k.Name, k.Source)
		}
	}, info)
}

func runServerAPIKeyAdd(args []string, deps Deps) error {
	fs := flag.NewFlagSet("server apikey add", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags
	var name, key string

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.StringVar(&name, "name", "", "key name (required)")
	fs.StringVar(&key, "key", "", "key value (generated if omitted)")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx server apikey add --name NAME [--key VALUE] [options]\n\nAdd an API key to the server config.\nIf --key is omitted, a random key is generated.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if name == "" {
		return fmt.Errorf("--name is required")
	}

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	for _, k := range cfg.Server.APIKeys {
		if k.Name == name {
			return fmt.Errorf("API key %q already exists, remove it first with: server apikey rm %s", name, name)
		}
	}

	if key == "" {
		key, err = generateAPIKey()
		if err != nil {
			return fmt.Errorf("generating key: %w", err)
		}
		fmt.Fprintf(deps.Stdout, "Generated key: %s\n", key)
	}

	cfg.Server.APIKeys = append(cfg.Server.APIKeys, config.APIKeyConfig{
		Name: name,
		Key:  key,
	})

	if err := cfg.SaveConfig(); err != nil {
		return err
	}

	fmt.Fprintf(deps.Stdout, "API key added: %s\n", name)
	return nil
}

func runServerAPIKeyRm(args []string, deps Deps) error {
	fs := flag.NewFlagSet("server apikey rm", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx server apikey rm <name> [options]\n\nRemove an API key from the server config.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("usage: server apikey rm <name>")
	}
	name := fs.Arg(0)

	cfg, err := config.LoadMinimal(flags)
	if err != nil {
		return err
	}

	found := false
	keys := make([]config.APIKeyConfig, 0, len(cfg.Server.APIKeys))
	for _, k := range cfg.Server.APIKeys {
		if k.Name == name {
			found = true
			continue
		}
		keys = append(keys, k)
	}
	if !found {
		return fmt.Errorf("API key %q not found", name)
	}

	cfg.Server.APIKeys = keys
	if len(cfg.Server.APIKeys) == 0 {
		cfg.Server.APIKeys = nil
	}
	if err := cfg.SaveConfig(); err != nil {
		return err
	}

	fmt.Fprintf(deps.Stdout, "API key removed: %s\n", name)
	return nil
}

// describeKeySource returns a human-readable description of the key source.
func describeKeySource(key string) string {
	if strings.HasPrefix(key, "env:") {
		return key
	}
	if strings.HasPrefix(key, "vault:") {
		return key
	}
	return fmt.Sprintf("literal (%d chars)", len(key))
}

func printServerUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx server <command> [options]

Commands:
  apikey  Manage server API keys (add, list, rm)

Run "express-botx server <command> --help" for details on a specific command.
`)
}

func printServerAPIKeyUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx server apikey <command> [options]

Commands:
  list    List configured API keys
  add     Add an API key
  rm      Remove an API key

Run "express-botx server apikey <command> --help" for details on a specific command.
`)
}
