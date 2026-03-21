package server

import (
	"encoding/json"
	"testing"
)

func TestParseEventType(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{"system:chat_created", EventChatCreated},
		{"system:added_to_chat", EventAddedToChat},
		{"system:user_joined_to_chat", EventUserJoinedToChat},
		{"system:deleted_from_chat", EventDeletedFromChat},
		{"system:left_from_chat", EventLeftFromChat},
		{"system:chat_deleted_by_user", EventChatDeletedByUser},
		{"system:cts_login", EventCTSLogin},
		{"system:cts_logout", EventCTSLogout},
		{"system:event_edit", EventEdit},
		{"system:smartapp_event", EventSmartAppEvent},
		{"system:internal_bot_notification", EventInternalBotNotification},
		{"system:conference_created", EventConferenceCreated},
		{"system:conference_deleted", EventConferenceDeleted},
		{"system:call_started", EventCallStarted},
		{"system:call_ended", EventCallEnded},
		// Unknown system event — strip prefix.
		{"system:future_event", "future_event"},
		// Regular text → message.
		{"hello world", EventMessage},
		{"", EventMessage},
		{"/start", EventMessage},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			got := parseEventType(tt.body)
			if got != tt.want {
				t.Errorf("parseEventType(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestCallbackPayload_JSON(t *testing.T) {
	raw := `{
		"sync_id": "abc-123",
		"command": {
			"body": "system:chat_created",
			"command_type": "system"
		},
		"from": {
			"user_huid": "user-456",
			"group_chat_id": "chat-789",
			"host": "express.example.com"
		},
		"bot_id": "bot-000",
		"proto_version": 4
	}`

	var p CallbackPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if p.SyncID != "abc-123" {
		t.Errorf("SyncID = %q, want %q", p.SyncID, "abc-123")
	}
	if p.Command.Body != "system:chat_created" {
		t.Errorf("Command.Body = %q, want %q", p.Command.Body, "system:chat_created")
	}
	if p.From.UserHUID != "user-456" {
		t.Errorf("From.UserHUID = %q, want %q", p.From.UserHUID, "user-456")
	}
	if p.From.GroupChatID != "chat-789" {
		t.Errorf("From.GroupChatID = %q, want %q", p.From.GroupChatID, "chat-789")
	}
	if p.BotID != "bot-000" {
		t.Errorf("BotID = %q, want %q", p.BotID, "bot-000")
	}
	if p.ProtoVersion != 4 {
		t.Errorf("ProtoVersion = %d, want %d", p.ProtoVersion, 4)
	}
}

func TestNotificationCallbackPayload_JSON(t *testing.T) {
	raw := `{
		"sync_id": "notif-123",
		"status": "ok",
		"result": {"delivered": true},
		"reason": "",
		"errors": []
	}`

	var p NotificationCallbackPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if p.SyncID != "notif-123" {
		t.Errorf("SyncID = %q, want %q", p.SyncID, "notif-123")
	}
	if p.Status != "ok" {
		t.Errorf("Status = %q, want %q", p.Status, "ok")
	}
}
