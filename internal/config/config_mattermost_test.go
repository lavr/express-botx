package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMattermostErrorColorsNilVsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantNil bool
	}{
		{name: "absent key stays nil", yaml: "default_chat_id: a\n", wantNil: true},
		{name: "explicit empty list is not nil", yaml: "error_colors: []\n", wantNil: false},
		{name: "populated list is not nil", yaml: "error_colors: [\"#d9534f\"]\n", wantNil: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c MattermostYAMLConfig
			if err := yaml.Unmarshal([]byte(tt.yaml), &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if (c.ErrorColors == nil) != tt.wantNil {
				t.Errorf("ErrorColors nil = %v, want %v (value %#v)", c.ErrorColors == nil, tt.wantNil, c.ErrorColors)
			}
		})
	}
}

func TestMattermostSectionIsAccepted(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantUnknown []string
	}{
		{
			name: "every documented key is known",
			yaml: `
server:
  mattermost:
    default_chat_id: alerts
    error_severities: [critical, high]
    error_colors: ["#d9534f"]
    icons:
      resolved: "x"
`,
		},
		{
			name: "a typo inside the section is reported",
			yaml: `
server:
  mattermost:
    default_chat_ids: alerts
`,
			wantUnknown: []string{"default_chat_ids"},
		},
		{
			name: "a typo in the section name is reported",
			yaml: `
server:
  matermost:
    default_chat_id: alerts
`,
			wantUnknown: []string{"matermost"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := detectUnknownKeys([]byte(tt.yaml))
			var reported []string
			for _, r := range results {
				reported = append(reported, r.Message)
			}
			if len(tt.wantUnknown) == 0 {
				if len(reported) != 0 {
					t.Fatalf("detectUnknownKeys() reported %v, want nothing", reported)
				}
				return
			}
			for _, want := range tt.wantUnknown {
				found := false
				for _, msg := range reported {
					if strings.Contains(msg, want) {
						found = true
					}
				}
				if !found {
					t.Errorf("detectUnknownKeys() = %v, want a report naming %q", reported, want)
				}
			}
		})
	}
}
