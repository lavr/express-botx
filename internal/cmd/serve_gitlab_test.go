package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

func TestBuildGitlabConfig_DefaultTemplate(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		DefaultChatID: "alerts",
		Secret:        "s3cr3t",
	}
	cfg, err := buildGitlabConfig(gl, "")
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if cfg.DefaultChatID != "alerts" {
		t.Errorf("DefaultChatID = %q, want alerts", cfg.DefaultChatID)
	}
	if cfg.SecretToken != "s3cr3t" {
		t.Errorf("SecretToken = %q, want s3cr3t", cfg.SecretToken)
	}
	if cfg.Templates == nil {
		t.Fatal("Templates is nil")
	}
	// With no template override, an unrecognised event falls through to the
	// built-in generic default, which surfaces the event key and common fields.
	view := map[string]any{
		"EventKey": "wiki_page",
		"Project":  "myproj",
		"Title":    "Add feature X",
		"User":     "Alice",
		"URL":      "https://gl/myproj/-/wikis/home",
	}
	msg, err := cfg.Templates.Render("wiki_page", "wiki_page", view)
	if err != nil {
		t.Fatalf("execute template: %v", err)
	}
	for _, want := range []string{"wiki_page", "myproj", "Add feature X", "Alice"} {
		if !strings.Contains(msg, want) {
			t.Errorf("default render missing %q:\n%s", want, msg)
		}
	}
}

func TestBuildGitlabConfig_InlineTemplate(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret:    "tok",
		Templates: map[string]string{"default": "custom {{ .Event }}"},
	}
	cfg, err := buildGitlabConfig(gl, "")
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	msg := renderGitlabTemplate(t, cfg, "open")
	if msg != "custom open" {
		t.Errorf("rendered %q, want %q", msg, "custom open")
	}
}

func TestBuildGitlabConfig_TemplateFile(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "gitlab.tmpl")
	if err := os.WriteFile(tmplPath, []byte("file {{ .Event }}"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	configPath := filepath.Join(dir, "config.yaml")

	gl := &config.GitlabYAMLConfig{
		Secret:        "tok",
		TemplateFiles: map[string]string{"default": "gitlab.tmpl"}, // relative to config path dir
	}
	cfg, err := buildGitlabConfig(gl, configPath)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	msg := renderGitlabTemplate(t, cfg, "merge")
	if msg != "file merge" {
		t.Errorf("rendered %q, want %q", msg, "file merge")
	}
}

func TestBuildGitlabConfig_SecretTokenAlias(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		SecretToken: "aliased",
	}
	cfg, err := buildGitlabConfig(gl, "")
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if cfg.SecretToken != "aliased" {
		t.Errorf("SecretToken = %q, want aliased", cfg.SecretToken)
	}
}

func TestBuildGitlabConfig_SecretResolvedFromEnv(t *testing.T) {
	t.Setenv("GITLAB_TEST_TOKEN", "from-env")
	gl := &config.GitlabYAMLConfig{
		Secret: "env:GITLAB_TEST_TOKEN",
	}
	cfg, err := buildGitlabConfig(gl, "")
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if cfg.SecretToken != "from-env" {
		t.Errorf("SecretToken = %q, want from-env", cfg.SecretToken)
	}
}

func TestBuildGitlabConfig_MissingSecret(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		DefaultChatID: "alerts",
	}
	if _, err := buildGitlabConfig(gl, ""); err == nil {
		t.Fatal("expected error for missing secret token")
	}
}

// A secret reference that fails to resolve (e.g. an unset env var) must abort
// startup rather than register an endpoint with a broken token.
func TestBuildGitlabConfig_SecretResolveError(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret: "env:GITLAB_DEFINITELY_UNSET_TOKEN_XYZ",
	}
	if _, err := buildGitlabConfig(gl, ""); err == nil {
		t.Fatal("expected error when secret reference cannot be resolved")
	}
}

func TestBuildGitlabConfig_FiltersAndErrorEvents(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret: "tok",
		Events: config.GitlabEventsConfig{
			Only:    []string{"merge_request.*", "pipeline.failed", "push"},
			Exclude: []string{"merge_request.update"},
		},
		ErrorEvents: []string{"pipeline.failed", "build.failed"},
	}
	cfg, err := buildGitlabConfig(gl, "")
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg.Only, []string{"merge_request.*", "pipeline.failed", "push"}) {
		t.Errorf("Only = %v", cfg.Only)
	}
	if !reflect.DeepEqual(cfg.Exclude, []string{"merge_request.update"}) {
		t.Errorf("Exclude = %v", cfg.Exclude)
	}
	if !reflect.DeepEqual(cfg.ErrorEvents, []string{"pipeline.failed", "build.failed"}) {
		t.Errorf("ErrorEvents = %v", cfg.ErrorEvents)
	}
}

func TestBuildGitlabConfig_MissingTemplateFile(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret:        "tok",
		TemplateFiles: map[string]string{"default": "/nonexistent/does-not-exist.tmpl"},
	}
	if _, err := buildGitlabConfig(gl, ""); err == nil {
		t.Fatal("expected error for missing template file")
	}
}

func renderGitlabTemplate(t *testing.T, cfg *server.GitlabConfig, event string) string {
	t.Helper()
	// An empty kind/eventKey selects the generic default, which the inline/file
	// template overrides in these tests.
	msg, err := cfg.Templates.Render("", "", map[string]any{"Event": event})
	if err != nil {
		t.Fatalf("execute template: %v", err)
	}
	return msg
}
