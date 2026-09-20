package botapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildEditRequest(t *testing.T) {
	tests := []struct {
		name     string
		params   EditParams
		wantBody string
	}{
		{
			name:     "body passes through uncapped",
			params:   EditParams{SyncID: "sync-1", Message: "resolved"},
			wantBody: "resolved",
		},
		{
			name:     "body is capped exactly as a send is",
			params:   EditParams{SyncID: "sync-1", Message: "0123456789", MaxMessageLength: 6, TruncateSuffix: "..."},
			wantBody: "012...",
		},
		{
			name:     "a body inside the cap is untouched",
			params:   EditParams{SyncID: "sync-1", Message: "short", MaxMessageLength: 100, TruncateSuffix: "..."},
			wantBody: "short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er := BuildEditRequest(&tt.params)
			if er.SyncID != tt.params.SyncID {
				t.Errorf("SyncID = %q, want %q", er.SyncID, tt.params.SyncID)
			}
			if er.Payload.Body != tt.wantBody {
				t.Errorf("Body = %q, want %q", er.Payload.Body, tt.wantBody)
			}
		})
	}
}

func TestEditRequestWireFormat(t *testing.T) {
	encoded, err := json.Marshal(BuildEditRequest(&EditParams{SyncID: "sync-1", Message: "resolved"}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"sync_id":"sync-1","payload":{"body":"resolved"}}`
	if string(encoded) != want {
		t.Errorf("wire format = %s, want %s", encoded, want)
	}
}

func TestEditMessage(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    string
	}{
		{name: "200 succeeds", statusCode: 200, body: `{"status":"ok"}`},
		{name: "202 succeeds", statusCode: 202, body: `{"status":"ok"}`},
		{name: "401 is reported as unauthorized", statusCode: 401, body: ``, wantErr: "unauthorized"},
		{name: "404 carries the upstream body", statusCode: 404, body: `{"reason":"message_not_found"}`, wantErr: "message_not_found"},
		{name: "500 is an error", statusCode: 500, body: `boom`, wantErr: "HTTP 500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotAuth, gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				w.WriteHeader(tt.statusCode)
				io.WriteString(w, tt.body) //nolint:errcheck
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "tok", 5*time.Second)
			err := c.EditMessage(context.Background(), BuildEditRequest(&EditParams{SyncID: "sync-1", Message: "resolved"}))

			if gotPath != "/api/v3/botx/events/edit_event" {
				t.Errorf("path = %q, want %q", gotPath, "/api/v3/botx/events/edit_event")
			}
			if gotAuth != "Bearer tok" {
				t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok")
			}
			if !strings.Contains(gotBody, `"sync_id":"sync-1"`) {
				t.Errorf("request body = %s, want it to carry sync_id", gotBody)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("EditMessage() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("EditMessage() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("EditMessage() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}
