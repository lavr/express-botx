package botapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

func captureV1(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prevStderr, prevLevel := os.Stderr, vlog.Level
	os.Stderr, vlog.Level = w, 1
	t.Cleanup(func() { os.Stderr, vlog.Level = prevStderr, prevLevel })
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stderr, vlog.Level = prevStderr, prevLevel
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func TestSendWithSyncID_LogsTransportFailure(t *testing.T) {
	// A closed port: the request never reaches an HTTP response, which is the
	// case that currently vanishes from the log entirely.
	c := &Client{BaseURL: "http://127.0.0.1:1", Token: "t", HTTPClient: &http.Client{Timeout: 2 * time.Second}}

	var sendErr error
	logs := captureV1(t, func() {
		_, sendErr = c.SendWithSyncID(context.Background(), &SendRequest{
			GroupChatID:  "c1",
			Notification: &SendNotification{Status: "ok", Body: "hi"},
		})
	})

	if sendErr == nil {
		t.Fatal("expected a transport error")
	}
	if !strings.Contains(logs, "send:") {
		t.Fatalf("transport failure was not logged at V1; logs = %q", logs)
	}
	if !strings.Contains(logs, "connection refused") && !strings.Contains(logs, "connect:") {
		t.Errorf("log does not carry the transport cause; logs = %q", logs)
	}
}

func TestSendWithSyncID_LogsUpstreamErrorBodyAtV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"reason":"body too long"}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "t", HTTPClient: srv.Client()}

	logs := captureV1(t, func() {
		_, _ = c.SendWithSyncID(context.Background(), &SendRequest{
			GroupChatID:  "c1",
			Notification: &SendNotification{Status: "ok", Body: "hi"},
		})
	})

	if !strings.Contains(logs, "body too long") {
		t.Fatalf("upstream error text is not visible at V1; logs = %q", logs)
	}
}

func TestSendWithSyncID_KeepsLegacySuccessOn2xx(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{name: "status error still counts as sent", body: `{"status":"error","reason":"x","result":{"sync_id":"s-1"}}`, want: "s-1"},
		{name: "non-JSON body counts as sent", body: `<html>ok</html>`, want: ""},
		{name: "status ok", body: `{"status":"ok","result":{"sync_id":"s-2"}}`, want: "s-2"},
		{name: "large body without status keeps sync_id", body: `{"result":{"sync_id":"s-3"},"padding":"` + strings.Repeat("x", 5000) + `"}`, want: "s-3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			c := &Client{BaseURL: srv.URL, Token: "t", HTTPClient: srv.Client()}
			got, err := c.SendWithSyncID(context.Background(), &SendRequest{GroupChatID: "c1"})
			if err != nil || got != tt.want {
				t.Errorf("SendWithSyncID() = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestSendWithSyncID_OtherSuccessCodesStayErrors(t *testing.T) {
	for _, code := range []int{http.StatusNoContent, http.StatusPartialContent, http.StatusMultiStatus} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			c := &Client{BaseURL: srv.URL, Token: "t", HTTPClient: srv.Client()}
			if _, err := c.SendWithSyncID(context.Background(), &SendRequest{GroupChatID: "c1"}); err == nil {
				t.Errorf("HTTP %d: want an error", code)
			}
		})
	}
}
