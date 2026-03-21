package cmd

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lavr/express-botx/internal/config"
)

func runConfig(args []string, deps Deps) error {
	if len(args) == 0 {
		printConfigUsage(deps.Stderr)
		return fmt.Errorf("subcommand required: bot, chat, apikey, show, edit")
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
	case "edit":
		return runConfigEdit(args[1:], deps)
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
		return fmt.Errorf("subcommand required: add, set, import, rm, list")
	}

	switch args[0] {
	case "add":
		return runChatsAdd(args[1:], deps)
	case "set":
		return runChatsAliasSet(args[1:], deps)
	case "import":
		return runChatsImport(args[1:], deps)
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

func runConfigEdit(args []string, deps Deps) error {
	fs := flag.NewFlagSet("config edit", flag.ContinueOnError)
	fs.SetOutput(deps.Stderr)
	var flags config.Flags

	fs.StringVar(&flags.ConfigPath, "config", "", "path to config file")
	fs.Usage = func() {
		fmt.Fprintf(deps.Stderr, "Usage: express-botx config edit [options]\n\nOpen config file in $EDITOR for manual editing.\n\nOptions:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	configPath, _ := config.ResolveConfigPath(flags.ConfigPath)
	if configPath == "" {
		configPath = "express-botx.yaml"
	}
	info, err := os.Stat(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("config file not found: %s", configPath)
		}
		return fmt.Errorf("accessing config file: %w", err)
	}
	configMode := info.Mode()

	// Resolve symlinks so atomic rename targets the real file, not the symlink.
	resolvedConfigPath, err := filepath.EvalSymlinks(configPath)
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}

	original, err := os.ReadFile(resolvedConfigPath)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	tmpDir, err := os.MkdirTemp("", "express-botx-config-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			os.RemoveAll(tmpDir)
		}
	}()

	tmpFile := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(tmpFile, original, 0o600); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	editorParts := splitEditorCmd(editor)
	if len(editorParts) == 0 {
		return fmt.Errorf("EDITOR is set but empty")
	}
	reader := bufio.NewReader(deps.Stdin)

	for {
		cmd := exec.Command(editorParts[0], append(editorParts[1:], tmpFile)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			cleanupTmp = false
			fmt.Fprintf(deps.Stderr, "Your edits are preserved at: %s\n", tmpFile)
			return fmt.Errorf("editor exited with error: %w", err)
		}

		newData, err := os.ReadFile(tmpFile)
		if err != nil {
			cleanupTmp = false
			fmt.Fprintf(deps.Stderr, "Your edits are preserved at: %s\n", tmpFile)
			return fmt.Errorf("reading edited file: %w", err)
		}

		if string(newData) == string(original) {
			fmt.Fprintln(deps.Stderr, "Edit cancelled, no changes made")
			return nil
		}

		if err := config.ValidateConfig(newData); err != nil {
			fmt.Fprintf(deps.Stderr, "Validation error: %s\n", err)
			fmt.Fprint(deps.Stderr, "[r]etry editing / [d]iscard changes? (r/d) ")

			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))

			if answer == "r" {
				continue
			}
			fmt.Fprintln(deps.Stderr, "Changes discarded")
			return nil
		}

		// Check for concurrent modifications right before writing.
		currentData, err := os.ReadFile(resolvedConfigPath)
		if err != nil {
			cleanupTmp = false
			fmt.Fprintf(deps.Stderr, "Your edits are preserved at: %s\n", tmpFile)
			return fmt.Errorf("reading config for conflict check: %w", err)
		}
		if string(currentData) != string(original) {
			cleanupTmp = false
			fmt.Fprintf(deps.Stderr, "Config file was modified externally while editing, aborting to avoid data loss\n")
			fmt.Fprintf(deps.Stderr, "Your edits are preserved at: %s\n", tmpFile)
			return fmt.Errorf("config file changed on disk")
		}

		// saveErr preserves the temp file and informs the user on write failure.
		saveErr := func(err error) error {
			cleanupTmp = false
			fmt.Fprintf(deps.Stderr, "Your edits are preserved at: %s\n", tmpFile)
			return err
		}

		// Atomic write: write to temp file in the same directory, then rename.
		// Fall back to direct write if the directory is not writable (e.g.
		// --config points to a writable file in a read-only directory).
		configDir := filepath.Dir(resolvedConfigPath)
		atomicTmp, atomicErr := os.CreateTemp(configDir, ".config-*.yaml.tmp")
		if atomicErr != nil {
			if !errors.Is(atomicErr, os.ErrPermission) {
				return saveErr(fmt.Errorf("creating temp file for atomic write: %w", atomicErr))
			}
			// Directory not writable; fall back to direct write.
			if err := os.WriteFile(resolvedConfigPath, newData, configMode); err != nil {
				return saveErr(fmt.Errorf("writing config: %w", err))
			}
		} else {
			atomicTmpPath := atomicTmp.Name()
			if _, err := atomicTmp.Write(newData); err != nil {
				atomicTmp.Close()
				os.Remove(atomicTmpPath)
				return saveErr(fmt.Errorf("writing config: %w", err))
			}
			if err := atomicTmp.Close(); err != nil {
				os.Remove(atomicTmpPath)
				return saveErr(fmt.Errorf("writing config: %w", err))
			}
			if err := os.Chmod(atomicTmpPath, configMode); err != nil {
				os.Remove(atomicTmpPath)
				return saveErr(fmt.Errorf("setting config permissions: %w", err))
			}
			if err := os.Rename(atomicTmpPath, resolvedConfigPath); err != nil {
				os.Remove(atomicTmpPath)
				return saveErr(fmt.Errorf("writing config: %w", err))
			}
		}
		fmt.Fprintf(deps.Stderr, "Config updated: %s\n", configPath)
		return nil
	}
}

func printConfigUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: express-botx config <command> [options]

Commands:
  bot     Manage bots (add, rm, list)
  chat    Manage chat aliases (add, set, rm, list)
  apikey  Manage server API keys (add, rm, list)
  show    Show config file location and summary
  edit    Open config file in editor for manual editing

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
  import  Import all bot chats into config
  rm      Remove a chat alias
  list    List configured chat aliases

Run "express-botx config chat <command> --help" for details on a specific command.
`)
}

// splitEditorCmd splits an editor command string into parts, honoring single
// and double quotes and backslash escapes so that paths with spaces and
// shell wrappers like `sh -c "vim \"$1\"" sh` work correctly.
func splitEditorCmd(s string) []string {
	var parts []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && !inSingle && i+1 < len(runes) && isEscapable(runes[i+1]):
			// Only consume backslash when followed by a character that
			// needs escaping. This preserves Windows-style paths like
			// C:\Program Files\... where backslashes are literal.
			i++
			cur.WriteRune(runes[i])
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case r == ' ' && !inSingle && !inDouble:
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// isEscapable returns true for characters that a backslash should escape.
// Limiting this set preserves literal backslashes in Windows paths.
func isEscapable(r rune) bool {
	return r == '\\' || r == '"' || r == '\'' || r == ' '
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
