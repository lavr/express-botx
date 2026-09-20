package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

func TestBuildIncidentRelayConfig_Defaults(t *testing.T) {
	got, err := buildIncidentRelayConfig(&config.IncidentRelayYAMLConfig{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MessageSource != server.IncidentRelayMessageSourceWebhook {
		t.Fatalf("message source = %q, want %q", got.MessageSource, server.IncidentRelayMessageSourceWebhook)
	}
	if strings.Join(got.ErrorSeverities, ",") != "critical,high" {
		t.Fatalf("error severities = %v, want [critical high]", got.ErrorSeverities)
	}
	if got.Template == nil {
		t.Fatal("template must be compiled so message_source=template works without extra config")
	}
}

func TestBuildIncidentRelayConfig_RejectsUnknownMessageSource(t *testing.T) {
	_, err := buildIncidentRelayConfig(&config.IncidentRelayYAMLConfig{MessageSource: "webhok"}, "")
	if err == nil {
		t.Fatal("expected an error for an unknown message_source")
	}
	if !strings.Contains(err.Error(), "webhok") {
		t.Fatalf("error should name the rejected value, got %v", err)
	}
}

func TestBuildIncidentRelayConfig_OverridesPropagate(t *testing.T) {
	got, err := buildIncidentRelayConfig(&config.IncidentRelayYAMLConfig{
		DefaultChatID:   "infra",
		MessageSource:   server.IncidentRelayMessageSourceTemplate,
		ErrorSeverities: []string{"critical"},
		Template:        `{{ .Title }}`,
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.DefaultChatID != "infra" {
		t.Fatalf("default chat = %q, want infra", got.DefaultChatID)
	}
	if got.MessageSource != server.IncidentRelayMessageSourceTemplate {
		t.Fatalf("message source = %q, want template", got.MessageSource)
	}
	if strings.Join(got.ErrorSeverities, ",") != "critical" {
		t.Fatalf("error severities = %v, want [critical]", got.ErrorSeverities)
	}

	var buf bytes.Buffer
	if err := got.Template.Execute(&buf, server.IncidentRelayWebhook{Title: "Disk full"}); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	if buf.String() != "Disk full" {
		t.Fatalf("configured template was not used: rendered %q", buf.String())
	}
}
