package cmd

import (
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

func TestBuildGrafanaConfig_MessageSourceDefaultsToTemplate(t *testing.T) {
	got, err := buildGrafanaConfig(&config.GrafanaYAMLConfig{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MessageSource != server.GrafanaMessageSourceTemplate {
		t.Fatalf("expected %q, got %q", server.GrafanaMessageSourceTemplate, got.MessageSource)
	}
}

func TestBuildGrafanaConfig_MessageSourcePropagates(t *testing.T) {
	got, err := buildGrafanaConfig(&config.GrafanaYAMLConfig{MessageSource: server.GrafanaMessageSourceWebhook}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MessageSource != server.GrafanaMessageSourceWebhook {
		t.Fatalf("expected %q, got %q", server.GrafanaMessageSourceWebhook, got.MessageSource)
	}
}

func TestBuildGrafanaConfig_MessageSourceRejectsUnknown(t *testing.T) {
	_, err := buildGrafanaConfig(&config.GrafanaYAMLConfig{MessageSource: "webhok"}, "")
	if err == nil {
		t.Fatal("expected an error for an unknown message_source")
	}
	if !strings.Contains(err.Error(), "webhok") {
		t.Fatalf("error should name the rejected value, got %v", err)
	}
}
