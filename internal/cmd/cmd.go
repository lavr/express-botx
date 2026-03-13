package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lavr/express-send/internal/auth"
	"github.com/lavr/express-send/internal/config"
	"github.com/lavr/express-send/internal/secret"
	"github.com/lavr/express-send/internal/token"
)

// Deps holds external dependencies injected from main.
type Deps struct {
	Stdout     io.Writer
	Stderr     io.Writer
	Stdin      io.Reader
	IsTerminal bool
}

// Run is the top-level command dispatcher.
func Run(args []string, deps Deps) error {
	if len(args) == 0 {
		printUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: send, serve, chats, bot, user")
	}

	switch args[0] {
	case "send":
		return runSend(args[1:], deps)
	case "serve":
		return runServe(args[1:], deps)
	case "chats":
		return runChats(args[1:], deps)
	case "bot":
		return runBot(args[1:], deps)
	case "user":
		return runUser(args[1:], deps)
	case "--help", "-h":
		printUsage(deps.Stderr)
		return nil
	default:
		printUsage(deps.Stderr)
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

// globalFlags registers flags common to all subcommands.
func globalFlags(fs *flag.FlagSet, flags *config.Flags) {
	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file (default: ~/.config/express-send/config.yaml)")
	fs.StringVar(&flags.Bot, "bot", "", "bot name from config")
	fs.StringVar(&flags.Host, "host", "", "eXpress server host")
	fs.StringVar(&flags.BotID, "bot-id", "", "bot UUID")
	fs.StringVar(&flags.Secret, "secret", "", "bot secret (literal, env:VAR, or vault:path#key)")
	fs.BoolVar(&flags.NoCache, "no-cache", false, "disable token caching")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
}

// authenticate resolves the secret, gets or loads a cached token.
func authenticate(cfg *config.Config) (string, token.Cache, error) {
	secretKey, err := secret.Resolve(cfg.BotSecret)
	if err != nil {
		return "", nil, fmt.Errorf("resolving secret: %w", err)
	}

	signature := auth.BuildSignature(cfg.BotID, secretKey)
	cache := newCache(cfg.Cache)

	ctx := context.Background()
	cacheKey := cfg.CacheKey()

	tok, _ := cache.Get(ctx, cacheKey)
	if tok == "" {
		tok, err = auth.GetToken(ctx, cfg.Host, cfg.BotID, signature)
		if err != nil {
			return "", nil, fmt.Errorf("getting token: %w", err)
		}
		ttl := time.Duration(cfg.Cache.TTL) * time.Second
		cache.Set(ctx, cacheKey, tok, ttl)
	}

	return tok, cache, nil
}

// refreshToken forces a fresh token from the API.
func refreshToken(cfg *config.Config, cache token.Cache) (string, error) {
	secretKey, err := secret.Resolve(cfg.BotSecret)
	if err != nil {
		return "", fmt.Errorf("resolving secret: %w", err)
	}

	signature := auth.BuildSignature(cfg.BotID, secretKey)
	ctx := context.Background()

	tok, err := auth.GetToken(ctx, cfg.Host, cfg.BotID, signature)
	if err != nil {
		return "", err
	}

	ttl := time.Duration(cfg.Cache.TTL) * time.Second
	cache.Set(ctx, cfg.CacheKey(), tok, ttl)
	return tok, nil
}

func newCache(cfg config.CacheConfig) token.Cache {
	switch cfg.Type {
	case "file":
		path := cfg.FilePath
		if path == "" {
			if dir, err := os.UserCacheDir(); err == nil {
				path = dir + "/express-send/tokens.json"
			} else {
				home, _ := os.UserHomeDir()
				path = home + "/.cache/express-send/tokens.json"
			}
		}
		return &token.FileCache{Path: path}
	case "vault":
		return &token.VaultCache{
			URL:   cfg.VaultURL,
			Path:  cfg.VaultPath,
			Token: os.Getenv("VAULT_TOKEN"),
		}
	default:
		return token.NoopCache{}
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-bot <command> [options]

Commands:
  send    Send a message and/or file to an eXpress chat
  serve   Start HTTP server for sending messages via API
  chats   Manage chats (list, info, alias)
  bot     Bot management (ping, info, list, add, rm)
  user    User operations (search)

Run "express-bot <command> --help" for details on a specific command.
`)
}
