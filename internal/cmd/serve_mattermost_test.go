package cmd

import (
	"maps"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

// TestBuildMattermostConfig goes from YAML so the nil-versus-empty distinction
// is exercised the way an operator writes it: an absent key takes the default,
// an explicit empty value switches the rule off.
func TestBuildMattermostConfig(t *testing.T) {
	tests := []struct {
		name           string
		yaml           string
		wantChat       string
		wantSeverities []string
		wantWarnings   []string
		wantIcons      map[string]string
	}{
		{
			name:           "an empty section takes every built-in default",
			yaml:           "{}",
			wantSeverities: server.DefaultMattermostErrorSeverities,
			wantWarnings:   server.DefaultMattermostWarningSeverities,
			wantIcons:      server.DefaultMattermostIcons,
		},
		{
			name: "configured values replace the defaults and are normalized",
			yaml: `
default_chat_id: infra
error_severities: [critical, crit, error]
warning_severities: [medium]
icons:
  "  ERROR ": "\U0001F525"
`,
			wantChat:       "infra",
			wantSeverities: []string{"critical", "crit", "error"},
			wantWarnings:   []string{"medium"},
			wantIcons:      map[string]string{server.MattermostStateError: "\U0001F525"},
		},
		{
			name:           "explicit empty values switch the rules off",
			yaml:           "error_severities: []\nwarning_severities: []\nicons: {}\n",
			wantSeverities: []string{},
			wantWarnings:   []string{},
			wantIcons:      map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mm config.MattermostYAMLConfig
			if err := yaml.Unmarshal([]byte(tt.yaml), &mm); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := buildMattermostConfig(&mm)
			if got.DefaultChatID != tt.wantChat {
				t.Errorf("DefaultChatID = %q, want %q", got.DefaultChatID, tt.wantChat)
			}
			if !slices.Equal(got.ErrorSeverities, tt.wantSeverities) {
				t.Errorf("ErrorSeverities = %v, want %v", got.ErrorSeverities, tt.wantSeverities)
			}
			if !slices.Equal(got.WarningSeverities, tt.wantWarnings) {
				t.Errorf("WarningSeverities = %v, want %v", got.WarningSeverities, tt.wantWarnings)
			}
			if !maps.Equal(got.Icons, tt.wantIcons) {
				t.Errorf("Icons = %v, want %v", got.Icons, tt.wantIcons)
			}
		})
	}
}
