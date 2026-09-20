package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// A scoped key has every per-chat error rewritten to "chat not allowed" before
// it reaches the client, so the server log is the only place the real cause can
// survive. Without this line a delivery outage is indistinguishable from a
// permissions problem for both the caller and the operator.
func TestFanout_LogsRealCauseForScopedKey(t *testing.T) {
	const ownUUID2 = "bcb715a2-e8d3-57a8-ab3b-6a14c044dd22"

	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{
		{Name: "narrow", Key: "k", Chats: []string{ownUUID2}},
	}}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "", fmt.Errorf("sending: dial tcp 10.0.0.1:443: connect: connection refused")
	}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: ownUUID2}, nil
	}

	var body string
	logs := captureTrace(t, 1, func() {
		srv := New(cfg, sendFn, chatFn, WithAlertmanager(testAlertmanagerConfig(t)))
		w := doRequest(srv, "POST", "/api/v1/alertmanager?chat_id="+ownUUID2,
			strings.NewReader(alertmanagerPayload("firing", AlertItem{
				Status: "firing",
				Labels: map[string]string{"alertname": "T", "severity": "critical"},
			})),
			map[string]string{"X-API-Key": "k", "Content-Type": "application/json"})
		body = w.Body.String()
	})

	if !strings.Contains(body, "chat not allowed") {
		t.Fatalf("expected the response to stay sanitized, got %s", body)
	}
	if !strings.Contains(logs, "connection refused") {
		t.Errorf("real cause missing from the server log:\n%s", logs)
	}
	if !strings.Contains(logs, "narrow") {
		t.Errorf("log does not name the key:\n%s", logs)
	}
}

// The cause is logged verbatim, but an upstream error carries the whole
// response body, so the line has to be bounded like every other payload log.
func TestFanout_BoundsTheLoggedCause(t *testing.T) {
	const marker = "TAIL-MARKER"
	huge := strings.Repeat("x", 8000) + marker

	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "k", Key: "k"}}}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		return "", fmt.Errorf("send failed: HTTP 400: %s", huge)
	}
	chatFn := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}

	logs := captureTrace(t, 1, func() {
		srv := New(cfg, sendFn, chatFn, WithAlertmanager(testAlertmanagerConfig(t)))
		doRequest(srv, "POST", "/api/v1/alertmanager?chat_id=c1",
			strings.NewReader(alertmanagerPayload("firing", AlertItem{
				Status: "firing",
				Labels: map[string]string{"alertname": "T", "severity": "critical"},
			})),
			map[string]string{"X-API-Key": "k", "Content-Type": "application/json"})
	})

	if strings.Contains(logs, marker) {
		t.Errorf("the whole upstream body reached the log unbounded (%d bytes)", len(logs))
	}
	if !strings.Contains(logs, "HTTP 400") {
		t.Errorf("the useful head of the cause was lost:\n%s", logs[:min(len(logs), 400)])
	}
}
