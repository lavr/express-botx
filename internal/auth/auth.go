package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lavr/express-botx/internal/botapi"
	vlog "github.com/lavr/express-botx/internal/log"
)

// BuildSignature creates HMAC-SHA256 signature for BotX API authentication.
// Returns uppercase hex-encoded string.
func BuildSignature(botID, secretKey string) string {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(botID))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

type tokenResponse struct {
	Status string `json:"status"`
	Result string `json:"result"`
}

// GetToken obtains a bot token from BotX API.
// Host can be a hostname ("express.company.ru") or a full URL ("http://localhost:8080").
// Endpoint: GET /api/v2/botx/bots/{bot_id}/token?signature={sig}
func GetToken(ctx context.Context, host, botID, signature string, timeout time.Duration) (string, error) {
	baseURL := botapi.ResolveBaseURL(host)
	url := fmt.Sprintf("%s/api/v2/botx/bots/%s/token?signature=%s", baseURL, botID, signature)
	client := &http.Client{Timeout: timeout}
	return doGetToken(ctx, url, client)
}

func doGetToken(ctx context.Context, url string, client *http.Client) (string, error) {
	vlog.V2("auth: GET %s", maskSignatureInURL(url))

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		vlog.V1("auth: <- %d (%dms)", resp.StatusCode, elapsed.Milliseconds())
		return "", fmt.Errorf("token request failed: HTTP %d", resp.StatusCode)
	}

	var result tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	if result.Status != "ok" {
		return "", fmt.Errorf("unexpected status: %s", result.Status)
	}

	vlog.V1("auth: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
	return result.Result, nil
}

func maskSignatureInURL(url string) string {
	if i := strings.Index(url, "signature="); i != -1 {
		sig := url[i+len("signature="):]
		return url[:i+len("signature=")] + vlog.Mask(sig)
	}
	return url
}
