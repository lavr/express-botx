package config

import "testing"
import "gopkg.in/yaml.v3"

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
