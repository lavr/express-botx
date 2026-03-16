package cmd

import (
	"os"
	"strings"
	"testing"
)

// --- chats add (direct UUID mode) ---

func TestChatsAdd_DirectUUID(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "uuid-123",
		"--alias", "deploy",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "added") {
		t.Errorf("expected 'added', got: %s", stdout.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "uuid-123") {
		t.Errorf("expected config to contain uuid-123, got: %s", string(data))
	}
}

func TestChatsAdd_DirectUUID_WithBot(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "uuid-456",
		"--alias", "alerts",
		"--bot", "mybot",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "alerts") || !strings.Contains(out, "bot: mybot") {
		t.Errorf("expected alias with bot, got: %s", out)
	}
}

func TestChatsAdd_DirectUUID_Update(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  existing: old-uuid
`)
	deps, stdout, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "new-uuid",
		"--alias", "existing",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "updated") {
		t.Errorf("expected 'updated', got: %s", stdout.String())
	}
}

func TestChatsAdd_DirectUUID_MissingAlias(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "uuid-123",
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "--alias is required") {
		t.Errorf("expected '--alias is required' error, got: %v", err)
	}
}

func TestChatsAdd_MissingNameAndChatID(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runChatsAdd([]string{"--config", cfgPath}, deps)
	if err == nil || !strings.Contains(err.Error(), "--name or --chat-id is required") {
		t.Errorf("expected '--name or --chat-id is required' error, got: %v", err)
	}
}

// --- chats add --default ---

func TestChatsAdd_DirectUUID_WithDefault(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "uuid-123",
		"--alias", "general",
		"--default",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "added") {
		t.Errorf("expected 'added', got: %s", stdout.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "default: true") {
		t.Errorf("expected config to contain 'default: true', got: %s", content)
	}
}

func TestChatsAdd_DirectUUID_DefaultConflict(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  alerts:
    id: uuid-alerts
    default: true
`)
	deps, _, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "uuid-general",
		"--alias", "general",
		"--default",
	}, deps)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already marked as default") {
		t.Errorf("expected 'already marked as default' error, got: %v", err)
	}
}

func TestChatsAdd_DirectUUID_DefaultSameAlias(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  general:
    id: old-uuid
    default: true
`)
	deps, stdout, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "new-uuid",
		"--alias", "general",
		"--default",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "updated") {
		t.Errorf("expected 'updated', got: %s", stdout.String())
	}
}

// --- config chat list (default marker) ---

func TestChatsAliasList_ShowsDefault(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  alerts: uuid-alerts
  general:
    id: uuid-general
    default: true
`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "(default)") {
		t.Errorf("expected '(default)' marker in output, got: %s", out)
	}
	// alerts should not have default marker
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "alerts") && strings.Contains(line, "default") {
			t.Errorf("alerts should not have default marker, got: %s", line)
		}
	}
}

func TestChatsAliasList_ShowsBotAndDefault(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  general:
    id: uuid-general
    bot: mybot
    default: true
`)
	deps, stdout, _ := testDeps()

	err := runChatsAliasList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "bot: mybot") || !strings.Contains(out, "default") {
		t.Errorf("expected both 'bot: mybot' and 'default' in output, got: %s", out)
	}
}

// --- config chat set --default ---

func TestChatsAliasSet_WithDefault(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runChatsAliasSet([]string{
		"--config", cfgPath,
		"--default",
		"general", "uuid-general",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "default: true") {
		t.Errorf("expected config to contain 'default: true', got: %s", string(data))
	}
}

func TestChatsAliasSet_DefaultConflict(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  alerts:
    id: uuid-alerts
    default: true
`)
	deps, _, _ := testDeps()

	err := runChatsAliasSet([]string{
		"--config", cfgPath,
		"--default",
		"general", "uuid-general",
	}, deps)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already marked as default") {
		t.Errorf("expected 'already marked as default' error, got: %v", err)
	}
}

// --- config chat set --no-default ---

func TestChatsAliasSet_NoDefault(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  general:
    id: uuid-general
    default: true
`)
	deps, _, _ := testDeps()

	err := runChatsAliasSet([]string{
		"--config", cfgPath,
		"--no-default",
		"general", "uuid-general",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "default: true") {
		t.Errorf("expected default to be removed, got: %s", string(data))
	}
}

func TestChatsAliasSet_DefaultAndNoDefault_Conflict(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runChatsAliasSet([]string{
		"--config", cfgPath,
		"--default", "--no-default",
		"general", "uuid-general",
	}, deps)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got: %v", err)
	}
}

func TestChatsAliasSet_PreservesDefaultOnUpdate(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  general:
    id: old-uuid
    default: true
`)
	deps, _, _ := testDeps()

	// Update UUID without --default flag — should preserve default: true
	err := runChatsAliasSet([]string{
		"--config", cfgPath,
		"general", "new-uuid",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "new-uuid") {
		t.Errorf("expected config to contain new-uuid, got: %s", content)
	}
	if !strings.Contains(content, "default: true") {
		t.Errorf("expected default: true to be preserved, got: %s", content)
	}
}

// --- config chat add --no-default ---

func TestChatsAdd_DirectUUID_PreservesDefaultOnUpdate(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  general:
    id: old-uuid
    default: true
`)
	deps, _, _ := testDeps()

	// Update UUID without --default flag — should preserve default: true
	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "new-uuid",
		"--alias", "general",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "new-uuid") {
		t.Errorf("expected new-uuid, got: %s", content)
	}
	if !strings.Contains(content, "default: true") {
		t.Errorf("expected default: true to be preserved, got: %s", content)
	}
}

func TestChatsAdd_DirectUUID_NoDefault(t *testing.T) {
	cfgPath := writeTestConfig(t, `
chats:
  general:
    id: uuid-general
    default: true
`)
	deps, _, _ := testDeps()

	err := runChatsAdd([]string{
		"--config", cfgPath,
		"--chat-id", "uuid-general",
		"--alias", "general",
		"--no-default",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "default: true") {
		t.Errorf("expected default to be removed, got: %s", string(data))
	}
}

// --- slugify ---

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Deploy Alerts", "deploy-alerts"},
		{"CI/CD notifications", "ci-cd-notifications"},
		{"  spaces  ", "spaces"},
		{"MiXeD CaSe", "mixed-case"},
		{"already-slug", "already-slug"},
		{"multiple---hyphens", "multiple-hyphens"},
		{"123 numbers", "123-numbers"},
		{"", ""},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
