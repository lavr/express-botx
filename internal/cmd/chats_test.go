package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/botapi"
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
		{"Веб-админы", "veb-adminy"},
		{"", ""},
	}
	for _, tt := range tests {
		got := slugifyChatAlias(tt.input)
		if got != tt.want {
			t.Errorf("slugifyChatAlias(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateChatAlias_FallbackAndCollisions(t *testing.T) {
	taken := map[string]struct{}{
		"veb-adminy":    {},
		"chat-12345678": {},
	}

	if got := generateChatAlias("Веб-админы", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "", taken); got != "veb-adminy-2" {
		t.Fatalf("expected collision suffix, got %q", got)
	}

	if got := generateChatAlias("!!!", "12345678-bbbb-cccc-dddd-eeeeeeeeeeee", "", taken); got != "chat-12345678-2" {
		t.Fatalf("expected fallback alias, got %q", got)
	}

	if got := generateChatAlias("АРМ ci/cd", "87654321-bbbb-cccc-dddd-eeeeeeeeeeee", "team-", map[string]struct{}{}); got != "team-arm-ci-cd" {
		t.Fatalf("expected prefixed alias, got %q", got)
	}
}

func TestChatsImport_DryRunDoesNotWrite(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", "")
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	deps, stdout, _ := testDeps()
	err = runChatsImport([]string{"--config", cfgPath, "--dry-run"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Imported chats: 1") {
		t.Fatalf("unexpected output: %s", stdout.String())
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("config changed during dry-run:\n%s", string(after))
	}
}

func TestChatsImport_AddsMultipleChats(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
		{GroupChatID: "5de954e5-4853-5a18-af55-19f0b34ba360", Name: "TM Cache Alert", ChatType: chatTypeGroup},
		{GroupChatID: "f40f109f-3c15-577c-987a-55473e7390d1", Name: "Express Conference", ChatType: chatTypeVoexCall},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", "")
	deps, _, _ := testDeps()

	err := runChatsImport([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "veb-adminy") || !strings.Contains(content, "tm-cache-alert") {
		t.Fatalf("expected imported aliases, got:\n%s", content)
	}
	if strings.Contains(content, "Express Conference") || strings.Contains(content, "f40f109f-3c15-577c-987a-55473e7390d1") {
		t.Fatalf("voex_call chat should not be imported by default:\n%s", content)
	}
}

func TestChatsImport_WithBotBinding(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "mybot", "")
	deps, _, _ := testDeps()

	err := runChatsImport([]string{"--config", cfgPath, "--bot", "mybot"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "bot: mybot") {
		t.Fatalf("expected bot binding in config, got:\n%s", content)
	}
}

func TestChatsImport_OnlyTypeVoexCall(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
		{GroupChatID: "f40f109f-3c15-577c-987a-55473e7390d1", Name: "Express Conference", ChatType: chatTypeVoexCall},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", "")
	deps, _, _ := testDeps()

	err := runChatsImport([]string{"--config", cfgPath, "--only-type", chatTypeVoexCall}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "express-conference") {
		t.Fatalf("expected voex_call alias, got:\n%s", content)
	}
	if strings.Contains(content, "veb-adminy") {
		t.Fatalf("group_chat should not be imported with --only-type voex_call:\n%s", content)
	}
}

func TestChatsImport_RepeatedImportSkipsDuplicates(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", "")
	deps, _, _ := testDeps()

	if err := runChatsImport([]string{"--config", cfgPath}, deps); err != nil {
		t.Fatalf("first import failed: %v", err)
	}
	deps, stdout, _ := testDeps()
	if err := runChatsImport([]string{"--config", cfgPath}, deps); err != nil {
		t.Fatalf("second import failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "Skipped chats: 1") {
		t.Fatalf("expected duplicate skip output, got: %s", stdout.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "47694792-1263-5e54-9214-e92ed1e609be") != 1 {
		t.Fatalf("expected chat to appear once, got:\n%s", string(data))
	}
}

func TestChatsImport_SkipsExistingUUIDUnderAnotherAlias(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", `
chats:
  ops-room: 47694792-1263-5e54-9214-e92ed1e609be
`)
	deps, stdout, _ := testDeps()

	err := runChatsImport([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "already exists as ops-room") {
		t.Fatalf("expected skip reason in output, got: %s", stdout.String())
	}
}

func TestChatsImport_AliasConflictErrors(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "99999999-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", `
chats:
  veb-adminy: 47694792-1263-5e54-9214-e92ed1e609be
`)
	deps, _, _ := testDeps()

	err := runChatsImport([]string{"--config", cfgPath}, deps)
	if err == nil || !strings.Contains(err.Error(), "alias \"veb-adminy\" already points to 47694792-1263-5e54-9214-e92ed1e609be") {
		t.Fatalf("expected alias conflict error, got: %v", err)
	}
}

func TestChatsImport_SkipExistingAndOverwrite(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "99999999-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", `
chats:
  veb-adminy:
    id: 47694792-1263-5e54-9214-e92ed1e609be
    default: true
`)

	deps, stdout, _ := testDeps()
	if err := runChatsImport([]string{"--config", cfgPath, "--skip-existing"}, deps); err != nil {
		t.Fatalf("skip-existing failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "alias conflict") {
		t.Fatalf("expected conflict to be skipped, got: %s", stdout.String())
	}

	deps, _, _ = testDeps()
	if err := runChatsImport([]string{"--config", cfgPath, "--overwrite"}, deps); err != nil {
		t.Fatalf("overwrite failed: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "99999999-1263-5e54-9214-e92ed1e609be") || !strings.Contains(content, "default: true") {
		t.Fatalf("expected overwrite to preserve metadata and update UUID, got:\n%s", content)
	}
}

func TestChatsImport_JSONOutput(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", "")
	deps, stdout, _ := testDeps()

	err := runChatsImport([]string{"--config", cfgPath, "--dry-run", "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got chatImportResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid json output: %v\n%s", err, stdout.String())
	}
	if got.DryRun != true || len(got.Added) != 1 || got.Added[0].Alias != "veb-adminy" {
		t.Fatalf("unexpected json output: %+v", got)
	}
}

func TestChatsImport_RejectsInvalidFlags(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runChatsImport([]string{"--config", cfgPath, "--skip-existing", "--overwrite"}, deps)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive error, got: %v", err)
	}

	err = runChatsImport([]string{"--config", cfgPath, "--only-type", "personal_chat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unsupported --only-type") {
		t.Fatalf("expected only-type validation error, got: %v", err)
	}
}

func TestChatsAdd_UsesImprovedAliasGeneration(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", "")
	deps, _, _ := testDeps()

	err := runChatsAdd([]string{"--config", cfgPath, "--name", "Веб"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "veb-adminy") {
		t.Fatalf("expected transliterated alias in config, got:\n%s", string(data))
	}
}

func TestChatsAdd_NamePreservesExistingBotBinding(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "47694792-1263-5e54-9214-e92ed1e609be", Name: "Веб-админы", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeImportConfig(t, srv.URL, "deploy-bot", `
chats:
  ops-room:
    id: 47694792-1263-5e54-9214-e92ed1e609be
    bot: deploy-bot
`)
	deps, _, _ := testDeps()

	err := runChatsAdd([]string{"--config", cfgPath, "--name", "Веб"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "ops-room") || !strings.Contains(content, "bot: deploy-bot") {
		t.Fatalf("expected existing bot binding to be preserved, got:\n%s", content)
	}
}

func writeImportConfig(t *testing.T, host, botName, extra string) string {
	t.Helper()
	return writeTestConfig(t, fmt.Sprintf(`
bots:
  %s:
    host: %s
    id: bot-id
    token: test-token
%s
`, botName, host, extra))
}

// --- chats list --all ---

func TestChatsList_All_Text(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup, Members: []string{"a", "b"}},
		{GroupChatID: "chat-2", Name: "Alerts", ChatType: chatTypeGroup, Members: []string{"a"}},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  alpha:
    host: %s
    id: id-alpha
    token: test-token
  beta:
    host: %s
    id: id-beta
    token: test-token
`, srv.URL, srv.URL))

	deps, stdout, _ := testDeps()
	err := runChatsList([]string{"--config", cfgPath, "--all"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "alpha:") {
		t.Errorf("expected 'alpha:' header, got: %s", out)
	}
	if !strings.Contains(out, "beta:") {
		t.Errorf("expected 'beta:' header, got: %s", out)
	}
	if !strings.Contains(out, "chat-1") || !strings.Contains(out, "General") {
		t.Errorf("expected chat details, got: %s", out)
	}
}

func TestChatsList_All_JSON(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup, Members: []string{"a"}},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  alpha:
    host: %s
    id: id-alpha
    token: test-token
`, srv.URL))

	deps, stdout, _ := testDeps()
	err := runChatsList([]string{"--config", cfgPath, "--all", "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, `"bot_name"`) {
		t.Errorf("expected bot_name field in JSON, got: %s", out)
	}
	if !strings.Contains(out, `"group_chat_id"`) {
		t.Errorf("expected group_chat_id field in JSON, got: %s", out)
	}

	var entries []chatsListEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(entries) != 1 || entries[0].BotName != "alpha" || entries[0].GroupChatID != "chat-1" {
		t.Errorf("unexpected JSON entries: %+v", entries)
	}
}

func TestChatsList_All_WithBotFlag_Error(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  alpha:
    host: h
    id: b
    token: t
`)
	deps, _, _ := testDeps()
	err := runChatsList([]string{"--config", cfgPath, "--all", "--bot", "alpha"}, deps)
	if err == nil {
		t.Fatal("expected error for --all with --bot")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}
}

func TestChatsList_All_EmptyConfig(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()
	err := runChatsList([]string{"--config", cfgPath, "--all"}, deps)
	if err == nil {
		t.Fatal("expected error for empty config with --all")
	}
	if !strings.Contains(err.Error(), "no bots configured") {
		t.Errorf("expected 'no bots configured' error, got: %v", err)
	}
}

func TestChatsList_All_PartialFailure(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup, Members: []string{"a"}},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  good:
    host: %s
    id: id-good
    token: test-token
  bad:
    host: http://unreachable.invalid
    id: id-bad
    token: bad-token
`, srv.URL))

	deps, stdout, _ := testDeps()
	err := runChatsList([]string{"--config", cfgPath, "--all"}, deps)
	if err == nil {
		t.Fatal("expected error for partial failure")
	}
	if !strings.Contains(err.Error(), "one or more bots failed") {
		t.Errorf("expected 'one or more bots failed' error, got: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "good:") {
		t.Errorf("expected good bot in output, got: %s", out)
	}
	if !strings.Contains(out, "bad:") {
		t.Errorf("expected bad bot in output, got: %s", out)
	}
	if !strings.Contains(out, "chat-1") {
		t.Errorf("expected successful bot's chats, got: %s", out)
	}
}

func TestChatsList_All_Shorthand(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  only:
    host: only.example.com
    id: id-only
    token: tok-only
`)
	deps, stdout, _ := testDeps()
	// Will fail at API call but should parse -A flag
	err := runChatsList([]string{"--config", cfgPath, "-A"}, deps)
	if err == nil {
		t.Fatal("expected error (no real API server)")
	}
	if !strings.Contains(err.Error(), "one or more bots failed") {
		t.Errorf("expected 'one or more bots failed' error, got: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "only:") {
		t.Errorf("expected bot name in output (via -A shorthand), got: %s", out)
	}
}

// --- chats import --all ---

func TestChatsImport_All_DryRun(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup},
		{GroupChatID: "chat-2", Name: "Alerts", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  alpha:
    host: %s
    id: id-alpha
    token: test-token
  beta:
    host: %s
    id: id-beta
    token: test-token
`, srv.URL, srv.URL))

	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	deps, stdout, _ := testDeps()
	err = runChatsImport([]string{"--config", cfgPath, "--all", "--dry-run"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Imported chats:") {
		t.Errorf("expected import summary, got: %s", out)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("config changed during dry-run:\n%s", string(after))
	}
}

func TestChatsImport_All_MultiBotImport(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  alpha:
    host: %s
    id: id-alpha
    token: test-token
  beta:
    host: %s
    id: id-beta
    token: test-token
`, srv.URL, srv.URL))

	deps, stdout, _ := testDeps()
	err := runChatsImport([]string{"--config", cfgPath, "--all"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// Both bots have the same chat-1 but the first import should claim it;
	// the second bot's chat should be skipped (same UUID).
	out := stdout.String()
	if !strings.Contains(out, "Imported chats:") {
		t.Errorf("expected import output, got: %s", out)
	}
	// The alias should include the bot name as prefix
	if !strings.Contains(content, "alpha-general") {
		t.Errorf("expected alpha-general alias in config, got:\n%s", content)
	}
	// Bot binding should be set to source bot name
	if !strings.Contains(content, "bot: alpha") {
		t.Errorf("expected bot: alpha binding, got:\n%s", content)
	}
}

func TestChatsImport_All_SkipExisting(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  alpha:
    host: %s
    id: id-alpha
    token: test-token
chats:
  alpha-general: chat-1
`, srv.URL))

	deps, stdout, _ := testDeps()
	err := runChatsImport([]string{"--config", cfgPath, "--all"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Skipped chats: 1") {
		t.Errorf("expected 1 skipped chat, got: %s", out)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("expected 'already exists' reason, got: %s", out)
	}
}

func TestChatsImport_All_PartialFailure(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  good:
    host: %s
    id: id-good
    token: test-token
  bad:
    host: http://unreachable.invalid
    id: id-bad
    token: bad-token
`, srv.URL))

	deps, stdout, _ := testDeps()
	err := runChatsImport([]string{"--config", cfgPath, "--all"}, deps)
	if err == nil {
		t.Fatal("expected error for partial failure")
	}
	if !strings.Contains(err.Error(), "one or more bots failed") {
		t.Errorf("expected 'one or more bots failed' error, got: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Imported chats:") {
		t.Errorf("expected import summary, got: %s", out)
	}

	// Successful bot's chats should still be imported
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "good-general") {
		t.Errorf("expected good-general alias in config, got:\n%s", string(data))
	}
}

func TestChatsImport_All_JSON(t *testing.T) {
	chats := []botapi.ChatInfo{
		{GroupChatID: "chat-1", Name: "General", ChatType: chatTypeGroup},
	}
	srv := newChatsListServer(t, chats)
	defer srv.Close()

	cfgPath := writeTestConfig(t, fmt.Sprintf(`
bots:
  alpha:
    host: %s
    id: id-alpha
    token: test-token
`, srv.URL))

	deps, stdout, _ := testDeps()
	err := runChatsImport([]string{"--config", cfgPath, "--all", "--dry-run", "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got chatImportResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid json output: %v\n%s", err, stdout.String())
	}
	if !got.DryRun {
		t.Error("expected dry_run=true")
	}
	if len(got.Added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(got.Added))
	}
	if got.Added[0].BotName != "alpha" {
		t.Errorf("expected bot_name=alpha, got %s", got.Added[0].BotName)
	}
	if got.Added[0].Alias != "alpha-general" {
		t.Errorf("expected alias=alpha-general, got %s", got.Added[0].Alias)
	}
}

func TestChatsImport_All_WithBotFlag_Error(t *testing.T) {
	cfgPath := writeTestConfig(t, `
bots:
  alpha:
    host: h
    id: b
    token: t
`)
	deps, _, _ := testDeps()
	err := runChatsImport([]string{"--config", cfgPath, "--all", "--bot", "alpha"}, deps)
	if err == nil {
		t.Fatal("expected error for --all with --bot")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}
}

func TestChatsImport_All_EmptyConfig(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()
	err := runChatsImport([]string{"--config", cfgPath, "--all"}, deps)
	if err == nil {
		t.Fatal("expected error for empty config with --all")
	}
	if !strings.Contains(err.Error(), "no bots configured") {
		t.Errorf("expected 'no bots configured' error, got: %v", err)
	}
}

func newChatsListServer(t *testing.T, chats []botapi.ChatInfo) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/botx/chats/list", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"result": chats}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	})
	return httptest.NewServer(mux)
}
