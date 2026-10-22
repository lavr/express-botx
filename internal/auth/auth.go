package auth

import (
	"context"
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
	return botx.RequestToken(ctx, host, botID, signature, botx.WithTimeout(timeout))
}
