package server

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	vlog "github.com/lavr/express-botx/internal/log"
)

func captureTrace(t *testing.T, level int, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStderr, prevLevel := os.Stderr, vlog.Level
	os.Stderr, vlog.Level = w, level
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	defer func() {
		os.Stderr, vlog.Level = prevStderr, prevLevel
		_ = w.Close()
		_ = r.Close()
	}()
	fn()
	os.Stderr, vlog.Level = prevStderr, prevLevel
	_ = w.Close()
	return <-done
}

type countingReader struct {
	r     io.Reader
	reads int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	return c.r.Read(p)
}

func grafanaHeaders() map[string]string {
	return map[string]string{"X-API-Key": "k", "Content-Type": "application/json"}
}

// --- V3 tracing ---

func TestGrafana_LogsRawPayloadAtV3(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))
	body := grafanaPayloadWithMessage("Disk almost full", "node-1 at 94%")

	var code int
	logs := captureTrace(t, 3, func() {
		w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), grafanaHeaders())
		code = w.Code
	})

	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(logs, "grafana: raw payload") || !strings.Contains(logs, body) {
		t.Errorf("raw payload not traced:\n%s", logs)
	}
	if captured != "Disk almost full\n\nnode-1 at 94%" {
		t.Errorf("body was not decoded after reading: message = %q", captured)
	}
}

func TestGrafana_DoesNotLogAuthorizationHeaders(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))

	logs := captureTrace(t, 3, func() {
		doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(grafanaPayloadWithMessage("t", "m")), grafanaHeaders())
	})

	if strings.Contains(logs, "X-API-Key") {
		t.Errorf("trace log leaks request headers:\n%s", logs)
	}
}

func TestGrafana_LogsRawPayloadOnInvalidJSON(t *testing.T) {
	srv := newTestServerWithOpts([]ResolvedKey{{Name: "t", Key: "k"}}, WithGrafana(testGrafanaConfig(t)))

	var code int
	logs := captureTrace(t, 3, func() {
		w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader("{bad"), grafanaHeaders())
		code = w.Code
	})

	if code != 400 {
		t.Fatalf("expected 400 for invalid JSON, got %d", code)
	}
	if !strings.Contains(logs, "grafana: raw payload") || !strings.Contains(logs, "{bad") {
		t.Errorf("invalid JSON was not traced:\n%s", logs)
	}
}

func TestGrafana_NoRawPayloadBelowV3(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))

	logs := captureTrace(t, 2, func() {
		doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(grafanaPayloadWithMessage("t", "m")), grafanaHeaders())
	})

	if strings.Contains(logs, "raw payload") {
		t.Errorf("raw payload logged below V3:\n%s", logs)
	}
}

func TestGrafana_TruncatesOversizedRawPayload(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))
	tail := "TAIL-MARKER"
	body := grafanaPayloadWithMessage("big", strings.Repeat("x", maxRawBodyLogBytes)+tail)

	var code int
	logs := captureTrace(t, 3, func() {
		w := doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(body), grafanaHeaders())
		code = w.Code
	})

	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	record := traceRecord(t, logs, "grafana: raw payload")
	if !strings.Contains(record.header, "truncated to 65536 of ") {
		t.Errorf("oversized payload was not marked truncated: %q", record.header)
	}
	if got := len(record.data); got != maxRawBodyLogBytes {
		t.Errorf("logged %d bytes of body, want %d", got, maxRawBodyLogBytes)
	}
	if strings.Contains(record.data, tail) {
		t.Error("truncated log still contains the end of the body")
	}

	rendered := traceRecord(t, logs, "grafana: rendered message")
	if !strings.Contains(rendered.header, "truncated to 65536 of ") {
		t.Errorf("oversized rendered message was not marked truncated: %q", rendered.header)
	}
	if strings.Contains(logs, tail) {
		t.Error("the end of an oversized payload reached the log through some record")
	}
	if !strings.Contains(captured, tail) {
		t.Error("truncating the log must not truncate the delivered message")
	}
}

func TestAlertmanager_LogsRawPayloadAtV3(t *testing.T) {
	srv := newTestServerWithOpts([]ResolvedKey{{Name: "t", Key: "k"}}, WithAlertmanager(testAlertmanagerConfig(t)))
	body := alertmanagerPayload("firing", AlertItem{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})

	var code int
	logs := captureTrace(t, 3, func() {
		w := doRequest(srv, "POST", "/api/v1/alertmanager", strings.NewReader(body), grafanaHeaders())
		code = w.Code
	})

	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(logs, "alertmanager: raw payload") || !strings.Contains(logs, body) {
		t.Errorf("raw payload not traced:\n%s", logs)
	}
}

func TestGitlab_LogsRawPayloadAtV3(t *testing.T) {
	srv, cap := newGitlabTestServer(t, gitlabTestConfig("secret", "chat-a"))

	var code int
	logs := captureTrace(t, 3, func() {
		w := doRequest(srv, "POST", "/api/v1/gitlab", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		code = w.Code
	})

	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	if !strings.Contains(logs, "gitlab: raw payload") || !strings.Contains(logs, `"object_kind": "merge_request"`) {
		t.Errorf("raw payload not traced:\n%s", logs)
	}
	if strings.Contains(logs, "X-Gitlab-Token") || strings.Contains(logs, "secret") {
		t.Errorf("trace log leaks the gitlab token:\n%s", logs)
	}
	if cap.count() != 1 {
		t.Errorf("expected 1 delivery after reading the body, got %d", cap.count())
	}
}

// --- per-request trace marker ---

func TestTracePayloadRequested(t *testing.T) {
	tests := []struct {
		name   string
		target string
		header string
		want   bool
	}{
		{name: "no marker", target: "/x", want: false},
		{name: "bare param", target: "/x?trace", want: true},
		{name: "empty param", target: "/x?trace=", want: true},
		{name: "param 1", target: "/x?trace=1", want: true},
		{name: "param true", target: "/x?trace=TRUE", want: true},
		{name: "param yes", target: "/x?trace=yes", want: true},
		{name: "param 0", target: "/x?trace=0", want: false},
		{name: "param false", target: "/x?trace=false", want: false},
		{name: "unknown value", target: "/x?trace=maybe", want: false},
		{name: "header 1", target: "/x", header: "1", want: true},
		{name: "header empty", target: "/x", header: "", want: false},
		{name: "header no", target: "/x", header: "no", want: false},
		{name: "param wins over header", target: "/x?trace=0", header: "1", want: false},
		{name: "param on with header off", target: "/x?trace=1", header: "0", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tt.target, nil)
			if tt.header != "" {
				r.Header.Set(tracePayloadHeader, tt.header)
			}
			if got := tracePayloadRequested(r); got != tt.want {
				t.Errorf("tracePayloadRequested(%q, header %q) = %v, want %v", tt.target, tt.header, got, tt.want)
			}
		})
	}
}

func TestGrafana_TraceParamLogsAtDefaultVerbosity(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))
	body := grafanaPayloadWithMessage("Disk almost full", "node-1 at 94%")

	var code int
	logs := captureTrace(t, 0, func() {
		w := doRequest(srv, "POST", "/api/v1/grafana?trace=1", strings.NewReader(body), grafanaHeaders())
		code = w.Code
	})

	if code != 200 {
		t.Fatalf("expected 200, got %d", code)
	}
	record := traceRecord(t, logs, "grafana: raw payload")
	if !strings.Contains(record.header, "request_trace=true") {
		t.Errorf("traced record is not marked: %q", record.header)
	}
	if !strings.Contains(record.header, "id=") {
		t.Errorf("traced record carries no request id for access-log correlation: %q", record.header)
	}
	if record.data != body {
		t.Errorf("logged body does not match the request body:\n%s", record.data)
	}
	if !strings.Contains(logs, "grafana: rendered message") || !strings.Contains(logs, "node-1 at 94%") {
		t.Errorf("rendered message not logged alongside the payload:\n%s", logs)
	}
}

func TestGrafana_TraceHeaderLogsAtDefaultVerbosity(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))
	headers := grafanaHeaders()
	headers[tracePayloadHeader] = "1"

	logs := captureTrace(t, 0, func() {
		doRequest(srv, "POST", "/api/v1/grafana", strings.NewReader(grafanaPayloadWithMessage("t", "m")), headers)
	})

	if !strings.Contains(logs, "grafana: raw payload") {
		t.Errorf("header marker did not force the payload log:\n%s", logs)
	}
}

func TestGrafana_TraceParamOverridesHeader(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))
	headers := grafanaHeaders()
	headers[tracePayloadHeader] = "1"

	logs := captureTrace(t, 0, func() {
		doRequest(srv, "POST", "/api/v1/grafana?trace=0", strings.NewReader(grafanaPayloadWithMessage("t", "m")), headers)
	})

	if strings.Contains(logs, "raw payload") {
		t.Errorf("?trace=0 must suppress the header marker:\n%s", logs)
	}
}

func TestGrafana_TraceFalseKeepsV3Logging(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))

	logs := captureTrace(t, 3, func() {
		doRequest(srv, "POST", "/api/v1/grafana?trace=0", strings.NewReader(grafanaPayloadWithMessage("t", "m")), grafanaHeaders())
	})

	record := traceRecord(t, logs, "grafana: raw payload")
	if strings.Contains(record.header, "request_trace=true") {
		t.Errorf("?trace=0 must only drop the forced mode, not mark the record: %q", record.header)
	}
}

func TestGrafana_TraceAtV3LogsPayloadOnce(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))

	logs := captureTrace(t, 3, func() {
		doRequest(srv, "POST", "/api/v1/grafana?trace=1", strings.NewReader(grafanaPayloadWithMessage("t", "m")), grafanaHeaders())
	})

	if got := strings.Count(logs, "grafana: raw payload"); got != 1 {
		t.Errorf("payload logged %d times at V3 with the marker, want 1:\n%s", got, logs)
	}
	if got := strings.Count(logs, "grafana: rendered message"); got != 1 {
		t.Errorf("rendered message logged %d times at V3 with the marker, want 1:\n%s", got, logs)
	}
}

func TestGrafana_TraceDoesNotLogUnauthorizedBody(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(webhookGrafanaConfig(t)))
	body := grafanaPayloadWithMessage("secret title", "secret message")
	headers := grafanaHeaders()
	headers["X-API-Key"] = "wrong"
	reader := &countingReader{r: strings.NewReader(body)}

	var code int
	logs := captureTrace(t, 0, func() {
		w := doRequest(srv, "POST", "/api/v1/grafana?trace=1", reader, headers)
		code = w.Code
	})

	if code != 403 {
		t.Fatalf("expected 403 for a bad key, got %d", code)
	}
	if strings.Contains(logs, "raw payload") || strings.Contains(logs, "secret message") {
		t.Errorf("body of an unauthenticated request was logged:\n%s", logs)
	}
	if reader.reads != 0 {
		t.Errorf("body of an unauthenticated request was read %d times, want 0", reader.reads)
	}
}

func TestGitlab_TraceDoesNotLogUnauthorizedBody(t *testing.T) {
	srvCfg := Config{Listen: ":0", BasePath: "/api/v1", AllowRequestTrace: true}
	srv, _ := newGitlabTestServerCfg(t, srvCfg, gitlabTestConfig("secret", "chat-a"), nil)

	reader := &countingReader{r: strings.NewReader(mrOpenPayload)}

	var code int
	logs := captureTrace(t, 0, func() {
		w := doRequest(srv, "POST", "/api/v1/gitlab?trace=1", reader, gitlabHeaders("wrong-token"))
		code = w.Code
	})

	if code != 401 {
		t.Fatalf("expected 401 for a bad token, got %d", code)
	}
	if strings.Contains(logs, "raw payload") {
		t.Errorf("body of an unauthenticated request was logged:\n%s", logs)
	}
	if reader.reads != 0 {
		t.Errorf("body of an unauthenticated request was read %d times, want 0", reader.reads)
	}
}

func TestAlertmanager_TraceParamLogsAtDefaultVerbosity(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithAlertmanager(testAlertmanagerConfig(t)))
	body := alertmanagerPayload("firing", AlertItem{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
		Annotations: map[string]string{"summary": "CPU > 90%"},
	})

	logs := captureTrace(t, 0, func() {
		doRequest(srv, "POST", "/api/v1/alertmanager?trace=1", strings.NewReader(body), grafanaHeaders())
	})

	record := traceRecord(t, logs, "alertmanager: raw payload")
	if !strings.Contains(record.header, "request_trace=true") || record.data != body {
		t.Errorf("alertmanager payload not traced on demand:\n%s", logs)
	}
	if !strings.Contains(logs, "alertmanager: rendered message") {
		t.Errorf("rendered message not logged alongside the payload:\n%s", logs)
	}
}

func TestGitlab_TraceParamLogsAtDefaultVerbosity(t *testing.T) {
	srvCfg := Config{Listen: ":0", BasePath: "/api/v1", AllowRequestTrace: true}
	srv, cap := newGitlabTestServerCfg(t, srvCfg, gitlabTestConfig("secret", "chat-a"), nil)

	logs := captureTrace(t, 0, func() {
		doRequest(srv, "POST", "/api/v1/gitlab?trace=1", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
	})

	record := traceRecord(t, logs, "gitlab: raw payload")
	if !strings.Contains(record.header, "request_trace=true") {
		t.Errorf("gitlab record is not marked: %q", record.header)
	}
	if !strings.Contains(logs, mrOpenPayload) {
		t.Errorf("gitlab payload not traced on demand:\n%s", logs)
	}
	if cap.count() != 1 {
		t.Errorf("expected 1 delivery, got %d", cap.count())
	}
}

func TestGrafana_TraceLogsInvalidJSONAtDefaultVerbosity(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, true, &captured, WithGrafana(testGrafanaConfig(t)))

	var code int
	logs := captureTrace(t, 0, func() {
		w := doRequest(srv, "POST", "/api/v1/grafana?trace=1", strings.NewReader("{bad"), grafanaHeaders())
		code = w.Code
	})

	if code != 400 {
		t.Fatalf("expected 400 for invalid JSON, got %d", code)
	}
	if !strings.Contains(logs, "{bad") {
		t.Errorf("invalid JSON was not traced:\n%s", logs)
	}
}

// --- server.allow_request_trace ---

func webhookGrafanaConfig(t *testing.T) *GrafanaConfig {
	t.Helper()
	cfg := testGrafanaConfig(t)
	cfg.MessageSource = GrafanaMessageSourceWebhook
	return cfg
}

func traceGatedServer(t *testing.T, allow bool, captured *string, opts ...Option) *Server {
	t.Helper()
	cfg := Config{
		Listen:            ":0",
		BasePath:          "/api/v1",
		Keys:              []ResolvedKey{{Name: "t", Key: "k"}},
		AllowRequestTrace: allow,
	}
	sendFn := func(ctx context.Context, p *SendPayload) (string, error) {
		*captured = p.Message
		return "test-sync-id", nil
	}
	chatResolver := func(chatID string) (ChatResolveResult, error) {
		return ChatResolveResult{ChatID: chatID}, nil
	}
	return New(cfg, sendFn, chatResolver, opts...)
}

func TestGrafana_MarkerIgnoredWhenTraceNotAllowed(t *testing.T) {
	body := grafanaPayloadWithMessage("Disk almost full", "node-1 at 94%")
	cases := []struct {
		name string
		srv  func(t *testing.T, captured *string) *Server
	}{
		{
			name: "allow_request_trace unset",
			srv: func(t *testing.T, captured *string) *Server {
				return grafanaCapturingServer(t, GrafanaMessageSourceWebhook, captured)
			},
		},
		{
			name: "allow_request_trace false",
			srv: func(t *testing.T, captured *string) *Server {
				return traceGatedServer(t, false, captured, WithGrafana(webhookGrafanaConfig(t)))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured string
			srv := tc.srv(t, &captured)

			var code int
			logs := captureTrace(t, 0, func() {
				w := doRequest(srv, "POST", "/api/v1/grafana?trace=1", strings.NewReader(body), grafanaHeaders())
				code = w.Code
			})

			if strings.Contains(logs, "raw payload") || strings.Contains(logs, "rendered message") {
				t.Errorf("marker was honoured without allow_request_trace:\n%s", logs)
			}
			if code != 200 || captured == "" {
				t.Fatalf("a rejected marker must not break delivery: code=%d message=%q", code, captured)
			}
		})
	}
}

func TestGrafana_DisallowedTraceIgnoresMarker(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, false, &captured, WithGrafana(testGrafanaConfig(t)))
	body := grafanaPayloadWithMessage("Disk almost full", "node-1 at 94%")

	cases := []struct {
		name    string
		target  string
		headers map[string]string
	}{
		{name: "query marker", target: "/api/v1/grafana?trace=1", headers: grafanaHeaders()},
		{name: "header marker", target: "/api/v1/grafana", headers: map[string]string{
			"X-API-Key": "k", "Content-Type": "application/json", tracePayloadHeader: "1",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captured = ""
			var code int
			logs := captureTrace(t, 0, func() {
				w := doRequest(srv, "POST", tc.target, strings.NewReader(body), tc.headers)
				code = w.Code
			})

			if strings.Contains(logs, "raw payload") || strings.Contains(logs, "rendered message") {
				t.Errorf("marker was honoured although allow_request_trace is false:\n%s", logs)
			}
			if code != 200 {
				t.Fatalf("a rejected marker must not break delivery, got %d", code)
			}
			if captured == "" {
				t.Error("message was not delivered")
			}
		})
	}
}

func TestGrafana_DisallowedTraceStillLogsAtV3(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, false, &captured, WithGrafana(testGrafanaConfig(t)))

	logs := captureTrace(t, 3, func() {
		doRequest(srv, "POST", "/api/v1/grafana?trace=1", strings.NewReader(grafanaPayloadWithMessage("t", "m")), grafanaHeaders())
	})

	record := traceRecord(t, logs, "grafana: raw payload")
	if strings.Contains(record.header, "request_trace=true") {
		t.Errorf("a rejected marker must not be reported as a forced trace: %q", record.header)
	}
	if !strings.Contains(logs, "grafana: rendered message") {
		t.Errorf("-vvv must keep logging both records:\n%s", logs)
	}
}

func TestAlertmanager_DisallowedTraceIgnoresMarker(t *testing.T) {
	var captured string
	srv := traceGatedServer(t, false, &captured, WithAlertmanager(testAlertmanagerConfig(t)))
	body := alertmanagerPayload("firing", AlertItem{
		Status: "firing",
		Labels: map[string]string{"alertname": "HighCPU", "severity": "critical"},
	})

	var code int
	logs := captureTrace(t, 0, func() {
		w := doRequest(srv, "POST", "/api/v1/alertmanager?trace=1", strings.NewReader(body), grafanaHeaders())
		code = w.Code
	})

	if strings.Contains(logs, "raw payload") || strings.Contains(logs, "rendered message") {
		t.Errorf("marker was honoured although allow_request_trace is false:\n%s", logs)
	}
	if code != 200 {
		t.Fatalf("a rejected marker must not break delivery, got %d", code)
	}
}

func TestGitlab_DisallowedTraceIgnoresMarker(t *testing.T) {
	srvCfg := Config{Listen: ":0", BasePath: "/api/v1", AllowRequestTrace: false}
	srv, cap := newGitlabTestServerCfg(t, srvCfg, gitlabTestConfig("secret", "chat-a"), nil)

	var code int
	logs := captureTrace(t, 0, func() {
		w := doRequest(srv, "POST", "/api/v1/gitlab?trace=1", strings.NewReader(mrOpenPayload), gitlabHeaders("secret"))
		code = w.Code
	})

	if strings.Contains(logs, "raw payload") || strings.Contains(logs, "rendered message") {
		t.Errorf("marker was honoured although allow_request_trace is false:\n%s", logs)
	}
	if code != 200 || cap.count() != 1 {
		t.Fatalf("a rejected marker must not break delivery: code=%d deliveries=%d", code, cap.count())
	}
}

type loggedRecord struct {
	header string
	data   string
}

// traceRecord splits the two-line payload/message record that starts with the
// given prefix: the bracketed header line and the body line that follows it.
func traceRecord(t *testing.T, logs, prefix string) loggedRecord {
	t.Helper()
	idx := strings.Index(logs, prefix)
	if idx < 0 {
		t.Fatalf("no %q record in log:\n%.400s", prefix, logs)
	}
	lines := strings.SplitN(logs[idx:], "\n", 3)
	if len(lines) < 2 {
		t.Fatalf("record %q has no body line: %.400s", prefix, logs[idx:])
	}
	return loggedRecord{header: lines[0], data: lines[1]}
}
