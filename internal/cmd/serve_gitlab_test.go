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

const (
	gitlabChatID1 = "00000000-0000-0000-0000-000000000001"
	gitlabChatID2 = "00000000-0000-0000-0000-000000000002"
)

func gitlabChats(entries map[string]config.ChatConfig) map[string]config.ChatConfig {
	return entries
}

func TestBuildGitlabConfig_BuildsCompleteSenderRuntime(t *testing.T) {
	t.Setenv("GITLAB_DEV_TOKEN", "resolved-token")
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Name: "dev", Secret: "env:GITLAB_DEV_TOKEN", Chats: []string{"alerts"},
		Events:      config.GitlabEventsConfig{Only: []string{"push"}, Exclude: []string{"pipeline.*"}},
		ErrorEvents: []string{"pipeline.failed"},
		Templates:   map[string]string{"push": "custom {{ .Project }}"},
		Routes: []config.GitlabRouteYAMLConfig{{
			Match: map[string][]string{"event": {"push"}}, Chats: []string{"alerts"}, Stop: true,
		}},
	}}}

	cfg, err := buildGitlabConfig(gl, "", gitlabChats(map[string]config.ChatConfig{
		"alerts": {ID: gitlabChatID1, Bot: "ops"},
	}), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Senders) != 1 {
		t.Fatalf("senders = %d, want 1", len(cfg.Senders))
	}
	sender := cfg.Senders[0]
	if sender.Label != `"dev"` || sender.Secret != "resolved-token" {
		t.Errorf("identity = (%q, %q), want (\"dev\", resolved-token)", sender.Label, sender.Secret)
	}
	if !reflect.DeepEqual(sender.Scope, map[string]server.GitlabTarget{gitlabChatID1: {Target: "alerts", Bot: "ops"}}) {
		t.Errorf("scope = %#v", sender.Scope)
	}
	if !reflect.DeepEqual(sender.Targets, []string{"alerts"}) {
		t.Errorf("targets = %v, want [alerts]", sender.Targets)
	}
	if !reflect.DeepEqual(sender.Only, []string{"push"}) ||
		!reflect.DeepEqual(sender.Exclude, []string{"pipeline.*"}) ||
		!reflect.DeepEqual(sender.ErrorEvents, []string{"pipeline.failed"}) {
		t.Errorf("event settings = only:%v exclude:%v errors:%v", sender.Only, sender.Exclude, sender.ErrorEvents)
	}
	if sender.Templates == nil {
		t.Fatal("templates is nil")
	}
	message, err := sender.Templates.Render("push", "push", map[string]any{"Project": "example"})
	if err != nil {
		t.Fatal(err)
	}
	if message != "custom example" {
		t.Errorf("rendered template = %q, want custom example", message)
	}
	if len(sender.Routes) != 1 {
		t.Errorf("routes = %d, want 1", len(sender.Routes))
	}
}

func TestBuildGitlabConfig_NormalizesRouteUUIDToScopedAlias(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Name: "dev", Secret: "token", Chats: []string{"alerts"},
		Routes: []config.GitlabRouteYAMLConfig{{
			Match: map[string][]string{"event": {"push"}}, Chats: []string{gitlabChatID1},
		}},
	}}}
	aliases := map[string]config.ChatConfig{"alerts": {ID: gitlabChatID1, Bot: "ops"}}
	cfg, err := buildGitlabConfig(gl, "", aliases, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Senders[0].Scope[gitlabChatID1].Target; got != "alerts" {
		t.Fatalf("scope target = %q, want alerts", got)
	}
	if got := gl.Senders[0].Routes[0].Chats[0]; got != gitlabChatID1 {
		t.Fatalf("loaded YAML route was mutated to %q", got)
	}
}

func TestBuildGitlabConfig_RejectsDuplicateResolvedSecrets(t *testing.T) {
	t.Setenv("GITLAB_DUP_TOKEN", "same-token")
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{
		{Name: "first", Secret: "same-token", Chats: []string{"one"}},
		{Name: "second", Secret: "env:GITLAB_DUP_TOKEN", Chats: []string{"two"}},
	}}
	_, err := buildGitlabConfig(gl, "", gitlabChats(map[string]config.ChatConfig{
		"one": {ID: gitlabChatID1}, "two": {ID: gitlabChatID2},
	}), false)
	if err == nil || !strings.Contains(err.Error(), `sender "second" secret duplicates "first"`) {
		t.Fatalf("error = %v, want duplicate resolved secret owners", err)
	}
}

func TestBuildGitlabConfig_RejectsDuplicateNamesOffline(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{
		{Name: "dev", Secret: "one", Chats: []string{"alerts"}},
		{Name: "dev", Secret: "two", Chats: []string{"alerts"}},
	}}
	_, err := buildGitlabConfig(gl, "", map[string]config.ChatConfig{"alerts": {ID: gitlabChatID1}}, false)
	if err == nil || !strings.Contains(err.Error(), "name duplicates") {
		t.Fatalf("error = %v, want duplicate name validation error", err)
	}
}

func TestBuildGitlabConfig_RejectsRouteOutsideExplicitScope(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{"alerts"},
		Routes: []config.GitlabRouteYAMLConfig{{Chats: []string{"releases"}}},
	}}}
	_, err := buildGitlabConfig(gl, "", gitlabChats(map[string]config.ChatConfig{
		"alerts": {ID: gitlabChatID1}, "releases": {ID: gitlabChatID2},
	}), false)
	if err == nil || !strings.Contains(err.Error(), "outside sender scope") {
		t.Fatalf("error = %v, want route outside scope error", err)
	}
}

func TestBuildGitlabConfig_RejectsDifferentTargetsForOneUUID(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{"alerts", "oncall"},
	}}}
	_, err := buildGitlabConfig(gl, "", gitlabChats(map[string]config.ChatConfig{
		"alerts": {ID: gitlabChatID1}, "oncall": {ID: gitlabChatID1},
	}), false)
	if err == nil || !strings.Contains(err.Error(), "same chat UUID") {
		t.Fatalf("error = %v, want ambiguous delivery target error", err)
	}
}

func TestBuildGitlabConfig_DerivesRouteOnlyScope(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token",
		Routes: []config.GitlabRouteYAMLConfig{
			{Chats: []string{"alerts", "alerts"}},
			{Chats: []string{"releases", "alerts"}},
		},
	}}}
	cfg, err := buildGitlabConfig(gl, "", gitlabChats(map[string]config.ChatConfig{
		"alerts": {ID: gitlabChatID1}, "releases": {ID: gitlabChatID2},
	}), false)
	if err != nil {
		t.Fatal(err)
	}
	wantScope := map[string]server.GitlabTarget{gitlabChatID1: {Target: "alerts"}, gitlabChatID2: {Target: "releases"}}
	if !reflect.DeepEqual(cfg.Senders[0].Scope, wantScope) {
		t.Errorf("scope = %#v, want %#v", cfg.Senders[0].Scope, wantScope)
	}
	if !reflect.DeepEqual(cfg.Senders[0].Targets, []string{"alerts", "releases"}) {
		t.Errorf("targets = %v, want [alerts releases]", cfg.Senders[0].Targets)
	}
}

func TestBuildGitlabConfig_DeduplicatesRepeatedIdenticalTargets(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{"alerts", "alerts", "alerts"},
	}}}
	cfg, err := buildGitlabConfig(gl, "", map[string]config.ChatConfig{"alerts": {ID: gitlabChatID1}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Senders[0].Targets, []string{"alerts"}) {
		t.Errorf("targets = %v, want [alerts]", cfg.Senders[0].Targets)
	}
}

func TestBuildGitlabConfig_RejectsMissingAlias(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{"missing"},
	}}}
	_, err := buildGitlabConfig(gl, "", nil, false)
	if err == nil || !strings.Contains(err.Error(), `unknown chat "missing" (no aliases configured)`) {
		t.Fatalf("error = %v, want unknown chat", err)
	}

	aliases := map[string]config.ChatConfig{
		"alerts": {ID: "00000000-0000-0000-0000-000000000001"},
	}
	_, err = buildGitlabConfig(gl, "", aliases, false)
	if err == nil || !strings.Contains(err.Error(), `unknown chat alias "missing", available: alerts`) {
		t.Fatalf("error = %v, want unknown alias listing available", err)
	}
}

// An alias with an empty id must fail startup: letting it through would key the
// sender scope by "" and any other empty-id alias would canonicalize into it,
// bypassing the scope check at request time.
func TestBuildGitlabConfig_RejectsEmptyChatID(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{"alerts"},
	}}}
	aliases := map[string]config.ChatConfig{"alerts": {Bot: "ops"}}
	_, err := buildGitlabConfig(gl, "", aliases, false)
	if err == nil || !strings.Contains(err.Error(), `chat alias "alerts" has no id`) {
		t.Fatalf("error = %v, want empty-id rejection", err)
	}
}

// A route chat whose alias carries a different bot binding than the scope
// target it resolves to must fail startup: rewriting it to the scope target
// would silently switch the sending bot.
func TestBuildGitlabConfig_RejectsRouteBotConflict(t *testing.T) {
	const id = "00000000-0000-0000-0000-000000000001"
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{"alerts-dev"},
		Routes: []config.GitlabRouteYAMLConfig{{
			Match: map[string][]string{"event": {"push"}}, Chats: []string{"alerts-ops"},
		}},
	}}}
	aliases := map[string]config.ChatConfig{
		"alerts-dev": {ID: id, Bot: "dev"},
		"alerts-ops": {ID: id, Bot: "ops"},
	}
	_, err := buildGitlabConfig(gl, "", aliases, false)
	if err == nil || !strings.Contains(err.Error(), `"alerts-ops" is bound to bot "ops" but scope target "alerts-dev" is bound to bot "dev"`) {
		t.Fatalf("error = %v, want bot conflict rejection", err)
	}
}

// In single-bot mode a raw-UUID scope target has no bot binding, and a route
// referencing the same chat via a bot-bound alias is NOT a conflict: only one
// bot exists, so no switch is possible. Regression test for the false positive
// where routeBot != "" was compared against the UUID target's empty binding.
func TestBuildGitlabConfig_SingleBotAllowsBoundRouteAliasForUUIDTarget(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{gitlabChatID1},
		Routes: []config.GitlabRouteYAMLConfig{{
			Match: map[string][]string{"event": {"push"}}, Chats: []string{"alerts"},
		}},
	}}}
	aliases := map[string]config.ChatConfig{"alerts": {ID: gitlabChatID1, Bot: "main"}}
	cfg, err := buildGitlabConfig(gl, "", aliases, false)
	if err != nil {
		t.Fatalf("unexpected startup error: %v", err)
	}
	if got := cfg.Senders[0].Scope[gitlabChatID1].Target; got != gitlabChatID1 {
		t.Fatalf("scope target = %q, want the raw UUID", got)
	}
}

func TestBuildGitlabConfig_MultiBotRejectsUnboundDeliveryTargets(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		aliases map[string]config.ChatConfig
		want    string
	}{
		{name: "raw UUID", target: gitlabChatID1, want: "raw chat UUID"},
		{name: "unbound alias", target: "alerts", aliases: map[string]config.ChatConfig{"alerts": {ID: gitlabChatID1}}, want: "no bot binding"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
				Secret: "token", Chats: []string{tt.target},
			}}}
			_, err := buildGitlabConfig(gl, "", tt.aliases, true)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestBuildGitlabConfig_MultiBotAllowsUUIDRouteReferenceInExplicitScope(t *testing.T) {
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Secret: "token", Chats: []string{"alerts"},
		Routes: []config.GitlabRouteYAMLConfig{{Chats: []string{gitlabChatID1}}},
	}}}
	cfg, err := buildGitlabConfig(gl, "", map[string]config.ChatConfig{
		"alerts": {ID: gitlabChatID1, Bot: "ops"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Senders[0].Targets, []string{"alerts"}) {
		t.Errorf("targets = %v, want [alerts]", cfg.Senders[0].Targets)
	}
}

func TestBuildGitlabConfig_LoadsRelativeTemplateFilesPerSender(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dev.tmpl"), []byte("file {{ .Project }}"), 0o644); err != nil {
		t.Fatal(err)
	}
	gl := &config.GitlabYAMLConfig{Senders: []config.GitlabSenderYAMLConfig{{
		Name: "dev", Secret: "token", Chats: []string{"alerts"},
		TemplateFiles: map[string]string{"push": "dev.tmpl"},
	}}}
	cfg, err := buildGitlabConfig(gl, filepath.Join(dir, "config.yaml"), map[string]config.ChatConfig{
		"alerts": {ID: gitlabChatID1},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	message, err := cfg.Senders[0].Templates.Render("push", "push", map[string]any{"Project": "example"})
	if err != nil {
		t.Fatal(err)
	}
	if message != "file example" {
		t.Errorf("rendered template = %q, want file example", message)
	}
}
