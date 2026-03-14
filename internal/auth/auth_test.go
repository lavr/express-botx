package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildSignature(t *testing.T) {
	tests := []struct {
		name      string
		botID     string
		secretKey string
		want      string
	}{
		{
			name:      "known vector",
			botID:     "test-bot-id",
			secretKey: "secret-key",
			// echo -n "test-bot-id" | openssl dgst -sha256 -hmac "secret-key" | awk '{print toupper($NF)}'
			want: "8DDBAC52A2A44CAC7FEE62CE5C50B9C1EEF43C8FD51A5757272E29B3ED7AA2B0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSignature(tt.botID, tt.secretKey)
			if got != tt.want {
				t.Errorf("BuildSignature() = %q, want %q", got, tt.want)
			}
			// Must be uppercase hex
			if got != strings.ToUpper(got) {
				t.Errorf("BuildSignature() result not uppercase: %q", got)
			}
			// SHA256 produces 32 bytes = 64 hex chars
			if len(got) != 64 {
				t.Errorf("BuildSignature() length = %d, want 64", len(got))
			}
		})
	}
}

func TestBuildSignature_Deterministic(t *testing.T) {
	sig1 := BuildSignature("bot-id", "secret")
	sig2 := BuildSignature("bot-id", "secret")
	if sig1 != sig2 {
		t.Errorf("BuildSignature not deterministic: %q != %q", sig1, sig2)
	}
}

func TestBuildSignature_DifferentInputs(t *testing.T) {
	sig1 := BuildSignature("bot-1", "secret")
	sig2 := BuildSignature("bot-2", "secret")
	if sig1 == sig2 {
		t.Error("different bot IDs produced same signature")
	}

	sig3 := BuildSignature("bot-1", "secret-a")
	sig4 := BuildSignature("bot-1", "secret-b")
	if sig3 == sig4 {
		t.Error("different secrets produced same signature")
	}
}

func TestGetToken(t *testing.T) {
	botID := "054af49e-5e18-4dca-ad73-4f96b6de63fa"
	expectedSig := "test-signature"
	expectedToken := "abc123token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/api/v2/botx/bots/" + botID + "/token"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if sig := r.URL.Query().Get("signature"); sig != expectedSig {
			t.Errorf("signature = %q, want %q", sig, expectedSig)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{
			Status: "ok",
			Result: expectedToken,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	token, err := GetToken(ctx, srv.URL, botID, expectedSig, 10*time.Second)
	if err != nil {
		t.Fatalf("GetToken() error: %v", err)
	}
	if token != expectedToken {
		t.Errorf("GetToken() = %q, want %q", token, expectedToken)
	}
}

func TestGetToken_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := GetToken(ctx, srv.URL, "bot-id", "sig", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestGetToken_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	ctx := context.Background()
	_, err := GetToken(ctx, srv.URL, "bot-id", "sig", 10*time.Second)
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}
