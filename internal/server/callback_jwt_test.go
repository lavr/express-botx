package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// makeJWT creates a JWT token string for testing.
func makeJWT(header, claims map[string]any, secret string) string {
	hdr, _ := json.Marshal(header)
	clm, _ := json.Marshal(claims)

	h := base64.RawURLEncoding.EncodeToString(hdr)
	c := base64.RawURLEncoding.EncodeToString(clm)

	signingInput := h + "." + c
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return h + "." + c + "." + sig
}

func TestVerifyCallbackJWT(t *testing.T) {
	const botID = "bot-123"
	const secret = "my-secret-key"

	lookup := func(id string) (string, error) {
		if id == botID {
			return secret, nil
		}
		return "", fmt.Errorf("unknown bot: %s", id)
	}

	t.Run("valid token", func(t *testing.T) {
		now := time.Now().Unix()
		token := makeJWT(
			map[string]any{"alg": "HS256", "typ": "JWT"},
			map[string]any{"aud": botID, "exp": now + 3600, "nbf": now - 60},
			secret,
		)

		claims, err := verifyCallbackJWT(token, lookup)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if claims.Aud != botID {
			t.Fatalf("expected aud=%s, got %s", botID, claims.Aud)
		}
	})

	t.Run("valid token without exp/nbf", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID},
			secret,
		)

		claims, err := verifyCallbackJWT(token, lookup)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if claims.Aud != botID {
			t.Fatalf("expected aud=%s, got %s", botID, claims.Aud)
		}
	})

	t.Run("malformed token", func(t *testing.T) {
		_, err := verifyCallbackJWT("not-a-jwt", lookup)
		if !errors.Is(err, errJWTMalformed) {
			t.Fatalf("expected errJWTMalformed, got: %v", err)
		}
	})

	t.Run("too many parts", func(t *testing.T) {
		_, err := verifyCallbackJWT("a.b.c.d", lookup)
		if !errors.Is(err, errJWTMalformed) {
			t.Fatalf("expected errJWTMalformed, got: %v", err)
		}
	})

	t.Run("algorithm none rejected", func(t *testing.T) {
		hdr, _ := json.Marshal(map[string]any{"alg": "none"})
		clm, _ := json.Marshal(map[string]any{"aud": botID})
		token := base64.RawURLEncoding.EncodeToString(hdr) + "." +
			base64.RawURLEncoding.EncodeToString(clm) + "."

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTAlgNone) {
			t.Fatalf("expected errJWTAlgNone, got: %v", err)
		}
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		hdr, _ := json.Marshal(map[string]any{"alg": "RS256"})
		clm, _ := json.Marshal(map[string]any{"aud": botID})
		token := base64.RawURLEncoding.EncodeToString(hdr) + "." +
			base64.RawURLEncoding.EncodeToString(clm) + ".fakesig"

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTAlgorithm) {
			t.Fatalf("expected errJWTAlgorithm, got: %v", err)
		}
	})

	t.Run("wrong signature", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID},
			"wrong-secret",
		)

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTSignature) {
			t.Fatalf("expected errJWTSignature, got: %v", err)
		}
	})

	t.Run("missing aud claim", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"sub": "test"},
			secret,
		)

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTMissingAud) {
			t.Fatalf("expected errJWTMissingAud, got: %v", err)
		}
	})

	t.Run("unknown bot ID", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": "unknown-bot"},
			secret,
		)

		_, err := verifyCallbackJWT(token, lookup)
		if err == nil {
			t.Fatal("expected error for unknown bot")
		}
		if !strings.Contains(err.Error(), "unknown bot") {
			t.Fatalf("expected 'unknown bot' in error, got: %v", err)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "exp": time.Now().Unix() - 3600},
			secret,
		)

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTExpired) {
			t.Fatalf("expected errJWTExpired, got: %v", err)
		}
	})

	t.Run("not yet valid", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "nbf": time.Now().Unix() + 3600},
			secret,
		)

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTNotYetValid) {
			t.Fatalf("expected errJWTNotYetValid, got: %v", err)
		}
	})

	t.Run("invalid header encoding", func(t *testing.T) {
		_, err := verifyCallbackJWT("!!!.YQ.YQ", lookup)
		if err == nil || !strings.Contains(err.Error(), "header encoding") {
			t.Fatalf("expected header encoding error, got: %v", err)
		}
	})

	t.Run("invalid claims encoding", func(t *testing.T) {
		hdr, _ := json.Marshal(map[string]any{"alg": "HS256"})
		token := base64.RawURLEncoding.EncodeToString(hdr) + ".!!!.YQ"

		_, err := verifyCallbackJWT(token, lookup)
		if err == nil || !strings.Contains(err.Error(), "claims encoding") {
			t.Fatalf("expected claims encoding error, got: %v", err)
		}
	})
}

func TestJWTClaims(t *testing.T) {
	const botID = "bot-123"
	const secret = "my-secret-key"

	lookup := func(id string) (string, error) {
		if id == botID {
			return secret, nil
		}
		return "", fmt.Errorf("unknown bot: %s", id)
	}

	t.Run("exp expired rejects token", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "exp": time.Now().Unix() - 60},
			secret,
		)
		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTExpired) {
			t.Fatalf("expected errJWTExpired, got: %v", err)
		}
	})

	t.Run("exp in future accepts token", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "exp": time.Now().Unix() + 3600},
			secret,
		)
		claims, err := verifyCallbackJWT(token, lookup)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if claims.Aud != botID {
			t.Fatalf("expected aud=%s, got %s", botID, claims.Aud)
		}
	})

	t.Run("nbf in future rejects token", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "nbf": time.Now().Unix() + 3600},
			secret,
		)
		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTNotYetValid) {
			t.Fatalf("expected errJWTNotYetValid, got: %v", err)
		}
	})

	t.Run("nbf in past accepts token", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "nbf": time.Now().Unix() - 60},
			secret,
		)
		claims, err := verifyCallbackJWT(token, lookup)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if claims.Aud != botID {
			t.Fatalf("expected aud=%s, got %s", botID, claims.Aud)
		}
	})

	t.Run("aud must match known bot_id", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": "unknown-bot-456"},
			secret,
		)
		_, err := verifyCallbackJWT(token, lookup)
		if err == nil {
			t.Fatal("expected error for unknown bot_id in aud")
		}
		if !strings.Contains(err.Error(), "unknown bot") {
			t.Fatalf("expected 'unknown bot' error, got: %v", err)
		}
	})

	t.Run("aud missing rejects token", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"sub": "test"},
			secret,
		)
		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTMissingAud) {
			t.Fatalf("expected errJWTMissingAud, got: %v", err)
		}
	})

	t.Run("alg none rejected", func(t *testing.T) {
		hdr, _ := json.Marshal(map[string]any{"alg": "none"})
		clm, _ := json.Marshal(map[string]any{"aud": botID})
		token := base64.RawURLEncoding.EncodeToString(hdr) + "." +
			base64.RawURLEncoding.EncodeToString(clm) + "."

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTAlgNone) {
			t.Fatalf("expected errJWTAlgNone, got: %v", err)
		}
	})

	t.Run("alg None case insensitive rejected", func(t *testing.T) {
		hdr, _ := json.Marshal(map[string]any{"alg": "None"})
		clm, _ := json.Marshal(map[string]any{"aud": botID})
		token := base64.RawURLEncoding.EncodeToString(hdr) + "." +
			base64.RawURLEncoding.EncodeToString(clm) + "."

		_, err := verifyCallbackJWT(token, lookup)
		if !errors.Is(err, errJWTAlgNone) {
			t.Fatalf("expected errJWTAlgNone for 'None', got: %v", err)
		}
	})

	t.Run("exp and nbf both valid", func(t *testing.T) {
		now := time.Now().Unix()
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "exp": now + 300, "nbf": now - 60},
			secret,
		)
		claims, err := verifyCallbackJWT(token, lookup)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if claims.Aud != botID {
			t.Fatalf("expected aud=%s, got %s", botID, claims.Aud)
		}
	})

	t.Run("exp boundary exactly now rejects", func(t *testing.T) {
		// exp = now means now > exp is false (now == exp), so token should still be valid
		now := time.Now().Unix()
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "exp": now},
			secret,
		)
		// exp==now: now > exp is false, so token is valid (not expired)
		_, err := verifyCallbackJWT(token, lookup)
		if err != nil {
			t.Fatalf("expected token with exp=now to be valid, got: %v", err)
		}
	})
}

func TestJWTMiddleware(t *testing.T) {
	const botID = "bot-123"
	const secret = "my-secret-key"

	lookup := func(id string) (string, error) {
		if id == botID {
			return secret, nil
		}
		return "", fmt.Errorf("unknown bot: %s", id)
	}

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	validToken := func() string {
		now := time.Now().Unix()
		return makeJWT(
			map[string]any{"alg": "HS256", "typ": "JWT"},
			map[string]any{"aud": botID, "exp": now + 3600},
			secret,
		)
	}

	t.Run("verify disabled passes through", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, false)
		req := httptest.NewRequest("POST", "/command", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("verify disabled passes without auth header", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, false)
		req := httptest.NewRequest("POST", "/command", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("missing authorization header returns 401", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		assertJSONError(t, rec, "missing Authorization header")
	})

	t.Run("non-bearer scheme returns 401", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		req.Header.Set("Authorization", "Basic abc123")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		assertJSONError(t, rec, "Bearer scheme")
	})

	t.Run("empty bearer token returns 401", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		req.Header.Set("Authorization", "Bearer ")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		assertJSONError(t, rec, "empty Bearer token")
	})

	t.Run("invalid JWT returns 401", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		req.Header.Set("Authorization", "Bearer not-a-jwt")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		assertJSONError(t, rec, "JWT verification failed")
	})

	t.Run("expired JWT returns 401", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID, "exp": time.Now().Unix() - 3600},
			secret,
		)
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		assertJSONError(t, rec, "JWT verification failed")
	})

	t.Run("valid JWT passes through", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		req.Header.Set("Authorization", "Bearer "+validToken())
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("wrong signature returns 401", func(t *testing.T) {
		token := makeJWT(
			map[string]any{"alg": "HS256"},
			map[string]any{"aud": botID},
			"wrong-secret",
		)
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
		assertJSONError(t, rec, "JWT verification failed")
	})

	t.Run("response has JSON content type", func(t *testing.T) {
		mw := callbackJWTMiddleware(okHandler, lookup, true)
		req := httptest.NewRequest("POST", "/command", nil)
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Fatalf("expected application/json, got %s", ct)
		}
	})
}

func assertJSONError(t *testing.T, rec *httptest.ResponseRecorder, substr string) {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, substr) {
		t.Fatalf("expected error containing %q, got %q", substr, errMsg)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatal("expected ok=false in error response")
	}
}


