package cmd

import (
	"os"
	"strings"
	"testing"
)

// --- server dispatcher ---

func TestRunServer_NoArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := runServer(nil, deps)
	if err == nil || !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("expected subcommand required error, got: %v", err)
	}
}

func TestRunServer_Unknown(t *testing.T) {
	deps, _, _ := testDeps()
	err := runServer([]string{"foobar"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error, got: %v", err)
	}
}

// --- server apikey dispatcher ---

func TestRunServerAPIKey_NoArgs(t *testing.T) {
	deps, _, _ := testDeps()
	err := runServerAPIKey(nil, deps)
	if err == nil || !strings.Contains(err.Error(), "subcommand required") {
		t.Errorf("expected subcommand required error, got: %v", err)
	}
}

func TestRunServerAPIKey_Unknown(t *testing.T) {
	deps, _, _ := testDeps()
	err := runServerAPIKey([]string{"foobar"}, deps)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error, got: %v", err)
	}
}

// --- server apikey list ---

func TestServerAPIKeyList_Empty(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runServerAPIKeyList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No API keys configured") {
		t.Errorf("expected 'No API keys configured', got: %s", stdout.String())
	}
}

func TestServerAPIKeyList_WithKeys(t *testing.T) {
	cfgPath := writeTestConfig(t, `
server:
  api_keys:
    - name: my-key
      key: "abcdef1234567890"
    - name: env-key
      key: "env:MY_API_KEY"
    - name: vault-key
      key: "vault:secret/data/express#api_key"
`)
	deps, stdout, _ := testDeps()

	err := runServerAPIKeyList([]string{"--config", cfgPath}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "my-key") {
		t.Errorf("expected 'my-key' in output, got: %s", out)
	}
	if !strings.Contains(out, "literal (16 chars)") {
		t.Errorf("expected 'literal (16 chars)' in output, got: %s", out)
	}
	if !strings.Contains(out, "env:MY_API_KEY") {
		t.Errorf("expected 'env:MY_API_KEY' in output, got: %s", out)
	}
	if !strings.Contains(out, "vault:secret/data/express#api_key") {
		t.Errorf("expected vault ref in output, got: %s", out)
	}
}

func TestServerAPIKeyList_JSON(t *testing.T) {
	cfgPath := writeTestConfig(t, `
server:
  api_keys:
    - name: json-key
      key: "test-value"
`)
	deps, stdout, _ := testDeps()

	err := runServerAPIKeyList([]string{"--config", cfgPath, "--format", "json"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, `"name": "json-key"`) {
		t.Errorf("expected JSON with key name, got: %s", out)
	}
	if !strings.Contains(out, `"source":`) {
		t.Errorf("expected JSON with source field, got: %s", out)
	}
	if strings.Contains(out, "test-value") {
		t.Errorf("JSON output should not contain raw key value, got: %s", out)
	}
}

// --- server apikey add ---

func TestServerAPIKeyAdd_WithKey(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runServerAPIKeyAdd([]string{"--config", cfgPath, "--name", "test-key", "--key", "my-secret"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "API key added: test-key") {
		t.Errorf("expected 'API key added', got: %s", stdout.String())
	}

	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "my-secret") {
		t.Errorf("expected config to contain key value, got: %s", string(data))
	}
}

func TestServerAPIKeyAdd_Generated(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runServerAPIKeyAdd([]string{"--config", cfgPath, "--name", "gen-key"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Generated key:") {
		t.Errorf("expected 'Generated key:', got: %s", out)
	}
	if !strings.Contains(out, "API key added: gen-key") {
		t.Errorf("expected 'API key added', got: %s", out)
	}
}

func TestServerAPIKeyAdd_EnvRef(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, stdout, _ := testDeps()

	err := runServerAPIKeyAdd([]string{"--config", cfgPath, "--name", "env-key", "--key", "env:MY_KEY"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "API key added: env-key") {
		t.Errorf("expected 'API key added', got: %s", stdout.String())
	}

	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "env:MY_KEY") {
		t.Errorf("expected config to contain env ref, got: %s", string(data))
	}
}

func TestServerAPIKeyAdd_Duplicate(t *testing.T) {
	cfgPath := writeTestConfig(t, `
server:
  api_keys:
    - name: existing
      key: "value"
`)
	deps, _, _ := testDeps()

	err := runServerAPIKeyAdd([]string{"--config", cfgPath, "--name", "existing", "--key", "new"}, deps)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' error, got: %v", err)
	}
}

func TestServerAPIKeyAdd_MissingName(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runServerAPIKeyAdd([]string{"--config", cfgPath, "--key", "value"}, deps)
	if err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Errorf("expected '--name is required' error, got: %v", err)
	}
}

// --- server apikey rm ---

func TestServerAPIKeyRm(t *testing.T) {
	cfgPath := writeTestConfig(t, `
server:
  api_keys:
    - name: todelete
      key: "value"
    - name: tokeep
      key: "other"
`)
	deps, stdout, _ := testDeps()

	err := runServerAPIKeyRm([]string{"--config", cfgPath, "todelete"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "API key removed: todelete") {
		t.Errorf("expected 'API key removed', got: %s", stdout.String())
	}

	data, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(data), "todelete") {
		t.Errorf("expected key to be removed from config, got: %s", string(data))
	}
	if !strings.Contains(string(data), "tokeep") {
		t.Errorf("expected other key to remain, got: %s", string(data))
	}
}

func TestServerAPIKeyRm_NotFound(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runServerAPIKeyRm([]string{"--config", cfgPath, "nonexistent"}, deps)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestServerAPIKeyRm_NoName(t *testing.T) {
	cfgPath := writeTestConfig(t, `{}`)
	deps, _, _ := testDeps()

	err := runServerAPIKeyRm([]string{"--config", cfgPath}, deps)
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected usage error, got: %v", err)
	}
}

// --- describeKeySource ---

func TestDescribeKeySource(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"env:MY_VAR", "env:MY_VAR"},
		{"vault:secret/data/express#key", "vault:secret/data/express#key"},
		{"abcdef1234567890", "literal (16 chars)"},
		{"", "literal (0 chars)"},
	}
	for _, tt := range tests {
		got := describeKeySource(tt.key)
		if got != tt.want {
			t.Errorf("describeKeySource(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
