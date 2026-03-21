package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

var (
	errJWTMalformed       = errors.New("malformed JWT: expected 3 parts")
	errJWTAlgorithm       = errors.New("unsupported JWT algorithm: only HS256 is allowed")
	errJWTSignature       = errors.New("invalid JWT signature")
	errJWTExpired         = errors.New("JWT has expired")
	errJWTNotYetValid     = errors.New("JWT is not yet valid")
	errJWTMissingAud      = errors.New("JWT missing aud claim")
	errJWTAlgNone         = errors.New("unsigned JWT (alg: none) is not allowed")
)

// jwtHeader represents the JOSE header of a JWT.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
}

// jwtClaims represents the claims in a BotX callback JWT.
type jwtClaims struct {
	Aud string `json:"aud"`
	Exp *int64 `json:"exp,omitempty"`
	Nbf *int64 `json:"nbf,omitempty"`
	Iat *int64 `json:"iat,omitempty"`
}

// verifyCallbackJWT verifies a JWT token string from a BotX callback.
// It checks the algorithm is HS256, looks up the bot secret via the aud claim,
// and verifies the HMAC-SHA256 signature.
func verifyCallbackJWT(tokenString string, secretLookup func(botID string) (string, error)) (*jwtClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, errJWTMalformed
	}

	// Decode and validate header.
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT header encoding: %w", err)
	}

	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("invalid JWT header JSON: %w", err)
	}

	if strings.EqualFold(header.Alg, "none") {
		return nil, errJWTAlgNone
	}
	if header.Alg != "HS256" {
		return nil, errJWTAlgorithm
	}

	// Decode claims.
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT claims encoding: %w", err)
	}

	var claims jwtClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, fmt.Errorf("invalid JWT claims JSON: %w", err)
	}

	if claims.Aud == "" {
		return nil, errJWTMissingAud
	}

	// Look up the secret for this bot.
	secret, err := secretLookup(claims.Aud)
	if err != nil {
		return nil, fmt.Errorf("bot secret lookup failed: %w", err)
	}

	// Verify HMAC-SHA256 signature.
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT signature encoding: %w", err)
	}

	if !hmac.Equal(expectedSig, actualSig) {
		return nil, errJWTSignature
	}

	// Validate time-based claims.
	now := time.Now().Unix()

	if claims.Exp != nil && now > *claims.Exp {
		return nil, errJWTExpired
	}

	if claims.Nbf != nil && now < *claims.Nbf {
		return nil, errJWTNotYetValid
	}

	return &claims, nil
}

// callbackJWTMiddleware wraps an http.Handler with JWT verification for BotX callbacks.
// When verifyEnabled is false, the middleware passes requests through without checking.
// When verification fails, it responds with HTTP 401 and a JSON error body.
func callbackJWTMiddleware(h http.Handler, secretLookup func(botID string) (string, error), verifyEnabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyEnabled {
			h.ServeHTTP(w, r)
			return
		}

		if secretLookup == nil {
			vlog.V1("server: JWT verification enabled but no secret lookup configured")
			writeJWTError(w, "JWT verification is not configured")
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeJWTError(w, "missing Authorization header")
			return
		}

		if !strings.HasPrefix(auth, "Bearer ") {
			writeJWTError(w, "Authorization header must use Bearer scheme")
			return
		}

		token := strings.TrimPrefix(auth, "Bearer ")
		if token == "" {
			writeJWTError(w, "empty Bearer token")
			return
		}

		_, err := verifyCallbackJWT(token, secretLookup)
		if err != nil {
			vlog.Info("server: callback JWT verification failed: %v", err)
			writeJWTError(w, "JWT verification failed")
			return
		}

		h.ServeHTTP(w, r)
	})
}

// writeJWTError writes a 401 JSON error response for JWT verification failures.
func writeJWTError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{
		"ok":    false,
		"error": msg,
	})
}
