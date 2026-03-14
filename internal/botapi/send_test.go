package botapi

import (
	"encoding/json"
	"testing"
)

func TestResolveBaseURL(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"express.company.ru", "https://express.company.ru"},
		{"https://express.company.ru", "https://express.company.ru"},
		{"https://express.company.ru:8443", "https://express.company.ru:8443"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"http://localhost:8080/", "http://localhost:8080"},
		{"http://127.0.0.1:9999", "http://127.0.0.1:9999"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			got := ResolveBaseURL(tt.host)
			if got != tt.want {
				t.Errorf("ResolveBaseURL(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestBuildSendRequest_TextOnly(t *testing.T) {
	sr := BuildSendRequest(&SendParams{
		ChatID:  "chat-1",
		Message: "hello",
		Status:  "ok",
	})
	if sr.GroupChatID != "chat-1" {
		t.Errorf("GroupChatID = %q, want %q", sr.GroupChatID, "chat-1")
	}
	if sr.Notification == nil {
		t.Fatal("expected Notification")
	}
	if sr.Notification.Body != "hello" {
		t.Errorf("Body = %q, want %q", sr.Notification.Body, "hello")
	}
	if sr.Notification.Status != "ok" {
		t.Errorf("Status = %q, want %q", sr.Notification.Status, "ok")
	}
	if sr.File != nil {
		t.Error("expected nil File")
	}
	if sr.Opts != nil {
		t.Error("expected nil Opts")
	}
}

func TestBuildSendRequest_FileOnly(t *testing.T) {
	sr := BuildSendRequest(&SendParams{
		ChatID: "chat-1",
		Status: "ok",
		File:   &SendFile{FileName: "test.txt", Data: "data:text/plain;base64,aGVsbG8="},
	})
	if sr.Notification != nil {
		t.Error("expected nil Notification for file-only")
	}
	if sr.File == nil {
		t.Fatal("expected File")
	}
	if sr.File.FileName != "test.txt" {
		t.Errorf("FileName = %q, want %q", sr.File.FileName, "test.txt")
	}
}

func TestBuildSendRequest_FileOnlyWithMetadata(t *testing.T) {
	meta := json.RawMessage(`{"key":"val"}`)
	sr := BuildSendRequest(&SendParams{
		ChatID:   "chat-1",
		Status:   "ok",
		File:     &SendFile{FileName: "f.txt", Data: "data:;base64,"},
		Metadata: meta,
	})
	if sr.Notification == nil {
		t.Fatal("expected Notification for metadata even without message")
	}
	if sr.Notification.Body != "" {
		t.Errorf("Body = %q, want empty", sr.Notification.Body)
	}
	if string(sr.Notification.Metadata) != `{"key":"val"}` {
		t.Errorf("Metadata = %s, want {\"key\":\"val\"}", sr.Notification.Metadata)
	}
}

func TestBuildSendRequest_AllOpts(t *testing.T) {
	sr := BuildSendRequest(&SendParams{
		ChatID:   "chat-1",
		Message:  "hi",
		Status:   "error",
		Silent:   true,
		Stealth:  true,
		ForceDND: true,
		NoNotify: true,
	})
	if sr.Notification == nil || !sr.Notification.Opts.SilentResponse {
		t.Error("expected SilentResponse=true")
	}
	if sr.Opts == nil {
		t.Fatal("expected Opts")
	}
	if !sr.Opts.StealthMode {
		t.Error("expected StealthMode=true")
	}
	if sr.Opts.NotificationOpts == nil {
		t.Fatal("expected NotificationOpts")
	}
	if !sr.Opts.NotificationOpts.ForceDND {
		t.Error("expected ForceDND=true")
	}
	if sr.Opts.NotificationOpts.Send == nil || *sr.Opts.NotificationOpts.Send != false {
		t.Error("expected Send=false")
	}
}

func TestBuildSendRequest_NoOpts(t *testing.T) {
	sr := BuildSendRequest(&SendParams{
		ChatID:  "chat-1",
		Message: "hi",
		Status:  "ok",
	})
	if sr.Opts != nil {
		t.Error("expected nil Opts when no delivery options set")
	}
	if sr.Notification.Opts != nil {
		t.Error("expected nil Notification.Opts when silent=false")
	}
}
