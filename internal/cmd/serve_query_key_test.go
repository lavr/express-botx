package cmd

import (
	"testing"

	"github.com/lavr/express-botx/internal/config"
)

func TestResolveAPIKeys_QueryAuthOptIn(t *testing.T) {
	tests := []struct {
		name string
		keys []config.APIKeyConfig
		want bool
	}{
		{
			name: "opted in",
			keys: []config.APIKeyConfig{{Name: "ir", Key: "v", AllowQueryAuth: true}},
			want: true,
		},
		{
			name: "absent means off",
			keys: []config.APIKeyConfig{{Name: "ir", Key: "v"}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolveAPIKeys(tt.keys, nil)
			if err != nil {
				t.Fatalf("resolveAPIKeys: %v", err)
			}
			if got := resolved[0].AllowQueryAuth; got != tt.want {
				t.Fatalf("AllowQueryAuth = %v, want %v", got, tt.want)
			}
		})
	}
}
