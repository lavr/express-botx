package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidate_IncidentRelay(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantPath string
	}{
		{
			name: "unknown message source is an error",
			cfg: Config{
				Server: ServerConfig{IncidentRelay: &IncidentRelayYAMLConfig{MessageSource: "webhok"}},
			},
			wantPath: "server.incidentrelay.message_source",
		},
		{
			name: "default_chat_id must name a configured alias",
			cfg: Config{
				Chats:  map[string]ChatConfig{"infra": {}},
				Server: ServerConfig{IncidentRelay: &IncidentRelayYAMLConfig{DefaultChatID: "nope"}},
			},
			wantPath: "server.incidentrelay.default_chat_id",
		},
		{
			name: "known alias and source validate clean",
			cfg: Config{
				Chats: map[string]ChatConfig{"infra": {}},
				Server: ServerConfig{IncidentRelay: &IncidentRelayYAMLConfig{
					DefaultChatID: "infra",
					MessageSource: IncidentRelayMessageSourceTemplate,
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var found bool
			for _, res := range tt.cfg.Validate(nil) {
				if res.Level != ValidationError {
					continue
				}
				if strings.HasPrefix(res.Path, "server.incidentrelay") {
					found = true
					if res.Path != tt.wantPath {
						t.Errorf("path = %q, want %q", res.Path, tt.wantPath)
					}
				}
			}
			if found != (tt.wantPath != "") {
				t.Fatalf("incidentrelay error found = %v, want %v", found, tt.wantPath != "")
			}
		})
	}
}

func TestValidate_IncidentRelayRawYAML(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantUnknown string
	}{
		{
			name: "new keys are part of the schema",
			raw: `
server:
  api_keys:
    - name: ir
      key: v
      allow_query_auth: true
  incidentrelay:
    default_chat_id: alerts
    message_source: webhook
    error_severities: [critical]
    template: "{{ .Title }}"
`,
		},
		{
			name: "a typo inside the section is reported",
			raw: `
server:
  incidentrelay:
    default_chat_id: alerts
    mesage_source: webhook
`,
			wantUnknown: "server.incidentrelay.mesage_source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := yaml.Unmarshal([]byte(tt.raw), &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			var unknown []string
			for _, r := range cfg.Validate([]byte(tt.raw)) {
				if r.Level == ValidationWarning && strings.Contains(r.Message, "unknown key") {
					unknown = append(unknown, r.Path)
				}
			}
			if tt.wantUnknown == "" {
				if len(unknown) != 0 {
					t.Fatalf("expected no unknown-key warnings, got %v", unknown)
				}
				return
			}
			for _, u := range unknown {
				if u == tt.wantUnknown {
					return
				}
			}
			t.Fatalf("expected %q among unknown keys, got %v", tt.wantUnknown, unknown)
		})
	}
}
