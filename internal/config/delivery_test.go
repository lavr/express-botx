package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBotDeliveryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		bot        string
		wantLimit  int
		wantSuffix string
	}{
		{
			name:       "absent keys take the defaults",
			raw:        "bots:\n  main:\n    host: a\n",
			bot:        "main",
			wantLimit:  DefaultMaxMessageLength,
			wantSuffix: DefaultTruncateSuffix,
		},
		{
			name:       "an explicit zero disables truncation",
			raw:        "bots:\n  main:\n    host: a\n    max_message_length: 0\n",
			bot:        "main",
			wantLimit:  0,
			wantSuffix: DefaultTruncateSuffix,
		},
		{
			name:       "an explicit empty suffix cuts without one",
			raw:        "bots:\n  main:\n    host: a\n    truncate_suffix: \"\"\n",
			bot:        "main",
			wantLimit:  DefaultMaxMessageLength,
			wantSuffix: "",
		},
		{
			name:       "explicit values propagate",
			raw:        "bots:\n  main:\n    host: a\n    max_message_length: 1000\n    truncate_suffix: \"[cut]\"\n",
			bot:        "main",
			wantLimit:  1000,
			wantSuffix: "[cut]",
		},
		{
			name:       "a negative limit is treated as disabled",
			raw:        "bots:\n  main:\n    host: a\n    max_message_length: -5\n",
			bot:        "main",
			wantLimit:  0,
			wantSuffix: DefaultTruncateSuffix,
		},
		{
			name:       "each bot carries its own limit",
			raw:        "bots:\n  main:\n    host: a\n    max_message_length: 1000\n  other:\n    host: b\n",
			bot:        "other",
			wantLimit:  DefaultMaxMessageLength,
			wantSuffix: DefaultTruncateSuffix,
		},
		{
			name:       "an unknown bot falls back to the defaults",
			raw:        "bots:\n  main:\n    host: a\n    max_message_length: 1000\n",
			bot:        "nope",
			wantLimit:  DefaultMaxMessageLength,
			wantSuffix: DefaultTruncateSuffix,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := yaml.Unmarshal([]byte(tt.raw), &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			limit, suffix := cfg.BotDeliveryPolicy(tt.bot)
			if limit != tt.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tt.wantLimit)
			}
			if suffix != tt.wantSuffix {
				t.Errorf("suffix = %q, want %q", suffix, tt.wantSuffix)
			}
		})
	}
}

// The policy is deliberately not projected into the flat per-bot Config fields
// that ForBot and ApplyChatBot populate: a third projection site would silently
// drop it. Reading it back by bot name must therefore keep working after both.
func TestBotDeliveryPolicy_SurvivesBotProjection(t *testing.T) {
	raw := "bots:\n  main:\n    host: a\n    id: 00000000-0000-0000-0000-000000000001\n    secret: s\n    max_message_length: 1234\n"
	var cfg Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := cfg.ApplyChatBot("main"); err != nil {
		t.Fatalf("ApplyChatBot: %v", err)
	}
	limit, _ := cfg.BotDeliveryPolicy(cfg.BotName)
	if limit != 1234 {
		t.Fatalf("limit after ApplyChatBot = %d, want 1234", limit)
	}
}

func TestValidate_BotDeliverySchema(t *testing.T) {
	raw := []byte("bots:\n  main:\n    host: a\n    max_message_length: 4096\n    truncate_suffix: \"...\"\n    mesage_length: 1\n")
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var unknown []string
	for _, r := range cfg.Validate(raw) {
		if r.Level == ValidationWarning {
			unknown = append(unknown, r.Path)
		}
	}
	found := false
	for _, u := range unknown {
		if u == "bots.main.mesage_length" {
			found = true
		}
		if u == "bots.main.max_message_length" || u == "bots.main.truncate_suffix" {
			t.Errorf("known key reported as unknown: %s", u)
		}
	}
	if !found {
		t.Fatalf("typo inside the bot was not reported; warnings = %v", unknown)
	}
}

// BotByID walks a map, so two aliases of one bot_id must not disagree about the
// cap: the worker would otherwise apply whichever alias it happened to hit.
func TestValidateBotIDs_RejectsDisagreeingDeliveryPolicy(t *testing.T) {
	limit10, limit100 := 10, 100
	sfxA, sfxB := "…", "~"
	defaultLimit := DefaultMaxMessageLength

	tests := []struct {
		name    string
		a, b    BotConfig
		wantErr bool
	}{
		{
			name:    "different limits disagree",
			a:       BotConfig{Host: "h", ID: "id", MaxMessageLength: &limit10},
			b:       BotConfig{Host: "h", ID: "id", MaxMessageLength: &limit100},
			wantErr: true,
		},
		{
			name:    "different suffixes disagree",
			a:       BotConfig{Host: "h", ID: "id", TruncateSuffix: &sfxA},
			b:       BotConfig{Host: "h", ID: "id", TruncateSuffix: &sfxB},
			wantErr: true,
		},
		{
			name:    "an absent key equals the explicit default",
			a:       BotConfig{Host: "h", ID: "id"},
			b:       BotConfig{Host: "h", ID: "id", MaxMessageLength: &defaultLimit},
			wantErr: false,
		},
		{
			name:    "identical policies agree",
			a:       BotConfig{Host: "h", ID: "id", MaxMessageLength: &limit10},
			b:       BotConfig{Host: "h", ID: "id", MaxMessageLength: &limit10},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Bots: map[string]BotConfig{"alpha": tt.a, "beta": tt.b}}
			err := cfg.ValidateBotIDs()
			if tt.wantErr != (err != nil) {
				t.Fatalf("error = %v, want error = %v", err, tt.wantErr)
			}
		})
	}
}

// A negative cap silently disabling the protection is a footgun: the value is
// almost certainly a typo, and the message it lets through is the one BotX
// rejects whole.
func TestValidate_RejectsNegativeMessageLength(t *testing.T) {
	raw := []byte("bots:\n  main:\n    host: a\n    max_message_length: -5\n")
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, r := range cfg.Validate(raw) {
		if r.Level == ValidationError && r.Path == "bots.main.max_message_length" {
			found = true
		}
	}
	if !found {
		t.Fatal("a negative max_message_length was not reported as an error")
	}
}
