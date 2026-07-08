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
	cfg, err := buildGitlabConfig(gl, "", nil, false)
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
	cfg, err := buildGitlabConfig(gl, "", nil, false)
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
	cfg, err := buildGitlabConfig(gl, configPath, nil, false)
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
	cfg, err := buildGitlabConfig(gl, "", nil, false)
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
	cfg, err := buildGitlabConfig(gl, "", nil, false)
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
	if _, err := buildGitlabConfig(gl, "", nil, false); err == nil {
		t.Fatal("expected error for missing secret token")
	}
}

// A secret reference that fails to resolve (e.g. an unset env var) must abort
// startup rather than register an endpoint with a broken token.
func TestBuildGitlabConfig_SecretResolveError(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret: "env:GITLAB_DEFINITELY_UNSET_TOKEN_XYZ",
	}
	if _, err := buildGitlabConfig(gl, "", nil, false); err == nil {
		t.Fatal("expected error when secret reference cannot be resolved")
	}
}

// The serve path does not run the full Config.Validate, so buildGitlabConfig must
// reject the config errors that offline validation catches: invalid event-key
// syntax and a key present in both templates and template_files.
func TestBuildGitlabConfig_RejectsInvalidConfig(t *testing.T) {
	cases := map[string]*config.GitlabYAMLConfig{
		"invalid event key in only": {
			Secret: "tok",
			Events: config.GitlabEventsConfig{Only: []string{"merge_request..open"}},
		},
		"invalid template key": {
			Secret:    "tok",
			Templates: map[string]string{"bad key!": "x"},
		},
		"duplicate templates/template_files key": {
			Secret:        "tok",
			Templates:     map[string]string{"push": "inline"},
			TemplateFiles: map[string]string{"push": "push.tmpl"},
		},
	}
	for name, gl := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := buildGitlabConfig(gl, "", nil, false); err == nil {
				t.Fatalf("expected validation error for %s", name)
			}
		})
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
	cfg, err := buildGitlabConfig(gl, "", nil, false)
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

// The config->server integration seam: YAML routes must be compiled and wired
// into GitlabConfig.Routes so the handler's fan-out path activates. Without this
// the routing engine and handler are each tested in isolation but never
// connected, so a dropped assignment would ship a silently inert feature.
func TestBuildGitlabConfig_CompilesRoutes(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret: "tok",
		Routes: []config.GitlabRouteYAMLConfig{
			{
				Match: map[string][]string{
					"project": {"team/*"},
					"event":   {"merge_request"},
				},
				Chats: []string{"backend-mrs", "releases"},
				Stop:  true,
			},
			{
				Match: map[string][]string{"branch": {"/^(main|release\\/.*)$/"}},
				Chats: []string{"oncall"},
			},
		},
	}
	cfg, err := buildGitlabConfig(gl, "", nil, false)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if len(cfg.Routes) != 2 {
		t.Fatalf("cfg.Routes length = %d, want 2", len(cfg.Routes))
	}
}

// A route with an invalid /regex/ pattern must abort startup. (ValidateForServe
// guards this upstream, so buildGitlabConfig's own compile-error return is
// defensive, but it must still surface rather than silently drop the route.)
func TestBuildGitlabConfig_RejectsInvalidRouteRegex(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret: "tok",
		Routes: []config.GitlabRouteYAMLConfig{
			{
				Match: map[string][]string{"branch": {"/[unterminated/"}},
				Chats: []string{"oncall"},
			},
		},
	}
	if _, err := buildGitlabConfig(gl, "", nil, false); err == nil {
		t.Fatal("expected error for invalid route regex")
	}
}

func TestBuildGitlabConfig_MissingTemplateFile(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Secret:        "tok",
		TemplateFiles: map[string]string{"default": "/nonexistent/does-not-exist.tmpl"},
	}
	if _, err := buildGitlabConfig(gl, "", nil, false); err == nil {
		t.Fatal("expected error for missing template file")
	}
}

// gitlabTestChats builds a chats map with the given aliases, standing in for
// the config's chats section in sender tests (values are irrelevant here).
func gitlabTestChats(aliases ...string) map[string]config.ChatConfig {
	m := map[string]config.ChatConfig{}
	for _, a := range aliases {
		m[a] = config.ChatConfig{}
	}
	return m
}

// gitlabTestChatsWithBot builds a chats map where every alias is bound to bot.
func gitlabTestChatsWithBot(bot string, aliases ...string) map[string]config.ChatConfig {
	m := map[string]config.ChatConfig{}
	for _, a := range aliases {
		m[a] = config.ChatConfig{Bot: bot}
	}
	return m
}

func TestBuildGitlabConfig_Senders(t *testing.T) {
	t.Setenv("GITLAB_TEAM_B_TOKEN", "tok-b-resolved")
	gl := &config.GitlabYAMLConfig{
		Secret: "default-tok",
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "tok-a", Chats: []string{"team-a", "team-a-alerts"}},
			{SecretToken: "env:GITLAB_TEAM_B_TOKEN", Chats: []string{"team-b"}},
		},
	}
	cfg, err := buildGitlabConfig(gl, "", gitlabTestChats("team-a", "team-a-alerts", "team-b"), false)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if cfg.SecretToken != "default-tok" {
		t.Errorf("SecretToken = %q, want default-tok", cfg.SecretToken)
	}
	want := []server.GitlabSender{
		{Secret: "tok-a", Chats: []string{"team-a", "team-a-alerts"}},
		{Secret: "tok-b-resolved", Chats: []string{"team-b"}},
	}
	if !reflect.DeepEqual(cfg.Senders, want) {
		t.Errorf("Senders = %+v, want %+v", cfg.Senders, want)
	}
}

// Senders-only mode: the default secret is optional when at least one sender
// is configured; the built config carries an empty SecretToken (which never
// authenticates) and the sender scopes.
func TestBuildGitlabConfig_SendersWithoutDefaultSecret(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "tok-a", Chats: []string{"team-a"}},
		},
	}
	cfg, err := buildGitlabConfig(gl, "", gitlabTestChats("team-a"), false)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if cfg.SecretToken != "" {
		t.Errorf("SecretToken = %q, want empty", cfg.SecretToken)
	}
	if len(cfg.Senders) != 1 || cfg.Senders[0].Secret != "tok-a" {
		t.Errorf("Senders = %+v", cfg.Senders)
	}
}

// Duplicate resolved token values make authentication ambiguous (which chat
// scope wins?), so startup must fail. The comparison is on resolved values:
// a literal and an env: reference that expand to the same string clash too.
// The error must attribute the clash to the entry that already owns the value
// (default secret or an earlier sender) so the operator knows which two of N
// team tokens collided.
func TestBuildGitlabConfig_DuplicateSenderTokens(t *testing.T) {
	t.Setenv("GITLAB_DUP_TOKEN", "same-tok")
	cases := map[string]struct {
		gl        *config.GitlabYAMLConfig
		wantOwner string
	}{
		"sender vs sender": {
			gl: &config.GitlabYAMLConfig{
				Secret: "default-tok",
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "same-tok", Chats: []string{"team-a"}},
					{Secret: "same-tok", Chats: []string{"team-b"}},
				},
			},
			wantOwner: "server.gitlab.senders[0]",
		},
		"sender vs sender via env": {
			gl: &config.GitlabYAMLConfig{
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "same-tok", Chats: []string{"team-a"}},
					{Secret: "env:GITLAB_DUP_TOKEN", Chats: []string{"team-b"}},
				},
			},
			wantOwner: "server.gitlab.senders[0]",
		},
		"sender vs default": {
			gl: &config.GitlabYAMLConfig{
				Secret: "same-tok",
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "same-tok", Chats: []string{"team-a"}},
				},
			},
			wantOwner: "server.gitlab.secret",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := buildGitlabConfig(tc.gl, "", gitlabTestChats("team-a", "team-b"), false)
			if err == nil {
				t.Fatal("expected duplicate-token error")
			}
			if !strings.Contains(err.Error(), "same value as "+tc.wantOwner) {
				t.Errorf("error = %v, want duplicate of %s", err, tc.wantOwner)
			}
		})
	}
}

// The serve path does not run the full Config.Validate, so buildGitlabConfig
// must reject broken senders itself: a sender without a secret, with an
// unresolvable secret reference, with no chats, or with a chat alias that does
// not exist in the chats section. (The "resolved secret is empty" guard is not
// coverable here: env: references error on empty values, so only a vault:
// reference can resolve to an empty string.)
func TestBuildGitlabConfig_RejectsInvalidSenders(t *testing.T) {
	t.Setenv("GITLAB_EMPTY_TOKEN", "")
	cases := map[string]struct {
		gl      *config.GitlabYAMLConfig
		wantErr string
	}{
		"sender without secret": {
			gl: &config.GitlabYAMLConfig{
				Senders: []config.GitlabSenderYAMLConfig{
					{Chats: []string{"team-a"}},
				},
			},
			wantErr: "secret is required",
		},
		"sender secret env var unset": {
			gl: &config.GitlabYAMLConfig{
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "env:GITLAB_DEFINITELY_UNSET_TOKEN_XYZ", Chats: []string{"team-a"}},
				},
			},
			wantErr: "is empty or not set",
		},
		"sender secret env var empty": {
			gl: &config.GitlabYAMLConfig{
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "env:GITLAB_EMPTY_TOKEN", Chats: []string{"team-a"}},
				},
			},
			wantErr: "is empty or not set",
		},
		"sender without chats": {
			gl: &config.GitlabYAMLConfig{
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "tok-a"},
				},
			},
			wantErr: "chats must not be empty",
		},
		// Pins the check order: chats are validated before the secret is
		// resolved, so a broken secret reference (a vault: round-trip in real
		// deployments) cannot mask the simpler chats misconfiguration.
		"sender missing chats with unresolvable secret": {
			gl: &config.GitlabYAMLConfig{
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "env:GITLAB_DEFINITELY_UNSET_TOKEN_XYZ"},
				},
			},
			wantErr: "chats must not be empty",
		},
		"sender chat references unknown alias": {
			gl: &config.GitlabYAMLConfig{
				Senders: []config.GitlabSenderYAMLConfig{
					{Secret: "tok-a", Chats: []string{"team-a", "no-such-chat"}},
				},
			},
			wantErr: `unknown chat alias "no-such-chat"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := buildGitlabConfig(tc.gl, "", gitlabTestChats("team-a"), false)
			if err == nil {
				t.Fatalf("expected error for %s", name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// Sender chats accept raw UUIDs without a matching alias entry, mirroring how
// default_chat_id and route chats are validated.
func TestBuildGitlabConfig_SenderChatUUIDPassesWithoutAlias(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "tok-a", Chats: []string{"00000000-0000-0000-0000-000000000042"}},
		},
	}
	cfg, err := buildGitlabConfig(gl, "", nil, false)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if len(cfg.Senders) != 1 || len(cfg.Senders[0].Chats) != 1 {
		t.Errorf("Senders = %+v, want single sender with the UUID chat", cfg.Senders)
	}
}

// In multi-bot mode a sender request always passes requestBot="" (?bot= is
// ignored to stop a sender picking another bot's identity), so the only way a
// delivery can get a bot is a binding on the chat itself. A raw UUID sender
// chat has no such binding and would fail every delivery at runtime with "bot
// is required" — buildGitlabConfig must reject it at startup instead.
func TestBuildGitlabConfig_MultiBot_RejectsUUIDSenderChat(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "tok-a", Chats: []string{"00000000-0000-0000-0000-000000000042"}},
		},
	}
	_, err := buildGitlabConfig(gl, "", nil, true)
	if err == nil {
		t.Fatal("expected error for raw UUID sender chat in multi-bot mode")
	}
	if !strings.Contains(err.Error(), "raw chat UUID") {
		t.Errorf("error = %v, want mention of raw chat UUID", err)
	}
}

// Same as above, but for a chat alias that exists yet carries no `bot:`
// binding — still unreachable for sender deliveries in multi-bot mode.
func TestBuildGitlabConfig_MultiBot_RejectsUnboundSenderAlias(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "tok-a", Chats: []string{"team-a"}},
		},
	}
	_, err := buildGitlabConfig(gl, "", gitlabTestChats("team-a"), true)
	if err == nil {
		t.Fatal("expected error for unbound sender chat alias in multi-bot mode")
	}
	if !strings.Contains(err.Error(), "no bot binding") {
		t.Errorf("error = %v, want mention of missing bot binding", err)
	}
}

// A sender chat alias bound to a bot resolves fine in multi-bot mode: the
// binding is the only source of a bot for that delivery.
func TestBuildGitlabConfig_MultiBot_AcceptsBoundSenderAlias(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "tok-a", Chats: []string{"team-a"}},
		},
	}
	cfg, err := buildGitlabConfig(gl, "", gitlabTestChatsWithBot("bot-a", "team-a"), true)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if len(cfg.Senders) != 1 || len(cfg.Senders[0].Chats) != 1 || cfg.Senders[0].Chats[0] != "team-a" {
		t.Errorf("Senders = %+v", cfg.Senders)
	}
}

// Duplicate chat entries within one sender would deliver the same event twice;
// buildGitlabConfig de-duplicates them preserving order (mirroring the
// union+dedup semantics of the routes path).
func TestBuildGitlabConfig_SenderChatsDeduplicated(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "tok-a", Chats: []string{"team-a", "team-a", "team-a-alerts", "team-a"}},
		},
	}
	cfg, err := buildGitlabConfig(gl, "", gitlabTestChats("team-a", "team-a-alerts"), false)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	want := []string{"team-a", "team-a-alerts"}
	if !reflect.DeepEqual(cfg.Senders[0].Chats, want) {
		t.Errorf("Chats = %v, want %v", cfg.Senders[0].Chats, want)
	}
}

// Within a sender entry, `secret` takes precedence over the `secret_token`
// alias, mirroring the top-level rule.
func TestBuildGitlabConfig_SenderSecretPrecedence(t *testing.T) {
	gl := &config.GitlabYAMLConfig{
		Senders: []config.GitlabSenderYAMLConfig{
			{Secret: "primary", SecretToken: "aliased", Chats: []string{"team-a"}},
		},
	}
	cfg, err := buildGitlabConfig(gl, "", gitlabTestChats("team-a"), false)
	if err != nil {
		t.Fatalf("buildGitlabConfig: %v", err)
	}
	if got := cfg.Senders[0].Secret; got != "primary" {
		t.Errorf("Secret = %q, want primary (secret wins over secret_token)", got)
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
