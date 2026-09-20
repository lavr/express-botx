package cmd

import (
	"testing"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

func TestBuildMattermostConfig(t *testing.T) {
	tests := []struct {
		name           string
		yaml           config.MattermostYAMLConfig
		wantChat       string
		wantSeverities []string
		wantWarnings   []string
		wantIcons      map[string]string
	}{
		{
			name:           "empty config takes every built-in default",
			yaml:           config.MattermostYAMLConfig{},
			wantSeverities: server.DefaultMattermostErrorSeverities,
			wantWarnings:   server.DefaultMattermostWarningSeverities,
			wantIcons:      server.DefaultMattermostIcons,
		},
		{
			name:           "default_chat_id propagates",
			yaml:           config.MattermostYAMLConfig{DefaultChatID: "infra"},
			wantChat:       "infra",
			wantSeverities: server.DefaultMattermostErrorSeverities,
			wantWarnings:   server.DefaultMattermostWarningSeverities,
			wantIcons:      server.DefaultMattermostIcons,
		},
		{
			name:           "configured severities replace the defaults",
			yaml:           config.MattermostYAMLConfig{ErrorSeverities: []string{"critical", "crit", "error"}},
			wantSeverities: []string{"critical", "crit", "error"},
			wantWarnings:   server.DefaultMattermostWarningSeverities,
			wantIcons:      server.DefaultMattermostIcons,
		},
		{
			name:           "configured icons replace the defaults and are normalized",
			yaml:           config.MattermostYAMLConfig{Icons: map[string]string{"  ERROR ": "\U0001F525"}},
			wantSeverities: server.DefaultMattermostErrorSeverities,
			wantWarnings:   server.DefaultMattermostWarningSeverities,
			wantIcons:      map[string]string{server.MattermostStateError: "\U0001F525"},
		},
		{
			name:           "an explicitly empty icon map disables icons",
			yaml:           config.MattermostYAMLConfig{Icons: map[string]string{}},
			wantSeverities: server.DefaultMattermostErrorSeverities,
			wantWarnings:   server.DefaultMattermostWarningSeverities,
			wantIcons:      map[string]string{},
		},
		{
			name:           "an explicitly empty severity list disables the error rule",
			yaml:           config.MattermostYAMLConfig{ErrorSeverities: []string{}},
			wantSeverities: []string{},
			wantWarnings:   server.DefaultMattermostWarningSeverities,
			wantIcons:      server.DefaultMattermostIcons,
		},
		{
			name:           "configured warning severities replace the defaults",
			yaml:           config.MattermostYAMLConfig{WarningSeverities: []string{"medium"}},
			wantSeverities: server.DefaultMattermostErrorSeverities,
			wantWarnings:   []string{"medium"},
			wantIcons:      server.DefaultMattermostIcons,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildMattermostConfig(&tt.yaml)
			if got.DefaultChatID != tt.wantChat {
				t.Errorf("DefaultChatID = %q, want %q", got.DefaultChatID, tt.wantChat)
			}
			assertStrings(t, "ErrorSeverities", got.ErrorSeverities, tt.wantSeverities)
			assertStrings(t, "WarningSeverities", got.WarningSeverities, tt.wantWarnings)
			if len(got.Icons) != len(tt.wantIcons) {
				t.Fatalf("Icons = %v, want %v", got.Icons, tt.wantIcons)
			}
			for state, icon := range tt.wantIcons {
				if got.Icons[state] != icon {
					t.Errorf("Icons[%q] = %q, want %q", state, got.Icons[state], icon)
				}
			}
		})
	}
}

func assertStrings(t *testing.T, field string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q, want %q", field, i, got[i], want[i])
		}
	}
}
