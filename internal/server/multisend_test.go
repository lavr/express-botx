package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParseChatIDs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"single", "a", []string{"a"}},
		{"multi", "a,b,c", []string{"a", "b", "c"}},
		{"spaces", "a , b", []string{"a", "b"}},
		{"dups", "a,a,b", []string{"a", "b"}},
		{"trailing comma", "a,", []string{"a"}},
		{"empty", "", []string{}},
		{"only commas", ",,", []string{}},
		{"whitespace only", "   ", []string{}},
		{"mixed spaces and dups", " a , b ,a, c ,b ", []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseChatIDs(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseChatIDs(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestFanout(t *testing.T) {
	targets := []string{"a", "b", "c"}
	deliver := func(_ context.Context, chat string) (SendResult, error) {
		if chat == "b" {
			return SendResult{}, errors.New("resolving chat: boom")
		}
		return SendResult{Chat: chat, SyncID: "sync-" + chat}, nil
	}
	results, errs := fanout(context.Background(), targets, deliver)

	wantResults := []SendResult{
		{Chat: "a", SyncID: "sync-a"},
		{Chat: "c", SyncID: "sync-c"},
	}
	if !reflect.DeepEqual(results, wantResults) {
		t.Fatalf("results = %#v, want %#v", results, wantResults)
	}
	wantErrs := []SendError{{Chat: "b", Error: "resolving chat: boom"}}
	if !reflect.DeepEqual(errs, wantErrs) {
		t.Fatalf("errs = %#v, want %#v", errs, wantErrs)
	}
}

func TestWriteMultiSend(t *testing.T) {
	tests := []struct {
		name          string
		results       []SendResult
		errs          []SendError
		successStatus int
		wantStatus    int
		wantOK        bool
	}{
		{
			name:          "all success sync",
			results:       []SendResult{{Chat: "a", SyncID: "s1"}, {Chat: "b", SyncID: "s2"}},
			successStatus: http.StatusOK,
			wantStatus:    http.StatusOK,
			wantOK:        true,
		},
		{
			name:          "partial failure",
			results:       []SendResult{{Chat: "a", SyncID: "s1"}},
			errs:          []SendError{{Chat: "b", Error: "boom"}},
			successStatus: http.StatusOK,
			wantStatus:    http.StatusOK,
			wantOK:        true,
		},
		{
			name:          "all failed",
			errs:          []SendError{{Chat: "a", Error: "boom"}, {Chat: "b", Error: "boom2"}},
			successStatus: http.StatusOK,
			wantStatus:    http.StatusBadGateway,
			wantOK:        false,
		},
		{
			name:          "async success 202",
			results:       []SendResult{{Chat: "a", RequestID: "r1", Queued: true}},
			successStatus: http.StatusAccepted,
			wantStatus:    http.StatusAccepted,
			wantOK:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeMultiSend(rec, tt.results, tt.errs, tt.successStatus)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
			var got MultiSendResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if got.OK != tt.wantOK {
				t.Fatalf("ok = %v, want %v", got.OK, tt.wantOK)
			}
			if !reflect.DeepEqual(got.Results, tt.results) && !(len(got.Results) == 0 && len(tt.results) == 0) {
				t.Fatalf("results = %#v, want %#v", got.Results, tt.results)
			}
			if !reflect.DeepEqual(got.Errors, tt.errs) && !(len(got.Errors) == 0 && len(tt.errs) == 0) {
				t.Fatalf("errors = %#v, want %#v", got.Errors, tt.errs)
			}
		})
	}
}
