# Config Edit Command

## Overview

Add `express-botx config edit` command that opens the config file in $EDITOR (fallback to vi), validates the edited YAML after save, and offers retry/rollback if validation fails. Follows the kubectl edit pattern.

## Context

- Files involved:
  - `internal/cmd/config.go` - config subcommand dispatcher and show command
  - `internal/config/config.go` - config loading, validation, path resolution
  - `internal/cmd/cmd.go` - Deps struct (has Stdin, IsTerminal)
- Related patterns: `runConfigShow()` uses `LoadMinimal()` + `config.Flags` for --config flag
- Dependencies: none (uses os/exec for editor, existing YAML + validation)

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add ValidateConfig function to config package

**Files:**
- Modify: `internal/config/config.go`

This function will be used by config edit to validate the edited file. It parses YAML and runs existing validators without resolving secrets or bot credentials.

- [ ] Add `ValidateConfig(data []byte) error` function that: unmarshals YAML into Config, then calls `validateBotConfigs()`, `ValidateDefaultChat()`, `ValidateChatBots(false)`, and `Callbacks.Validate()` if callbacks are configured
- [ ] Write tests for ValidateConfig in `internal/config/config_test.go`: valid config, invalid YAML syntax, bot with both secret+token, duplicate default chats, invalid callback rules

### Task 2: Implement config edit command

**Files:**
- Modify: `internal/cmd/config.go`

- [ ] Add `runConfigEdit(args []string, deps Deps) error` function:
  - Parse --config flag via flag.FlagSet (same pattern as runConfigShow)
  - Resolve config path via `config.LoadMinimal(flags)` + `cfg.ConfigPath()`
  - Error if config file does not exist (unlike other commands, edit requires an existing file)
  - Read original file content and save as backup
  - Determine editor: $EDITOR env var, fallback to "vi"
  - Copy config to a temp file (preserve .yaml extension for editor syntax highlighting)
  - Run editor via `os/exec.Command` with Stdin/Stdout/Stderr connected to deps (Stdin from deps.Stdin, Stdout/Stderr to os.Stderr for editor UI)
  - After editor exits: read temp file, compare with original
  - If unchanged: print "Edit cancelled, no changes made" and return
  - If changed: validate with `config.ValidateConfig(newData)`
  - If valid: write new content to original config path, print "Config updated: <path>"
  - If invalid: print validation error, prompt user: "[r]etry editing / [d]iscard changes? (r/d)" reading from deps.Stdin
  - On retry: loop back to editor with the (invalid) temp file so user can fix
  - On discard: restore original, print "Changes discarded"
- [ ] Register "edit" case in `runConfig()` switch
- [ ] Add "edit" to `printConfigUsage()` commands list
- [ ] Write tests in `internal/cmd/config_test.go`:
  - Config file not found returns error
  - No changes (editor returns same content) prints "no changes"
  - Valid edit writes new content to config file
  - Invalid YAML returns validation error (test with discard input on stdin)
  - Editor determined from EDITOR env var

### Task 3: Verify acceptance criteria

- [ ] Run full test suite (`go test ./...`)
- [ ] Run linter (`go vet ./...`)

### Task 4: Update documentation

- [ ] Update `docs/commands.md` with config edit command
- [ ] Move this plan to `docs/plans/completed/`
