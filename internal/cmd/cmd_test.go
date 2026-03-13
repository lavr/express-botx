package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDeps() (Deps, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return Deps{
		Stdout:     &stdout,
		Stderr:     &stderr,
		Stdin:      strings.NewReader(""),
		IsTerminal: false,
	}, &stdout, &stderr
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- Run dispatcher ---

func TestRun_NoArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := Run(nil, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	deps, _, _ := testDeps()
	err := Run([]string{"foobar"}, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_Help(t *testing.T) {
	deps, _, stderr := testDeps()
	err := Run([]string{"--help"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "Commands:") {
		t.Errorf("expected usage output, got: %s", stderr.String())
	}
}

// --- bot dispatcher ---

func TestRunBot_NoArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := runBot(nil, deps)
	if err == nil || !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("expected subcommand required error, got: %v", err)
	}
}

func TestRunBot_Unknown(t *testing.T) {
	deps, _, _ := testDeps()
	err := runBot([]string{"foobar"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error, got: %v", err)
	}
}

// --- chats dispatcher ---

func TestRunChats_NoArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := runChats(nil, deps)
	if err == nil || !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("expected subcommand required error, got: %v", err)
	}
}

func TestRunChats_Unknown(t *testing.T) {
	deps, _, _ := testDeps()
	err := runChats([]string{"foobar"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error, got: %v", err)
	}
}

// --- user dispatcher ---

func TestRunUser_NoArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := runUser(nil, deps)
	if err == nil || !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("expected subcommand required error, got: %v", err)
	}
}

func TestRunUser_Unknown(t *testing.T) {
	deps, _, _ := testDeps()
	err := runUser([]string{"foobar"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error, got: %v", err)
	}
}

// --- bot list ---

func TestBotList_Empty(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runBotList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No bots configured") {
		t.Errorf("expected 'No bots configured', got: %s", stdout.String())
	}
}

func TestBotList_WithBots(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  alpha:
    host: alpha.com
    id: id-a
    secret: s-a
  beta:
    host: beta.com
    id: id-b
    secret: s-b
`)
	deps, stdout, _ := testDeps()

	err := runBotList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("expected both bots in output, got: %s", out)
	}
}

func TestBotList_JSON(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  mybot:
    host: h.com
    id: bot-1
    secret: s
`)
	deps, stdout, _ := testDeps()

	err := runBotList([]string{"--config", cfgPath, "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"name": "mybot"`) {
		t.Errorf("expected JSON with bot name, got: %s", stdout.String())
	}
}

// --- bot add ---

func TestBotAdd(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runBotAdd([]string{
		"--config", cfgPath,
		"--host", "new.com",
		"--bot-id", "new-id",
		"--secret", "new-secret",
		"--name", "newbot",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "added") {
		t.Errorf("expected 'added' in output, got: %s", stdout.String())
	}

	// Verify config was written
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "new.com") {
		t.Errorf("expected config to contain new.com, got: %s", string(data))
	}
}

func TestBotAdd_Update(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  existing:
    host: old.com
    id: old-id
    secret: old-s
`)
	deps, stdout, _ := testDeps()

	err := runBotAdd([]string{
		"--config", cfgPath,
		"--host", "updated.com",
		"--bot-id", "updated-id",
		"--secret", "updated-s",
		"--name", "existing",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "updated") {
		t.Errorf("expected 'updated' in output, got: %s", stdout.String())
	}
}

func TestBotAdd_AutoName(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runBotAdd([]string{
		"--config", cfgPath,
		"--host", "auto.com",
		"--bot-id", "auto-id",
		"--secret", "auto-s",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "bot1") {
		t.Errorf("expected auto-generated name 'bot1', got: %s", stdout.String())
	}
}

func TestBotAdd_AutoName_SkipsExisting(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  bot1:
    host: h
    id: b
    secret: s
`)
	deps, stdout, _ := testDeps()

	err := runBotAdd([]string{
		"--config", cfgPath,
		"--host", "new.com",
		"--bot-id", "new-id",
		"--secret", "new-s",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "bot2") {
		t.Errorf("expected auto-generated name 'bot2', got: %s", stdout.String())
	}
}

func TestBotAdd_MissingFlags(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no host", []string{"--config", cfgPath, "--bot-id", "b", "--secret", "s", "--name", "bot1"}, "--host is required"},
		{"no bot-id", []string{"--config", cfgPath, "--host", "h", "--secret", "s", "--name", "bot1"}, "--bot-id is required"},
		{"no secret", []string{"--config", cfgPath, "--host", "h", "--bot-id", "b", "--name", "bot1"}, "--secret is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runBotAdd(tt.args, deps)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("expected error containing %q, got: %v", tt.want, err)
			}
		})
	}
}

// --- bot rm ---

func TestBotRm(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  todelete:
    host: h.com
    id: b
    secret: s
`)
	deps, stdout, _ := testDeps()

	err := runBotRm([]string{"--config", cfgPath, "todelete"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "removed") {
		t.Errorf("expected 'removed' in output, got: %s", stdout.String())
	}

	data, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(data), "todelete") {
		t.Errorf("expected bot to be removed from config, got: %s", string(data))
	}
}

func TestBotRm_NotFound(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runBotRm([]string{"--config", cfgPath, "nonexistent"}, deps)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestBotRm_NoName(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runBotRm([]string{"--config", cfgPath}, deps)
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected usage error, got: %v", err)
	}
}

// --- chats alias list ---

func TestChatsAliasList_Empty(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No chat aliases") {
		t.Errorf("expected 'No chat aliases', got: %s", stdout.String())
	}
}

func TestChatsAliasList_WithAliases(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  deploy: uuid-deploy
  alerts: uuid-alerts
`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "alerts") || !strings.Contains(out, "deploy") {
		t.Errorf("expected both aliases, got: %s", out)
	}
}

func TestChatsAliasList_JSON(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  deploy: uuid-deploy
`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasList([]string{"--config", cfgPath, "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"name": "deploy"`) {
		t.Errorf("expected JSON output, got: %s", stdout.String())
	}
}

// --- chats alias set ---

func TestChatsAliasSet(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasSet([]string{"--config", cfgPath, "myalias", "uuid-123"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "added") {
		t.Errorf("expected 'added', got: %s", stdout.String())
	}

	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "uuid-123") {
		t.Errorf("expected config to contain uuid-123, got: %s", string(data))
	}
}

func TestChatsAliasSet_Update(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  existing: old-uuid
`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasSet([]string{"--config", cfgPath, "existing", "new-uuid"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "updated") {
		t.Errorf("expected 'updated', got: %s", stdout.String())
	}
}

func TestChatsAliasSet_WrongArgs(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runChatsAliasSet([]string{"--config", cfgPath, "onlyone"}, deps)
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected usage error, got: %v", err)
	}
}

// --- chats alias rm ---

func TestChatsAliasRm(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  todelete: uuid-del
`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasRm([]string{"--config", cfgPath, "todelete"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "removed") {
		t.Errorf("expected 'removed', got: %s", stdout.String())
	}
}

func TestChatsAliasRm_NotFound(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runChatsAliasRm([]string{"--config", cfgPath, "nonexistent"}, deps)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

// --- chats alias dispatcher ---

func TestChatsAlias_NoArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := runChatsAlias(nil, deps)
	if err == nil || !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("expected subcommand required, got: %v", err)
	}
}

func TestChatsAlias_Unknown(t *testing.T) {
	deps, _, _ := testDeps()
	err := runChatsAlias([]string{"foobar"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand, got: %v", err)
	}
}
