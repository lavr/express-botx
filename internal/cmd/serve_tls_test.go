package cmd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/config"
	"github.com/lavr/express-botx/internal/server"
)

func TestResolveTLS(t *testing.T) {
	tests := []struct {
		name                         string
		yaml                         *config.TLSYAMLConfig
		envCert, envKey, envInterval string
		flagCert, flagKey            string
		wantCert, wantKey            string
		wantInterval                 time.Duration
		wantNil, wantErr             bool
	}{
		{name: "absent", wantNil: true},
		{name: "empty", yaml: &config.TLSYAMLConfig{}, wantNil: true},
		{name: "interval only", yaml: &config.TLSYAMLConfig{ReloadInterval: "bad"}, wantNil: true},
		{name: "yaml default", yaml: &config.TLSYAMLConfig{CertFile: "y.crt", KeyFile: "y.key"}, wantCert: "y.crt", wantKey: "y.key", wantInterval: time.Minute},
		{name: "env overrides", yaml: &config.TLSYAMLConfig{CertFile: "y.crt", KeyFile: "y.key", ReloadInterval: "10s"}, envCert: "e.crt", envKey: "e.key", envInterval: "20s", wantCert: "e.crt", wantKey: "e.key", wantInterval: 20 * time.Second},
		{name: "flags override", envCert: "e.crt", envKey: "e.key", flagCert: "f.crt", flagKey: "f.key", wantCert: "f.crt", wantKey: "f.key", wantInterval: time.Minute},
		{name: "split layers", yaml: &config.TLSYAMLConfig{CertFile: "y.crt"}, envKey: "e.key", wantCert: "y.crt", wantKey: "e.key", wantInterval: time.Minute},
		{name: "cert only", yaml: &config.TLSYAMLConfig{CertFile: "y.crt"}, wantErr: true},
		{name: "key only", envKey: "e.key", wantErr: true},
		{name: "bad interval", envCert: "e.crt", envKey: "e.key", envInterval: "soon", wantErr: true},
		{name: "zero", envCert: "e.crt", envKey: "e.key", envInterval: "0s", wantErr: true},
		{name: "negative", envCert: "e.crt", envKey: "e.key", envInterval: "-1s", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTLS(tt.yaml, tt.envCert, tt.envKey, tt.envInterval, tt.flagCert, tt.flagKey)
			if tt.wantErr {
				if err == nil {
					t.Fatal("wanted error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("got %#v", got)
				}
				return
			}
			if got == nil || got.CertFile != tt.wantCert || got.KeyFile != tt.wantKey || got.ReloadInterval != tt.wantInterval {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestServeHelpIncludesTLSFlags(t *testing.T) {
	deps, _, stderr := testDeps()
	if err := runServe([]string{"--help"}, deps); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"--tls-cert", "--tls-key"} {
		if !strings.Contains(stderr.String(), name) {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestWarnDisabledTLS(t *testing.T) {
	tests := []struct {
		name     string
		yaml     *config.TLSYAMLConfig
		resolved bool
		wantWarn bool
	}{
		{name: "absent section"},
		{name: "empty section", yaml: &config.TLSYAMLConfig{}, wantWarn: true},
		{name: "interval only", yaml: &config.TLSYAMLConfig{ReloadInterval: "60s"}, wantWarn: true},
		{name: "completed by env or flags", yaml: &config.TLSYAMLConfig{}, resolved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tlsCfg *server.TLSConfig
			if tt.resolved {
				tlsCfg = &server.TLSConfig{CertFile: "cert", KeyFile: "key", ReloadInterval: time.Minute}
			}
			var messages []string
			warnDisabledTLS(tt.yaml, tlsCfg, func(format string, args ...any) {
				messages = append(messages, fmt.Sprintf(format, args...))
			})
			if got := len(messages) == 1; got != tt.wantWarn {
				t.Fatalf("warning emitted = %v, want %v; messages=%v", got, tt.wantWarn, messages)
			}
			if tt.wantWarn && !strings.Contains(messages[0], "serving plaintext HTTP") {
				t.Fatalf("warning does not identify plaintext fallback: %q", messages[0])
			}
		})
	}
}
