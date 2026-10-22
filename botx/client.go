package botx

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/lavr/express-botx/internal/httputil"
	vlog "github.com/lavr/express-botx/internal/log"
)

// DefaultTimeout is the HTTP timeout of a Client built without WithTimeout or WithHTTPClient.
const DefaultTimeout = 10 * time.Second

// Client is a BotX API client. It is safe for concurrent use.
//
// A Client built by New obtains the bot token on the first call and obtains a new
// one once when a call is answered with HTTP 401, then repeats that call. Calls
// that need a token while one is being obtained wait for it, each within its own
// context. A Client built by NewWithToken uses its token as is.
type Client struct {
	baseURL      string
	httpClient   *http.Client
	botID        string
	signature    string
	tokenTimeout time.Duration

	mu       sync.Mutex
	token    string
	gen      int
	inflight *tokenCall
}

// tokenCall is a token request shared by the calls waiting for it.
type tokenCall struct {
	done  chan struct{}
	token string
	gen   int
	err   error
}

// Option configures a Client.
type Option func(*options)

type options struct {
	timeout    time.Duration
	httpClient *http.Client
	baseURL    string
}

// WithTimeout sets the timeout of the default HTTP client and of obtaining a token
// (DefaultTimeout when not positive). With WithHTTPClient it bounds only obtaining a token.
func WithTimeout(d time.Duration) Option {
	return func(o *options) { o.timeout = d }
}

// WithHTTPClient makes the Client send requests through hc.
func WithHTTPClient(hc *http.Client) Option {
	return func(o *options) { o.httpClient = hc }
}

// WithBaseURL replaces the base URL derived from the host, e.g. with an httptest server URL.
func WithBaseURL(u string) Option {
	return func(o *options) { o.baseURL = strings.TrimRight(u, "/") }
}

func buildOptions(host string, opts []Option) options {
	o := options{timeout: DefaultTimeout, baseURL: httputil.ResolveBaseURL(host)}
	for _, opt := range opts {
		opt(&o)
	}
	if o.httpClient == nil {
		o.httpClient = httputil.NewClient(o.timeout)
	}
	return o
}

// New returns a Client that authenticates as the bot botID with its secret key.
// Host is a hostname ("express.example.com", https is implied) or a full URL
// ("http://localhost:8080"). No request is made until the first call.
func New(host, botID, secret string, opts ...Option) (*Client, error) {
	if botID == "" {
		return nil, errors.New("botx: bot ID is required")
	}
	if secret == "" {
		return nil, errors.New("botx: bot secret is required")
	}
	o := buildOptions(host, opts)
	return &Client{
		baseURL:      o.baseURL,
		httpClient:   o.httpClient,
		botID:        botID,
		signature:    BuildSignature(botID, secret),
		tokenTimeout: o.timeout,
	}, nil
}

// NewWithToken returns a Client that sends token with every request and never
// refreshes it: a call answered with HTTP 401 fails with ErrUnauthorized.
func NewWithToken(host, token string, opts ...Option) *Client {
	o := buildOptions(host, opts)
	return &Client{baseURL: o.baseURL, httpClient: o.httpClient, token: token}
}

// BuildSignature returns the BotX signature of a bot: HMAC-SHA256 of botID keyed
// by secret, upper-case hex. It is a credential, never log or print it.
func BuildSignature(botID, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(botID))
	return strings.ToUpper(hex.EncodeToString(mac.Sum(nil)))
}

// RequestToken obtains a new bot token by signature, see BuildSignature.
// A Client built by New does this itself; RequestToken is for callers that keep
// tokens on their own. Errors never contain the signature.
func RequestToken(ctx context.Context, host, botID, signature string, opts ...Option) (string, error) {
	o := buildOptions(host, opts)
	return requestToken(ctx, o.httpClient, o.baseURL, botID, signature)
}

func requestToken(ctx context.Context, hc *http.Client, baseURL, botID, signature string) (string, error) {
	tok, err := doRequestToken(ctx, hc, baseURL, botID, signature)
	return tok, redactError(err, signature)
}

func doRequestToken(ctx context.Context, hc *http.Client, baseURL, botID, signature string) (string, error) {
	url := fmt.Sprintf("%s/api/v2/botx/bots/%s/token?signature=%s", baseURL, neturl.PathEscape(botID), neturl.QueryEscape(signature))
	vlog.V2("auth: GET %s", maskSignatureInURL(url))

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	// Diagnostics get the body with the signature masked before it is cut,
	// so a signature crossing the cut cannot survive in part.
	respBody, readErr := io.ReadAll(resp.Body)
	diag := maskSignature(respBody, signature)
	if resp.StatusCode != http.StatusOK {
		vlog.V1("auth: <- %d (%dms)", resp.StatusCode, elapsed.Milliseconds())
		return "", newAPIError("token request", resp.StatusCode, diag, "token request failed: HTTP %d", resp.StatusCode)
	}
	if readErr != nil {
		apiErr := newAPIError("token request", resp.StatusCode, diag, "token request: HTTP %d, reading response: %v", resp.StatusCode, readErr)
		apiErr.cause = readErr
		return "", apiErr
	}

	var result struct {
		Status string `json:"status"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", newAPIError("token request", resp.StatusCode, diag, "decoding token response: %v", err)
	}
	if result.Status != "ok" {
		return "", newAPIError("token request", resp.StatusCode, diag, "unexpected status: %s", result.Status)
	}
	if result.Result == "" {
		return "", newAPIError("token request", resp.StatusCode, diag, "token request: response has no token")
	}

	vlog.V1("auth: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
	return result.Result, nil
}

func maskSignature(body []byte, signature string) []byte {
	if signature == "" {
		return body
	}
	mask := []byte(vlog.Mask(signature))
	body = bytes.ReplaceAll(body, []byte(signature), mask)
	return bytes.ReplaceAll(body, []byte(neturl.QueryEscape(signature)), mask)
}

func maskSignatureInURL(url string) string {
	if i := strings.Index(url, "signature="); i != -1 {
		sig := url[i+len("signature="):]
		return url[:i+len("signature=")] + vlog.Mask(sig)
	}
	return url
}

// redactedError carries the text of an error with the signature masked. Only
// context cancellation stays reachable through Unwrap: the original chain holds
// the signature in places such as a *url.Error or a redirect parse error.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.cause }

// redactError masks every occurrence of the signature in err. An *APIError keeps
// its type with Status, Reason, Body and message masked.
func redactError(err error, signature string) error {
	if err == nil || signature == "" {
		return err
	}
	mask := vlog.Mask(signature)
	replacer := strings.NewReplacer(signature, mask, neturl.QueryEscape(signature), mask)
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		redacted := *apiErr
		redacted.msg = replacer.Replace(err.Error())
		redacted.Status = replacer.Replace(apiErr.Status)
		redacted.Reason = replacer.Replace(apiErr.Reason)
		redacted.Body = replacer.Replace(apiErr.Body)
		redacted.cause = contextCause(err)
		return &redacted
	}
	return &redactedError{msg: replacer.Replace(err.Error()), cause: contextCause(err)}
}

func contextCause(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		return context.Canceled
	}
	return nil
}

// currentToken returns the token to use and its generation.
func (c *Client) currentToken(ctx context.Context) (string, int, error) {
	c.mu.Lock()
	if c.token != "" || c.signature == "" {
		defer c.mu.Unlock()
		return c.token, c.gen, nil
	}
	call := c.startTokenCall()
	c.mu.Unlock()
	return c.waitToken(ctx, call, "getting token")
}

// renewToken obtains a new token after the one of generation gen was rejected,
// unless another call already replaced it.
func (c *Client) renewToken(ctx context.Context, gen int) (string, int, error) {
	c.mu.Lock()
	if c.gen != gen {
		defer c.mu.Unlock()
		return c.token, c.gen, nil
	}
	vlog.V1("auth: 401, refreshing token")
	call := c.startTokenCall()
	c.mu.Unlock()
	return c.waitToken(ctx, call, "refreshing token")
}

// startTokenCall joins the token request in flight or starts one. The request
// runs detached from the caller's context, bounded by the client timeout, so
// that one caller giving up does not fail the others. c.mu must be held.
func (c *Client) startTokenCall() *tokenCall {
	if c.inflight != nil {
		return c.inflight
	}
	call := &tokenCall{done: make(chan struct{})}
	c.inflight = call
	go func() {
		timeout := c.tokenTimeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		tok, err := requestToken(ctx, c.httpClient, c.baseURL, c.botID, c.signature)
		c.mu.Lock()
		if err == nil {
			c.token = tok
			c.gen++
		}
		call.token, call.gen, call.err = c.token, c.gen, err
		c.inflight = nil
		c.mu.Unlock()
		close(call.done)
	}()
	return call
}

func (c *Client) waitToken(ctx context.Context, call *tokenCall, op string) (string, int, error) {
	select {
	case <-call.done:
		if call.err != nil {
			return "", 0, fmt.Errorf("%s: %w", op, call.err)
		}
		return call.token, call.gen, nil
	case <-ctx.Done():
		return "", 0, fmt.Errorf("%s: %w", op, ctx.Err())
	}
}

// do sends a request with the bot token. On HTTP 401 a Client with a secret
// renews the token and sends the request once more: BotX rejected the first
// one, so repeating it cannot apply a change twice.
func (c *Client) do(ctx context.Context, method, path, contentType string, payload []byte) (*http.Response, error) {
	tok, gen, err := c.currentToken(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, method, path, contentType, payload, tok)
	if err != nil || resp.StatusCode != http.StatusUnauthorized || c.signature == "" {
		return resp, err
	}
	resp.Body.Close() //nolint:errcheck // best-effort close
	if tok, _, err = c.renewToken(ctx, gen); err != nil {
		return nil, err
	}
	return c.send(ctx, method, path, contentType, payload, tok)
}

func (c *Client) send(ctx context.Context, method, path, contentType string, payload []byte, tok string) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.httpClient.Do(req)
}

func (c *Client) bearer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return vlog.MaskBearer(c.token)
}

// readResponse reads the body of resp. A failed read is reported as an
// *APIError that keeps the HTTP status, so a cut 401 still matches ErrUnauthorized.
func readResponse(op string, resp *http.Response) ([]byte, *APIError) {
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		return body, nil
	}
	var apiErr *APIError
	if resp.StatusCode == http.StatusUnauthorized {
		apiErr = unauthorized(op, body)
	} else {
		apiErr = newAPIError(op, resp.StatusCode, body, "%s: HTTP %d, reading response: %v", op, resp.StatusCode, err)
	}
	apiErr.cause = err
	return body, apiErr
}

// envelopeOf extracts the "status" and "reason" fields of a BotX response body.
func envelopeOf(body []byte) (status, reason string) {
	var env struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(body, &env) != nil {
		return "", ""
	}
	return env.Status, env.Reason
}
