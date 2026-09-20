package cmd

import (
	"testing"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

func TestBuildMattermostConfig(t *testing.T) {
	tests := []struct {
		name       string
		yaml       config.MattermostYAMLConfig
		wantChat   string
		wantColors []string
	}{
		{
			name:       "empty config takes the built-in error colours",
			yaml:       config.MattermostYAMLConfig{},
			wantColors: server.DefaultMattermostErrorColors,
		},
		{
			name:       "default_chat_id propagates",
			yaml:       config.MattermostYAMLConfig{DefaultChatID: "infra"},
			wantChat:   "infra",
			wantColors: server.DefaultMattermostErrorColors,
		},
		{
			name:       "configured error colours replace the defaults",
			yaml:       config.MattermostYAMLConfig{ErrorColors: []string{"#ff0000", "#d9534f"}},
			wantColors: []string{"#ff0000", "#d9534f"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildMattermostConfig(&tt.yaml)
			if got.DefaultChatID != tt.wantChat {
				t.Errorf("DefaultChatID = %q, want %q", got.DefaultChatID, tt.wantChat)
			}
			if len(got.ErrorColors) != len(tt.wantColors) {
				t.Fatalf("ErrorColors = %v, want %v", got.ErrorColors, tt.wantColors)
			}
			for i, c := range tt.wantColors {
				if got.ErrorColors[i] != c {
					t.Errorf("ErrorColors[%d] = %q, want %q", i, got.ErrorColors[i], c)
				}
			}
		})
	}
}
