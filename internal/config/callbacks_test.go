package config

import (
	"strings"
	"testing"
)

func TestCallbacksConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CallbacksConfig
		wantErr string
	}{
		{
			name: "valid exec handler",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"chat_created", "added_to_chat"},
						Handler: CallbackHandlerConfig{Type: "exec", Command: "./handler.sh"},
					},
				},
			},
		},
		{
			name: "valid webhook handler with timeout",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"cts_login"},
						Async:   true,
						Handler: CallbackHandlerConfig{Type: "webhook", URL: "http://example.com/hook", Timeout: "30s"},
					},
				},
			},
		},
		{
			name: "valid wildcard rule",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"*"},
						Handler: CallbackHandlerConfig{Type: "exec", Command: "./fallback.sh"},
					},
				},
			},
		},
		{
			name: "empty events",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{},
						Handler: CallbackHandlerConfig{Type: "exec", Command: "./handler.sh"},
					},
				},
			},
			wantErr: "events must not be empty",
		},
		{
			name: "unknown handler type accepted with warning",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"message"},
						Handler: CallbackHandlerConfig{Type: "grpc"},
					},
				},
			},
			wantErr: "", // unknown types are now warnings, not errors (for custom handler extensibility)
		},
		{
			name: "exec without command",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"message"},
						Handler: CallbackHandlerConfig{Type: "exec"},
					},
				},
			},
			wantErr: "exec handler requires command",
		},
		{
			name: "webhook without url",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"message"},
						Handler: CallbackHandlerConfig{Type: "webhook"},
					},
				},
			},
			wantErr: "webhook handler requires url",
		},
		{
			name: "invalid timeout",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"message"},
						Handler: CallbackHandlerConfig{Type: "exec", Command: "./h.sh", Timeout: "bad"},
					},
				},
			},
			wantErr: "invalid timeout",
		},
		{
			name: "no rules is valid",
			cfg:  CallbacksConfig{},
		},
		{
			name: "multiple valid rules",
			cfg: CallbacksConfig{
				Rules: []CallbackRule{
					{
						Events:  []string{"chat_created"},
						Handler: CallbackHandlerConfig{Type: "exec", Command: "./a.sh", Timeout: "10s"},
					},
					{
						Events:  []string{"notification_callback"},
						Handler: CallbackHandlerConfig{Type: "webhook", URL: "http://localhost:8080/cb"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCallbacksConfig_ValidateUnknownEventWarns(t *testing.T) {
	// Unknown events should produce a warning but not an error.
	cfg := CallbacksConfig{
		Rules: []CallbackRule{
			{
				Events:  []string{"custom_event"},
				Handler: CallbackHandlerConfig{Type: "exec", Command: "./h.sh"},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() should not error on unknown events, got: %v", err)
	}
}
