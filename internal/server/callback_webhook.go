package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// WebhookHandler sends callback events as HTTP POST requests to a configured URL.
type WebhookHandler struct {
	url     string
	timeout time.Duration
	client  *http.Client
}

// NewWebhookHandler creates a WebhookHandler that POSTs callback payloads
// to the given URL with the specified timeout. A zero timeout means no deadline.
func NewWebhookHandler(url string, timeout time.Duration) *WebhookHandler {
	return &WebhookHandler{
		url:     url,
		timeout: timeout,
		client:  &http.Client{},
	}
}

// Type returns "webhook".
func (h *WebhookHandler) Type() string {
	return "webhook"
}

// Handle sends the callback payload as an HTTP POST to the configured URL.
// It sets Content-Type, X-Express-Event, and X-Express-Sync-ID headers.
func (h *WebhookHandler) Handle(ctx context.Context, event string, payload []byte) error {
	if h.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook handler %q: failed to create request: %w", h.url, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Express-Event", event)

	// Extract sync_id from payload for the header.
	var meta struct {
		SyncID string `json:"sync_id"`
	}
	if err := json.Unmarshal(payload, &meta); err == nil && meta.SyncID != "" {
		req.Header.Set("X-Express-Sync-ID", meta.SyncID)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("webhook handler %q: request timed out", h.url)
		}
		var netErr *net.OpError
		if errors.As(err, &netErr) {
			if netErr.Op == "dial" {
				return fmt.Errorf("webhook handler %q: connection refused (is the server running at %s?)", h.url, h.url)
			}
		}
		return fmt.Errorf("webhook handler %q: %w", h.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook handler %q: unexpected status %d: %s", h.url, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Drain the response body so the underlying TCP connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	return nil
}
