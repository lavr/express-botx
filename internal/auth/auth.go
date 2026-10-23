package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/lavr/express-botx/botx"
)

// BuildSignature creates HMAC-SHA256 signature for BotX API authentication.
// Returns uppercase hex-encoded string.
func BuildSignature(botID, secretKey string) string {
	return botx.BuildSignature(botID, secretKey)
}

// GetToken obtains a bot token from BotX API.
// Host can be a hostname ("express.company.ru") or a full URL ("http://localhost:8080").
// Endpoint: GET /api/v2/botx/bots/{bot_id}/token?signature={sig}
func GetToken(ctx context.Context, host, botID, signature string, timeout time.Duration) (string, error) {
	return GetTokenWithClient(ctx, host, botID, signature, timeout, nil)
}

// GetTokenWithClient is GetToken over the given HTTP client; nil uses the
// default one with timeout.
func GetTokenWithClient(ctx context.Context, host, botID, signature string, timeout time.Duration, hc *http.Client) (string, error) {
	opts := []botx.Option{botx.WithTimeout(timeout)}
	if hc != nil {
		opts = append(opts, botx.WithHTTPClient(hc))
	}
	return botx.RequestToken(ctx, host, botID, signature, opts...)
}
