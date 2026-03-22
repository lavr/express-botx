# --all flag for bot and chats commands

## Overview

Add --all / -A flag to bot info, bot ping, bot token, chats list, and chats import commands. When specified, the command iterates over all bots from config (via LoadMinimal + cfg.Bots), runs the operation for each bot, and outputs aggregated results. Per-bot errors are captured but don't stop iteration; exit code is non-zero if any bot failed.

## Context

- Files involved:
  - `internal/cmd/bot.go` - bot info, bot ping, bot token implementations
  - `internal/cmd/chats.go` - chats list, chats import implementations
  - `internal/cmd/cmd.go` - authenticate(), globalFlags(), Deps
  - `internal/cmd/output.go` - printOutput()
  - `internal/config/config.go` - Config, LoadMinimal, resolveBot, BotConfig
  - `internal/cmd/cmd_test.go` - test utilities (testDeps, writeTestConfig)
  - `internal/cmd/chats_test.go` - chats command tests
- Related patterns: LoadMinimal for multi-bot iteration (used in config bot list); printOutput for text/json formatting; error collection pattern from chats import (added/skipped)
- Dependencies: none new

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add helper for per-bot config resolution

**Files:**
- Modify: `internal/config/config.go`

The --all commands need to iterate bots and create a resolved Config per bot. Add a method that takes a bot name and returns a Config with Host/BotID/BotSecret/BotToken/BotName populated from that bot entry, inheriting Cache and other settings from the parent config.

- [x] Add `func (c *Config) ConfigForBot(name string) (*Config, error)` that creates a resolved copy of Config for a specific bot (sets Host, BotID, BotSecret, BotToken, BotName, BotTimeout, Cache from the parent)
- [x] Add `func (c *Config) BotNames() []string` if not already present (returns sorted bot names for deterministic iteration order)
- [x] Write unit tests for ConfigForBot in `internal/config/config_test.go`
- [x] Run project test suite - must pass before task 2

### Task 2: bot info --all

**Files:**
- Modify: `internal/cmd/bot.go`
- Modify: `internal/cmd/cmd_test.go`

- [x] Add --all / -A bool flag to runBotInfo
- [x] When --all is set: use LoadMinimal to load config, iterate cfg.BotNames(), call cfg.ConfigForBot(name) for each, authenticate, collect botInfoResult per bot (with auth errors captured in AuthStatus field)
- [x] Text output: table with columns Name, Host, BotID, Cache, Auth
- [x] JSON output: array of botInfoResult objects
- [x] Return ErrSilent (or a multi-bot error) if any bot failed auth, so exit code is non-zero
- [x] Validate that --all is mutually exclusive with --bot, --host, --bot-id, --secret, --token flags
- [x] Write tests: multi-bot config with --all (text and json), --all with single bot, --all with --bot error, empty config with --all
- [x] Run project test suite - must pass before task 3

### Task 3: bot ping --all

**Files:**
- Modify: `internal/cmd/bot.go`
- Modify: `internal/cmd/cmd_test.go`

- [x] Add --all / -A bool flag to runBotPing
- [x] When --all is set: use LoadMinimal, iterate bots, for each: resolve token, create client, call ListChats, measure time
- [x] Text output: one line per bot "botname: OK 123ms" or "botname: FAIL reason"
- [x] JSON output: array of objects with name, status, elapsed_ms, error fields
- [x] Non-zero exit code if any bot failed
- [x] Validate --all is mutually exclusive with --bot/--host/--bot-id/--secret/--token
- [x] Write tests for --all ping (success, partial failure, all fail)
- [x] Run project test suite - must pass before task 4

### Task 4: bot token --all

**Files:**
- Modify: `internal/cmd/bot.go`
- Modify: `internal/cmd/cmd_test.go`

- [x] Add --all / -A bool flag to runBotToken
- [x] When --all is set: use LoadMinimal, iterate bots, resolve token for each
- [x] Text output: "botname: <token>" per line (script-friendly)
- [x] JSON output: array of objects with name, token, error fields
- [x] Non-zero exit code if any bot failed
- [x] Add --format flag support to bot token (currently not supported, needed for --all json output)
- [x] Validate --all is mutually exclusive with --bot/--host/--bot-id/--secret/--token
- [x] Write tests for --all token
- [x] Run project test suite - must pass before task 5

### Task 5: chats list --all

**Files:**
- Modify: `internal/cmd/chats.go`
- Modify: `internal/cmd/chats_test.go`

- [x] Add --all / -A bool flag to runChatsList
- [x] When --all is set: use LoadMinimal, iterate bots, authenticate each, call ListChats for each, collect results with bot name annotation
- [x] Text output: grouped by bot name, or flat table with Bot column
- [x] JSON output: array of objects with bot_name field added to each chat entry
- [x] Non-zero exit code if any bot failed (but show chats from successful bots)
- [x] Validate --all is mutually exclusive with --bot/--host/--bot-id/--secret/--token
- [x] Write tests for --all chats list
- [x] Run project test suite - must pass before task 6

### Task 6: chats import --all

**Files:**
- Modify: `internal/cmd/chats.go`
- Modify: `internal/cmd/chats_test.go`

- [x] Add --all / -A bool flag to runChatsImport
- [x] When --all is set: use LoadMinimal, iterate bots, authenticate each, fetch chats, apply import logic per bot (with --dry-run, --only-type, --prefix, --skip-existing, --overwrite all respected)
- [x] Chat aliases must include bot name to avoid cross-bot collisions (e.g. "botname-chatname" or use --prefix per bot)
- [x] Bind imported chats to their source bot via the bot field in ChatConfig
- [x] Text/JSON output: aggregated results with bot_name in added/skipped items
- [x] Non-zero exit code if any bot failed
- [x] Write tests for --all chats import (dry-run, skip-existing, multi-bot)
- [x] Run project test suite - must pass before task 7

### Task 7: Verify acceptance criteria

- [x] Run full test suite (`go test ./...`)
- [x] Run linter (`go vet ./...`)
- [x] Verify all five commands work with --all flag
- [x] Verify --all output in both text and json formats
- [x] Verify non-zero exit code on partial failure
- [x] Verify --all and --bot are mutually exclusive

### Task 8: Update documentation

- [x] Update README.md if user-facing changes
- [x] Update CLAUDE.md if internal patterns changed
- [x] Move this plan to `docs/plans/completed/`
