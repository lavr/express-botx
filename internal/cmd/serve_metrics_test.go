package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lavr/express-botx/internal/config"
)

func TestServeMetrics_Disabled(t *testing.T) {
	t.Setenv("EXPRESS_BOTX_SERVER_METRICS_LISTEN", "")
	sm, err := newServeMetrics(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sm.m != nil {
		t.Fatal("metrics created while disabled")
	}
	if err := sm.start(context.Background()); err != nil {
		t.Errorf("start while disabled: %v", err)
	}
	if c := sm.botxClient(time.Second); c == nil || c.Timeout != time.Second {
		t.Errorf("botx client = %+v", c)
	}
}

func TestServeMetrics_EnvEnables(t *testing.T) {
	t.Setenv("EXPRESS_BOTX_SERVER_METRICS_LISTEN", "127.0.0.1:0")
	sm, err := newServeMetrics(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sm.m == nil || sm.path != "/metrics" {
		t.Fatalf("env did not enable metrics: %+v", sm)
	}
}

// Calls made through the BotX client of a serve run show up in its metrics.
func TestServeMetrics_BotXClientIsObserved(t *testing.T) {
	t.Setenv("EXPRESS_BOTX_SERVER_METRICS_LISTEN", "127.0.0.1:0")
	sm, err := newServeMetrics(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	botx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer botx.Close()

	resp, err := sm.botxClient(time.Second).Get(botx.URL + "/api/v3/botx/chats/info?group_chat_id=x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	w := httptest.NewRecorder()
	sm.m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	want := `express_botx_botx_requests_total{code="503",endpoint="/api/v3/botx/chats/info"} 1`
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("missing %s", want)
	}
}

func TestServeMetrics_BindFailureStopsStartup(t *testing.T) {
	ln := httptest.NewServer(http.NotFoundHandler())
	defer ln.Close()
	addr := strings.TrimPrefix(ln.URL, "http://")

	t.Setenv("EXPRESS_BOTX_SERVER_METRICS_LISTEN", addr)
	sm, err := newServeMetrics(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sm.start(context.Background()); err == nil {
		t.Error("start on a taken address succeeded")
	}
}
