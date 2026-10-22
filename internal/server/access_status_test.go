package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// newAccessServer builds a server whose send pipeline answers per chat with
// the given error, or succeeds for a chat it has no error for.
func newAccessServer(keys []ResolvedKey, outcomes map[string]error, srvOpts ...Option) *Server {
	cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: keys}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		if err := outcomes[p.ChatID]; err != nil {
			return "", err
		}
		return "sync-id", nil
	}
	resolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	return New(cfg, sendFn, resolver, srvOpts...)
}

func TestAccessStatus_Send(t *testing.T) {
	plain := errors.New("send failed: HTTP 500")
	tests := []struct {
		name     string
		outcomes map[string]error
		chats    string
		want     int
	}{
		{"access unavailable", map[string]error{ownUUID: ErrAccessUnavailable}, ownUUID, 503},
		{"wrapped access unavailable", map[string]error{ownUUID: errors.Join(errors.New("lookup"), ErrAccessUnavailable)}, ownUUID, 503},
		{"denied at delivery", map[string]error{ownUUID: ErrAccessDenied}, ownUUID, 403},
		{"every chat denied", map[string]error{ownUUID: ErrAccessDenied, otherUUID: ErrAccessDenied}, ownUUID + "," + otherUUID, 403},
		{"denied and unavailable", map[string]error{ownUUID: ErrAccessDenied, otherUUID: ErrAccessUnavailable}, ownUUID + "," + otherUUID, 503},
		{"denied and a delivery failure", map[string]error{ownUUID: ErrAccessDenied, otherUUID: plain}, ownUUID + "," + otherUUID, 502},
		{"plain ErrChatNotAllowed keeps 502", map[string]error{ownUUID: ErrChatNotAllowed}, ownUUID, 502},
		{"delivery failure keeps 502", map[string]error{ownUUID: plain}, ownUUID, 502},
		{"partial delivery stays 200", map[string]error{otherUUID: ErrAccessUnavailable}, ownUUID + "," + otherUUID, 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAccessServer([]ResolvedKey{{Name: "open", Key: "open"}}, tc.outcomes)
			w := sendAs(srv, "open", tc.chats)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.want, w.Body.String())
			}
			if retry := w.Header().Get("Retry-After"); (tc.want == 503) != (retry != "") {
				t.Errorf("Retry-After = %q with status %d", retry, w.Code)
			}
		})
	}
}

// A scoped key's per-chat errors are rewritten before the response is
// chosen; the status must survive that.
func TestAccessStatus_ScopedKeyKeepsStatus(t *testing.T) {
	key := ResolvedKey{Name: "narrow", Key: "narrow", Chats: []string{ownUUID, otherUUID}}
	srv := newAccessServer([]ResolvedKey{key}, map[string]error{ownUUID: ErrAccessDenied, otherUUID: ErrAccessUnavailable})
	if w := sendAs(srv, "narrow", ownUUID+","+otherUUID); w.Code != 503 {
		t.Errorf("status = %d, want 503 (body: %s)", w.Code, w.Body.String())
	}
}

func TestAccessStatus_Webhooks(t *testing.T) {
	am := testAlertmanagerConfig(t)
	am.DefaultChatID = ownUUID
	gr := testGrafanaConfig(t)
	gr.DefaultChatID = ownUUID
	keys := []ResolvedKey{{Name: "open", Key: "open"}}

	post := func(srv *Server, path, body string) *httptest.ResponseRecorder {
		return doRequest(srv, "POST", path, strings.NewReader(body),
			map[string]string{"Content-Type": "application/json", "Authorization": "Bearer open"})
	}
	amBody := alertmanagerPayload("firing", AlertItem{Status: "firing", Labels: map[string]string{"alertname": "HighCPU"}})
	grBody := grafanaPayload("firing", "alerting", "[FIRING:1] HighCPU", GrafanaAlertItem{Status: "firing", Labels: map[string]string{"alertname": "HighCPU"}})

	srv := newAccessServer(keys, map[string]error{ownUUID: ErrAccessUnavailable}, WithAlertmanager(am), WithGrafana(gr))
	if w := post(srv, "/api/v1/alertmanager", amBody); w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Errorf("alertmanager: status = %d, Retry-After = %q, want 503 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
	if w := post(srv, "/api/v1/grafana", grBody); w.Code != 503 {
		t.Errorf("grafana: status = %d, want 503", w.Code)
	}

	srv = newAccessServer(keys, map[string]error{ownUUID: ErrAccessDenied}, WithAlertmanager(am), WithGrafana(gr))
	if w := post(srv, "/api/v1/alertmanager", amBody); w.Code != 403 {
		t.Errorf("alertmanager: status = %d, want 403", w.Code)
	}
	if w := post(srv, "/api/v1/grafana", grBody); w.Code != 403 {
		t.Errorf("grafana: status = %d, want 403", w.Code)
	}
}

func TestAccessStatus_FailedStatusMatchesResponse(t *testing.T) {
	for _, errs := range [][]SendError{
		{{cause: ErrAccessUnavailable}},
		{{cause: ErrAccessDenied}, {cause: ErrAccessDenied}},
		{{cause: ErrAccessDenied}, {cause: errors.New("boom")}},
	} {
		w := httptest.NewRecorder()
		writeMultiSend(w, nil, errs, 200)
		if got := failedStatus(errs); got != w.Code {
			t.Errorf("failedStatus = %d, response = %d", got, w.Code)
		}
	}
}
