package botapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildSendRequest_Truncation(t *testing.T) {
	const id = "aaaaaaaa-1111-2222-3333-444444444444"

	tests := []struct {
		name         string
		params       SendParams
		wantBody     string
		wantMentions string
	}{
		{
			name: "limit zero leaves the body alone",
			params: SendParams{
				ChatID: "c", Status: "ok",
				Message:          strings.Repeat("x", 50),
				MaxMessageLength: 0,
			},
			wantBody: strings.Repeat("x", 50),
		},
		{
			name: "body over the limit is cut with the suffix",
			params: SendParams{
				ChatID: "c", Status: "ok",
				Message:          strings.Repeat("x", 50),
				MaxMessageLength: 10,
				TruncateSuffix:   "...",
			},
			wantBody: strings.Repeat("x", 7) + "...",
		},
		{
			name: "cyrillic is counted in runes",
			params: SendParams{
				ChatID: "c", Status: "ok",
				Message:          strings.Repeat("я", 50),
				MaxMessageLength: 10,
				TruncateSuffix:   "",
			},
			wantBody: strings.Repeat("я", 10),
		},
		{
			name: "an orphaned mention is dropped along with its placeholder",
			params: SendParams{
				ChatID: "c", Status: "ok",
				Message:          strings.Repeat("x", 40) + " @{mention:" + id + "}",
				Mentions:         json.RawMessage(`[{"mention_id":"` + id + `"}]`),
				MaxMessageLength: 20,
				TruncateSuffix:   "",
			},
			wantBody:     strings.Repeat("x", 20),
			wantMentions: "[]",
		},
		{
			name: "mentions survive when nothing is truncated",
			params: SendParams{
				ChatID: "c", Status: "ok",
				Message:          "hi @{mention:" + id + "}",
				Mentions:         json.RawMessage(`[{"mention_id":"` + id + `"}]`),
				MaxMessageLength: 4096,
			},
			wantBody:     "hi @{mention:" + id + "}",
			wantMentions: `[{"mention_id":"` + id + `"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr := BuildSendRequest(&tt.params)
			if sr.Notification == nil {
				t.Fatal("notification is nil")
			}
			if sr.Notification.Body != tt.wantBody {
				t.Errorf("body  = %q\nwant  = %q", sr.Notification.Body, tt.wantBody)
			}
			if tt.wantMentions != "" && string(sr.Notification.Mentions) != tt.wantMentions {
				t.Errorf("mentions = %s, want %s", sr.Notification.Mentions, tt.wantMentions)
			}
		})
	}
}

func TestBuildSendRequest_FileOnlyBodyNotTruncated(t *testing.T) {
	sr := BuildSendRequest(&SendParams{
		ChatID:           "c",
		Status:           "ok",
		File:             &SendFile{FileName: "a.txt", Data: "data:text/plain;base64,aGk="},
		Mentions:         json.RawMessage(`[{"mention_id":"aaaaaaaa-1111-2222-3333-444444444444"}]`),
		MaxMessageLength: 10,
		TruncateSuffix:   "...",
	})
	if sr.Notification == nil {
		t.Fatal("notification is nil")
	}
	if sr.Notification.Body != "" {
		t.Errorf("empty body gained content: %q", sr.Notification.Body)
	}
	if !strings.Contains(string(sr.Notification.Mentions), "aaaaaaaa") {
		t.Errorf("mentions were filtered on a file-only send: %s", sr.Notification.Mentions)
	}
}
