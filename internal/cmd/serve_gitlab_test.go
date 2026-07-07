package cmd

import (
	"os"
	"path/filepath"
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
	if cfg.Template == nil {
		t.Fatal("Template is nil")
	}
	// Default template must be the built-in one; render known events to confirm
	// it produces the expected branch-specific output.
	if msg := renderGitlabTemplate(t, cfg, "open"); !strings.Contains(msg, "Новый MR") {
		t.Errorf("open render missing %q:\n%s", "Новый MR", msg)
	}
	if msg := renderGitlabTemplate(t, cfg, "merge"); !strings.Contains(msg, "Успешно слито") {
		t.Errorf("merge render missing %q:\n%s", "Успешно слито", msg)
	}
}

func TestBuildGitlabConfig_InlineTemplate(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret:   "tok",
		Template: "custom {{ .Event }}",
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
		Secret:       "tok",
		TemplateFile: "gitlab.tmpl", // relative to config path dir
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

func TestBuildGitlabConfig_MissingTemplateFile(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret:       "tok",
		TemplateFile: "/nonexistent/does-not-exist.tmpl",
	}
	if _, err := buildGitlabConfig(gl, ""); err == nil {
		t.Fatal("expected error for missing template file")
	}
}

func renderGitlabTemplate(t *testing.T, cfg *server.GitlabConfig, event string) string {
	t.Helper()
	var sb bytesBuffer
	if err := cfg.Template.Execute(&sb, map[string]any{"Event": event}); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	return sb.String()
}

// bytesBuffer is a tiny io.Writer to avoid importing bytes just for tests here.
type bytesBuffer struct{ b []byte }

func (w *bytesBuffer) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *bytesBuffer) String() string              { return string(w.b) }
