package metrics

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != 200 {
		t.Fatalf("scrape: status %d", w.Code)
	}
	return w.Body.String()
}

func requireLine(t *testing.T, body, line string) {
	t.Helper()
	for _, l := range strings.Split(body, "\n") {
		if l == line {
			return
		}
	}
	t.Errorf("missing %q in:\n%s", line, grepPrefix(body, strings.SplitN(line, "{", 2)[0]))
}

func grepPrefix(body, prefix string) string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestBotXEndpoint(t *testing.T) {
	tests := map[string]string{
		"/api/v2/botx/bots/2a8e7c41-5b0d-4f3e-9c6a-1d2e3f405162/token": "/api/v2/botx/bots/:id/token",
		"/api/v2/botx/bots/anything-at-all/token":                      "/api/v2/botx/bots/:id/token",
		"/api/v4/botx/notifications/direct":                            "/api/v4/botx/notifications/direct",
		"/api/v3/botx/chats/info":                                      "/api/v3/botx/chats/info",
		"/api/v3/botx/users/by_email":                                  "/api/v3/botx/users/by_email",
		"/api/v3/botx/chats/info/extra":                                "other",
		"/api/v9/botx/something/new":                                   "other",
		"/hooks/random":                                                "other",
		"":                                                             "other",
	}
	for path, want := range tests {
		if got := BotXEndpoint(path); got != want {
			t.Errorf("BotXEndpoint(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestNilMetricsIsANoop(t *testing.T) {
	var m *Metrics
	m.ObserveHTTP("POST", "/x", 200, time.Millisecond)
	m.Delivery("/x", Delivered)
	m.PartialDelivery("/x")
	m.Edit("/x", Failed)
	m.MembershipCheck(Member, SourceCache)
	m.LookupStarted()
	m.LookupDone(Error)
	m.LookupRejected()
	m.QueuePublish(nil)
	m.InitSendRoute("/x")
	m.ObserveBotX(httptest.NewRequest("GET", "/api/v3/botx/chats/list", nil), nil, errors.New("x"), 0)
	if FromContext(WithContext(context.Background(), m)) != nil {
		t.Error("nil metrics attached to a context")
	}
}

func TestSeriesExistAtZero(t *testing.T) {
	m := New()
	m.InitSendRoute("/api/v1/send")
	body := scrape(t, m)
	for _, line := range []string{
		`express_botx_deliveries_total{handler="/api/v1/send",outcome="failed"} 0`,
		`express_botx_partial_deliveries_total{handler="/api/v1/send"} 0`,
		`express_botx_membership_checks_total{outcome="error",source="lookup"} 0`,
		`express_botx_membership_lookups_total{outcome="not_member"} 0`,
		`express_botx_queue_publish_total{outcome="error"} 0`,
		`express_botx_membership_lookups_inflight 0`,
	} {
		requireLine(t, body, line)
	}
	// The process collector reports only where procfs exists (Linux).
	if !strings.Contains(body, "go_goroutines") {
		t.Error("Go runtime collector missing")
	}
}

func TestInstancesAreIndependent(t *testing.T) {
	a, b := New(), New()
	a.QueuePublish(nil)
	a.QueuePublish(errors.New("broker down"))
	requireLine(t, scrape(t, a), `express_botx_queue_publish_total{outcome="ok"} 1`)
	requireLine(t, scrape(t, b), `express_botx_queue_publish_total{outcome="ok"} 0`)
}

func TestObserveBotXAndHTTP(t *testing.T) {
	m := New()
	req := httptest.NewRequest("GET", "/api/v3/botx/chats/info?group_chat_id=secret-looking-value", nil)
	m.ObserveBotX(req, &http.Response{StatusCode: 503}, nil, 20*time.Millisecond)
	m.ObserveBotX(req, nil, errors.New("timeout"), time.Second)
	m.ObserveHTTP("BREW", "other", 418, time.Millisecond)
	m.LookupStarted()
	m.LookupDone(Member)
	m.LookupRejected()

	body := scrape(t, m)
	requireLine(t, body, `express_botx_botx_requests_total{code="503",endpoint="/api/v3/botx/chats/info"} 1`)
	requireLine(t, body, `express_botx_botx_requests_total{code="error",endpoint="/api/v3/botx/chats/info"} 1`)
	requireLine(t, body, `express_botx_http_requests_total{code="418",handler="other",method="OTHER"} 1`)
	requireLine(t, body, `express_botx_membership_lookups_total{outcome="member"} 1`)
	requireLine(t, body, `express_botx_membership_lookups_inflight 0`)
	requireLine(t, body, `express_botx_membership_lookups_rejected_total 1`)
	if strings.Contains(body, "secret-looking-value") {
		t.Error("query string leaked into a label")
	}
}

func TestStart(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	addr, err := m.Start(ctx, "127.0.0.1:0", "/metrics", t.Logf)
	if err != nil {
		t.Fatal(err)
	}

	get := func(path string) (int, string) {
		resp, err := http.Get("http://" + addr.String() + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("/metrics"); code != 200 || !strings.Contains(body, "express_botx_queue_publish_total") {
		t.Errorf("/metrics: %d", code)
	}
	if code, _ := get("/debug/pprof/"); code != 404 {
		t.Errorf("/debug/pprof/: %d, want 404", code)
	}

	// A taken address fails at once.
	if _, err := m.Start(ctx, addr.String(), "/metrics", t.Logf); err == nil {
		t.Error("second Start on the same address succeeded")
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr.String(), 100*time.Millisecond)
		if err != nil {
			break
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatal("metrics listener still open after cancel")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConcurrentObserveAndScrape(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				m.Delivery("/api/v1/send", Delivered)
				m.ObserveHTTP("POST", "/api/v1/send", 200, time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = scrape(t, m)
			}
		}()
	}
	wg.Wait()
	requireLine(t, scrape(t, m), `express_botx_deliveries_total{handler="/api/v1/send",outcome="delivered"} 1600`)
}
