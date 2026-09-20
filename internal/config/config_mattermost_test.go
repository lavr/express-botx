package config

import (
	"strings"
	"testing"
)

// TestMattermostConfigKeysAreRegistered guards the schema: without the section
// in knownKeys a valid config is rejected outright, and without the nested icon
// states a typo silently removes every icon.
func TestMattermostConfigKeysAreRegistered(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantUnknown string
	}{
		{
			name: "every documented key is known",
			yaml: `
server:
  mattermost:
    default_chat_id: alerts
    error_severities: [critical]
    warning_severities: [warning]
    icons:
      resolved: "x"
      acknowledged: "x"
      error: "x"
      warning: "x"
      default: "x"
`,
		},
		{
			name:        "a typo in an icon state is reported",
			yaml:        "server:\n  mattermost:\n    icons:\n      resovled: \"x\"\n",
			wantUnknown: "resovled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := detectUnknownKeys([]byte(tt.yaml))
			if tt.wantUnknown == "" {
				if len(results) != 0 {
					t.Fatalf("unknown keys reported: %v, want none", results)
				}
				return
			}
			found := false
			for _, r := range results {
				if strings.Contains(r.Message, tt.wantUnknown) {
					found = true
				}
			}
			if !found {
				t.Errorf("reported %v, want a report naming %q", results, tt.wantUnknown)
			}
		})
	}
}
