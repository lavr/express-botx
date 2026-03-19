package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"strconv"

	"github.com/lavr/express-botx/internal/auth"
	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
	"github.com/lavr/express-botx/internal/secret"
	"github.com/lavr/express-botx/internal/token"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// hasHelpFlag checks if args contain --help or -h anywhere.
// Go's flag package stops parsing flags after the first non-flag argument,
// so --help after a positional arg would be ignored.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// Deps holds external dependencies injected from main.
type Deps struct {
	Stdout     io.Writer
	Stderr     io.Writer
	Stdin      io.Reader
	IsTerminal bool
}

// Run is the top-level command dispatcher.
func Run(args []string, deps Deps) error {
	// Parse -v/-vv/-vvv before dispatching
	level, args := vlog.ParseVerbosity(args)
	if v := os.Getenv("EXPRESS_BOTX_VERBOSE"); v != "" && level == 0 {
		if n, err := strconv.Atoi(v); err == nil {
			level = n
		}
	}
	vlog.Level = level

	if len(args) == 0 {
		printUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: send, enqueue, serve, worker, chats, bot, config, user")
	}

	switch args[0] {
	case "send":
		return runSend(args[1:], deps)
	case "enqueue":
		return runEnqueue(args[1:], deps)
	case "serve":
		return runServe(args[1:], deps)
	case "worker":
		return runWorker(args[1:], deps)
	case "chats":
		return runChats(args[1:], deps)
	case "bot":
		return runBot(args[1:], deps)
	case "user":
		return runUser(args[1:], deps)
	case "config":
		return runConfig(args[1:], deps)
	case "--version", "version":
		fmt.Fprintln(deps.Stdout, Version)
		return nil
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
	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file (auto: ./express-botx.yaml, then <os.UserConfigDir>/express-botx/config.yaml)")
	fs.StringVar(&flags.Bot, "bot", "", "bot name from config")
	fs.StringVar(&flags.Host, "host", "", "eXpress server host")
	fs.StringVar(&flags.BotID, "bot-id", "", "bot ID (UUID)")
	fs.StringVar(&flags.Secret, "secret", "", "bot secret (literal, env:VAR, or vault:path#key)")
	fs.StringVar(&flags.Token, "token", "", "bot token (alternative to --secret; literal, env:VAR, or vault:path#key)")
	fs.BoolVar(&flags.NoCache, "no-cache", false, "disable token caching")
	fs.StringVar(&flags.Format, "format", "", "output format: text or json (default: text)")
	fs.IntVar(&flags.Verbose, "verbose", 0, "verbosity level (1-3, same as -v/-vv/-vvv)")
}

// applyVerboseFlag applies --verbose flag if -v was not already set.
func applyVerboseFlag(flags config.Flags) {
	if flags.Verbose > 0 && vlog.Level == 0 {
		vlog.Level = flags.Verbose
	}
}

// authenticate resolves credentials and returns a token.
// In token mode, returns the static token directly without API call.
func authenticate(cfg *config.Config) (string, token.Cache, error) {
	if cfg.BotToken != "" {
		vlog.V1("auth: using static token")
		resolved, err := secret.Resolve(cfg.BotToken)
		if err != nil {
			return "", nil, fmt.Errorf("resolving token: %w", err)
		}
		return resolved, token.NoopCache{}, nil
	}

	vlog.V1("auth: resolving secret")
	secretKey, err := secret.Resolve(cfg.BotSecret)
	if err != nil {
		return "", nil, fmt.Errorf("resolving secret: %w", err)
	}

	signature := auth.BuildSignature(cfg.BotID, secretKey)
	vlog.V2("auth: signature=%s", vlog.Mask(signature))
	cache := newCache(cfg.Cache)

	ctx := context.Background()
	cacheKey := cfg.CacheKey()

	tok, _ := cache.Get(ctx, cacheKey)
	if tok != "" {
		vlog.V1("auth: token loaded from cache")
		return tok, cache, nil
	}

	vlog.V1("auth: cache miss, requesting new token")
	tok, err = auth.GetToken(ctx, cfg.Host, cfg.BotID, signature, cfg.HTTPTimeout())
	if err != nil {
		return "", nil, fmt.Errorf("getting token: %w", err)
	}
	vlog.V1("auth: token obtained")
	vlog.V2("auth: token=%s", vlog.MaskBearer(tok))

	ttl := time.Duration(cfg.Cache.TTL) * time.Second
	cache.Set(ctx, cacheKey, tok, ttl)
	vlog.V1("auth: token cached (ttl: %ds)", cfg.Cache.TTL)

	return tok, cache, nil
}

// refreshToken forces a fresh token from the API.
func refreshToken(cfg *config.Config, cache token.Cache) (string, error) {
	vlog.V1("auth: refreshing token")
	secretKey, err := secret.Resolve(cfg.BotSecret)
	if err != nil {
		return "", fmt.Errorf("resolving secret: %w", err)
	}

	signature := auth.BuildSignature(cfg.BotID, secretKey)
	ctx := context.Background()

	tok, err := auth.GetToken(ctx, cfg.Host, cfg.BotID, signature, cfg.HTTPTimeout())
	if err != nil {
		return "", err
	}

	ttl := time.Duration(cfg.Cache.TTL) * time.Second
	cache.Set(ctx, cfg.CacheKey(), tok, ttl)
	vlog.V1("auth: token refreshed and cached (ttl: %ds)", cfg.Cache.TTL)
	return tok, nil
}

func newCache(cfg config.CacheConfig) token.Cache {
	switch cfg.Type {
	case "file":
		path := os.ExpandEnv(cfg.FilePath)
		if path == "" {
			path = ".express-botx-token-cache.json"
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
	fmt.Fprintf(w, `Usage: express-botx <command> [options]

Commands:
  send      Send a message and/or file to an eXpress chat
  enqueue   Enqueue a message for async delivery via broker
  serve     Start HTTP server for sending messages via API
  worker    Consume messages from broker and send via BotX API
  chats     Chat operations (list, info)
  bot       Bot operations (ping, info, token)
  config    Manage configuration (bot, chat, apikey, show)
  user      User operations (search)

Run "express-botx <command> --help" for details on a specific command.
`)
}
