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

	data, _ := os.ReadFile(cfgPath)
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
