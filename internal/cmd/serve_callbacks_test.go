package cmd

import (
	"testing"

	"github.com/lavr/express-botx/internal/config"
)

func TestBuildBotSecretLookup_SingleBot(t *testing.T) {
	cfg := &config.Config{
		BotID:     "bot-123",
		BotSecret: "secret-abc",
	}
	lookup := buildBotSecretLookup(cfg)

	// Known bot
	sec, err := lookup("bot-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sec != "secret-abc" {
		t.Fatalf("expected secret-abc, got %s", sec)
	}

	// Unknown bot
	_, err = lookup("bot-unknown")
	if err == nil {
		t.Fatal("expected error for unknown bot_id")
	}
}

func TestBuildBotSecretLookup_NoSecret(t *testing.T) {
	cfg := &config.Config{
		BotID:    "bot-123",
		BotToken: "some-token",
	}
	lookup := buildBotSecretLookup(cfg)

	_, err := lookup("bot-123")
	if err == nil {
		t.Fatal("expected error for bot without secret")
	}
}
