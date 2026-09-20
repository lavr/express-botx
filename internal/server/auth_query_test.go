package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestQueryKeyAuth(t *testing.T) {
	tests := []struct {
		name     string
		keys     []ResolvedKey
		target   string
		headers  map[string]string
		wantCode int
	}{
		{
			name:     "query key accepted when key opted in",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret",
			wantCode: 200,
		},
		{
			name:     "query key refused when key did not opt in",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret"}},
			target:   "/api/v1/send?api_key=secret",
			wantCode: 401,
		},
		{
			name:     "unknown query key refused identically to one not opted in",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=wrong",
			wantCode: 401,
		},
		{
			name:     "invalid bearer suppresses the query fallback",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret",
			headers:  map[string]string{"Authorization": "Bearer wrong"},
			wantCode: 403,
		},
		{
			name:     "valid header wins over a bogus query key",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=bogus",
			headers:  map[string]string{"X-API-Key": "secret"},
			wantCode: 200,
		},
		{
			name:     "percent-escaped parameter name is honoured",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api%5Fkey=secret",
			wantCode: 200,
		},
		{
			name:     "duplicated query key is refused as ambiguous",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret&api_key=secret",
			wantCode: 401,
		},
		{
			name:     "a malformed second value does not rescue a duplicate",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret&api_key=%zz",
			wantCode: 401,
		},
		{
			name:     "unparsable query is not trusted for credentials",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret&broken=%zz",
			wantCode: 401,
		},
		{
			name:     "non-bearer Authorization suppresses the query fallback",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret",
			headers:  map[string]string{"Authorization": "Basic d3Jvbmc="},
			wantCode: 401,
		},
		{
			name:     "bearer with an empty token suppresses the query fallback",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret",
			headers:  map[string]string{"Authorization": "Bearer "},
			wantCode: 401,
		},
		{
			name:     "lowercase bearer suppresses the query fallback",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret",
			headers:  map[string]string{"Authorization": "bearer secret"},
			wantCode: 401,
		},
		{
			name:     "an empty Authorization header suppresses the query fallback",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret",
			headers:  map[string]string{"Authorization": ""},
			wantCode: 401,
		},
		{
			name:     "an empty X-API-Key header suppresses the query fallback",
			keys:     []ResolvedKey{{Name: "ir", Key: "secret", AllowQueryAuth: true}},
			target:   "/api/v1/send?api_key=secret",
			headers:  map[string]string{"X-API-Key": ""},
			wantCode: 401,
		},
		{
			name: "opting one key in does not opt the others in",
			keys: []ResolvedKey{
				{Name: "ir", Key: "secret", AllowQueryAuth: true},
				{Name: "other", Key: "other-secret"},
			},
			target:   "/api/v1/send?api_key=other-secret",
			wantCode: 401,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(tt.keys)
			body := strings.NewReader(`{"chat_id":"c1","message":"hi"}`)
			headers := map[string]string{"Content-Type": "application/json"}
			for k, v := range tt.headers {
				headers[k] = v
			}
			w := doRequest(srv, "POST", tt.target, body, headers)
			if w.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantCode, w.Body.String())
			}
		})
	}
}

func TestQueryKeyStrippedBeforeLogging(t *testing.T) {
	const secret = "s3cr3t-key"

	logs := captureTrace(t, 1, func() {
		srv := newTestServer([]ResolvedKey{{Name: "ir", Key: secret, AllowQueryAuth: true}})
		body := strings.NewReader(`{"chat_id":"infra","message":"hi"}`)
		w := doRequest(srv, "POST", "/api/v1/send?api_key="+secret+"&no_parse=true", body,
			map[string]string{"Content-Type": "application/json"})
		if w.Code != 200 {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
	})

	if strings.Contains(logs, secret) {
		t.Errorf("api key leaked into logs:\n%s", logs)
	}
	if !strings.Contains(logs, "no_parse=true") {
		t.Errorf("unrelated query parameters were dropped:\n%s", logs)
	}
}

type querySpyTracker struct{ seen []string }

func (t *querySpyTracker) Middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.seen = append(t.seen, r.URL.RawQuery, r.RequestURI)
		h.ServeHTTP(w, r)
	})
}

func (t *querySpyTracker) CaptureError(error) {}
func (t *querySpyTracker) Flush()             {}

func TestQueryKeyStrippedBeforeErrorTracker(t *testing.T) {
	const secret = "s3cr3t-key"

	targets := []string{
		"/api/v1/send?api_key=" + secret,
		"/api/v1/send?api%5Fkey=" + secret,
		"/api/v1/send?api_key=" + secret + "&api_key=" + secret,
		"/api/v1/send?api_key=" + secret + "&broken=%zz",
		"/api/v1/nope?api_key=" + secret,
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			spy := &querySpyTracker{}
			cfg := Config{Listen: ":0", BasePath: "/api/v1", Keys: []ResolvedKey{{Name: "ir", Key: secret, AllowQueryAuth: true}}}
			sendFn := func(ctx context.Context, p *SendPayload) (string, error) { return "sync", nil }
			chatFn := func(chatID string) (ChatResolveResult, error) { return ChatResolveResult{ChatID: chatID}, nil }
			srv := New(cfg, sendFn, chatFn, WithErrTracker(spy))

			doRequest(srv, "POST", target, strings.NewReader(`{"chat_id":"c1","message":"hi"}`),
				map[string]string{"Content-Type": "application/json"})

			if len(spy.seen) == 0 {
				t.Fatal("error tracker middleware never ran")
			}
			for _, got := range spy.seen {
				if strings.Contains(got, secret) {
					t.Errorf("error tracker saw the key: %q", got)
				}
			}
		})
	}
}
