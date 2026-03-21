package server

import (
	"context"
	"encoding/json"
	"strings"
)

// CallbackHandler processes a BotX callback event.
type CallbackHandler interface {
	// Type returns the handler type name (e.g. "exec", "webhook").
	Type() string
	// Handle processes a callback event. payload is the raw JSON body.
	Handle(ctx context.Context, event string, payload []byte) error
}

// BotX callback event type constants.
const (
	EventMessage                 = "message"
	EventChatCreated             = "chat_created"
	EventAddedToChat             = "added_to_chat"
	EventUserJoinedToChat        = "user_joined_to_chat"
	EventDeletedFromChat         = "deleted_from_chat"
	EventLeftFromChat            = "left_from_chat"
	EventChatDeletedByUser       = "chat_deleted_by_user"
	EventCTSLogin                = "cts_login"
	EventCTSLogout               = "cts_logout"
	EventEdit                    = "event_edit"
	EventSmartAppEvent           = "smartapp_event"
	EventInternalBotNotification = "internal_bot_notification"
	EventConferenceCreated       = "conference_created"
	EventConferenceDeleted       = "conference_deleted"
	EventCallStarted             = "call_started"
	EventCallEnded               = "call_ended"
	EventNotificationCallback    = "notification_callback"
)

// systemEventMap maps "system:*" command body prefixes to event type strings.
var systemEventMap = map[string]string{
	"system:chat_created":             EventChatCreated,
	"system:added_to_chat":            EventAddedToChat,
	"system:user_joined_to_chat":      EventUserJoinedToChat,
	"system:deleted_from_chat":        EventDeletedFromChat,
	"system:left_from_chat":           EventLeftFromChat,
	"system:chat_deleted_by_user":     EventChatDeletedByUser,
	"system:cts_login":                EventCTSLogin,
	"system:cts_logout":               EventCTSLogout,
	"system:event_edit":               EventEdit,
	"system:smartapp_event":           EventSmartAppEvent,
	"system:internal_bot_notification": EventInternalBotNotification,
	"system:conference_created":       EventConferenceCreated,
	"system:conference_deleted":       EventConferenceDeleted,
	"system:call_started":             EventCallStarted,
	"system:call_ended":               EventCallEnded,
}

// parseEventType determines the event type from the command body string.
// "system:chat_created" → "chat_created", plain text → "message".
func parseEventType(commandBody string) string {
	if strings.HasPrefix(commandBody, "system:") {
		if ev, ok := systemEventMap[commandBody]; ok {
			return ev
		}
		// Unknown system event — strip prefix and return as-is.
		return strings.TrimPrefix(commandBody, "system:")
	}
	return EventMessage
}

// CallbackPayload represents a BotX API v4 POST /command request body.
type CallbackPayload struct {
	SyncID       string                 `json:"sync_id"`
	Command      CallbackCommand        `json:"command"`
	From         CallbackFrom           `json:"from"`
	BotID        string                 `json:"bot_id"`
	ProtoVersion int                    `json:"proto_version,omitempty"`
	Attachments  json.RawMessage        `json:"attachments,omitempty"`
	Async        bool                   `json:"async_files,omitempty"`
	Entities     json.RawMessage        `json:"entities,omitempty"`
}

// CallbackCommand holds the command part of a callback payload.
type CallbackCommand struct {
	Body        string          `json:"body"`
	CommandType string          `json:"command_type,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// CallbackFrom holds sender information in a callback payload.
type CallbackFrom struct {
	UserHUID    string `json:"user_huid,omitempty"`
	GroupChatID string `json:"group_chat_id"`
	ChatType    string `json:"chat_type,omitempty"`
	Host        string `json:"host,omitempty"`
	AdChat      bool   `json:"ad_chat,omitempty"`
	AdGroup     bool   `json:"ad_group,omitempty"`
}

// NotificationCallbackPayload represents a BotX API v4 POST /notification/callback request body.
type NotificationCallbackPayload struct {
	SyncID    string          `json:"sync_id"`
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Errors    json.RawMessage `json:"errors,omitempty"`
	ErrorData json.RawMessage `json:"error_data,omitempty"`
}
