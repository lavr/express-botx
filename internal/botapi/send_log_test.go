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
	"unicode/utf8"

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

func TestTruncateErrorBody(t *testing.T) {
	tests := []struct {
		name          string
		body          []byte
		wantTruncated bool
		wantMarker    string
	}{
		{
			name:          "short body is passed through",
			body:          []byte(`{"reason":"nope"}`),
			wantTruncated: false,
		},
		{
			name:          "oversized body is marked",
			body:          bytes.Repeat([]byte("x"), maxErrorBodyLogBytes+100),
			wantTruncated: true,
			wantMarker:    "truncated to",
		},
		{
			// A three-byte rune offset by one ASCII byte: a raw data[:N] cut
			// would land mid-rune, which a two-byte rune on an even boundary
			// could not reveal.
			name:          "cut lands on a rune boundary",
			body:          append([]byte("x"), bytes.Repeat([]byte("→"), maxErrorBodyLogBytes)...),
			wantTruncated: true,
			wantMarker:    "truncated to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := truncateErrorBody(tt.body)
			if truncated != tt.wantTruncated {
				t.Fatalf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if tt.wantMarker != "" && !strings.Contains(got, tt.wantMarker) {
				t.Errorf("missing truncation marker in %q", got[max(0, len(got)-60):])
			}
			if !utf8.ValidString(got) {
				t.Errorf("log line is not valid UTF-8")
			}
		})
	}
}
