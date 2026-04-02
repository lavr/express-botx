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

func TestBuildSendRequest_Mentions(t *testing.T) {
	mentions := json.RawMessage(`[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Ivan"}}]`)
	sr := BuildSendRequest(&SendParams{
		ChatID:   "chat-1",
		Message:  "@{mention:aaa-bbb} hello",
		Status:   "ok",
		Mentions: mentions,
	})
	if sr.Notification == nil {
		t.Fatal("expected Notification")
	}
	if string(sr.Notification.Mentions) != string(mentions) {
		t.Errorf("Mentions = %s, want %s", sr.Notification.Mentions, mentions)
	}
}

func TestBuildSendRequest_FileMetadataMentions(t *testing.T) {
	meta := json.RawMessage(`{"key":"val"}`)
	mentions := json.RawMessage(`[{"mention_id":"aaa","mention_type":"all","mention_data":null}]`)
	sr := BuildSendRequest(&SendParams{
		ChatID:   "chat-1",
		Status:   "ok",
		File:     &SendFile{FileName: "f.txt", Data: "data:;base64,"},
		Metadata: meta,
		Mentions: mentions,
	})
	if sr.Notification == nil {
		t.Fatal("expected Notification for file + metadata + mentions")
	}
	if sr.Notification.Body != "" {
		t.Errorf("Body = %q, want empty", sr.Notification.Body)
	}
	if string(sr.Notification.Metadata) != string(meta) {
		t.Errorf("Metadata = %s, want %s", sr.Notification.Metadata, meta)
	}
	if string(sr.Notification.Mentions) != string(mentions) {
		t.Errorf("Mentions = %s, want %s", sr.Notification.Mentions, mentions)
	}
}

func TestBuildSendRequest_MentionsInJSON(t *testing.T) {
	mentions := json.RawMessage(`[{"mention_id":"aaa-bbb","mention_type":"user","mention_data":{"user_huid":"xxx","name":"Ivan"}}]`)
	sr := BuildSendRequest(&SendParams{
		ChatID:   "chat-1",
		Message:  "@{mention:aaa-bbb} hello",
		Status:   "ok",
		Mentions: mentions,
	})

	data, err := json.Marshal(sr)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	var notif map[string]json.RawMessage
	if err := json.Unmarshal(raw["notification"], &notif); err != nil {
		t.Fatalf("json.Unmarshal notification: %v", err)
	}

	if _, ok := notif["mentions"]; !ok {
		t.Fatal("expected mentions key in serialized notification JSON")
	}

	// Verify the mentions value round-trips correctly
	var got []map[string]interface{}
	if err := json.Unmarshal(notif["mentions"], &got); err != nil {
		t.Fatalf("json.Unmarshal mentions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 mention, got %d", len(got))
	}
	if got[0]["mention_id"] != "aaa-bbb" {
		t.Errorf("mention_id = %v, want aaa-bbb", got[0]["mention_id"])
	}
	if got[0]["mention_type"] != "user" {
		t.Errorf("mention_type = %v, want user", got[0]["mention_type"])
	}
}

func TestBuildSendRequest_EmptyMentionsArray(t *testing.T) {
	// Semantic decision: empty array [] is passed through as-is (not omitted).
	// json.RawMessage(`[]`) has len > 0, so omitempty does not drop it.
	// This allows callers to explicitly send an empty mentions array if needed.
	sr := BuildSendRequest(&SendParams{
		ChatID:   "chat-1",
		Message:  "hello",
		Status:   "ok",
		Mentions: json.RawMessage(`[]`),
	})

	if sr.Notification == nil {
		t.Fatal("expected Notification")
	}
	if string(sr.Notification.Mentions) != "[]" {
		t.Errorf("Mentions = %s, want []", sr.Notification.Mentions)
	}

	// Verify it appears in the serialized JSON
	data, err := json.Marshal(sr)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	var notif map[string]json.RawMessage
	if err := json.Unmarshal(raw["notification"], &notif); err != nil {
		t.Fatalf("json.Unmarshal notification: %v", err)
	}

	if _, ok := notif["mentions"]; !ok {
		t.Fatal("expected mentions key in JSON even for empty array")
	}
	if string(notif["mentions"]) != "[]" {
		t.Errorf("serialized mentions = %s, want []", notif["mentions"])
	}
}

func TestBuildSendRequest_FileMetadataMentionsSilent(t *testing.T) {
	meta := json.RawMessage(`{"key":"val"}`)
	mentions := json.RawMessage(`[{"mention_id":"aaa","mention_type":"all","mention_data":null}]`)
	sr := BuildSendRequest(&SendParams{
		ChatID:   "chat-1",
		Status:   "ok",
		File:     &SendFile{FileName: "f.txt", Data: "data:;base64,"},
		Metadata: meta,
		Mentions: mentions,
		Silent:   true,
	})
	if sr.Notification == nil {
		t.Fatal("expected Notification for file + metadata + mentions + silent")
	}
	if sr.Notification.Opts == nil || !sr.Notification.Opts.SilentResponse {
		t.Error("expected SilentResponse=true in fallback notification")
	}
	if string(sr.Notification.Metadata) != string(meta) {
		t.Errorf("Metadata = %s, want %s", sr.Notification.Metadata, meta)
	}
	if string(sr.Notification.Mentions) != string(mentions) {
		t.Errorf("Mentions = %s, want %s", sr.Notification.Mentions, mentions)
	}
}

func TestBuildSendRequest_NilMentionsOmitted(t *testing.T) {
	// When mentions is nil, it should be omitted from the serialized JSON.
	sr := BuildSendRequest(&SendParams{
		ChatID:  "chat-1",
		Message: "hello",
		Status:  "ok",
	})

	data, err := json.Marshal(sr)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	var notif map[string]json.RawMessage
	if err := json.Unmarshal(raw["notification"], &notif); err != nil {
		t.Fatalf("json.Unmarshal notification: %v", err)
	}

	if _, ok := notif["mentions"]; ok {
		t.Errorf("expected mentions to be omitted from JSON when nil, got %s", notif["mentions"])
	}
}
