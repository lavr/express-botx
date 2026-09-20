package cmd

import (
	"strings"
	"testing"

	"github.com/lavr/express-botx/internal/queue"
	"github.com/lavr/express-botx/internal/server"
)

func TestBuildSendRequest_HonoursPolicy(t *testing.T) {
	sr := buildSendRequest(&server.SendPayload{
		ChatID:  "c",
		Message: strings.Repeat("x", 50),
		Status:  "ok",
	}, 10, "...")

	if sr.Notification == nil {
		t.Fatal("notification is nil")
	}
	if got := sr.Notification.Body; got != strings.Repeat("x", 7)+"..." {
		t.Fatalf("sync path ignored the cap: %q", got)
	}
}

func TestBuildSendRequestFromWork_HonoursPolicy(t *testing.T) {
	msg := &queue.WorkMessage{}
	msg.Routing.ChatID = "c"
	msg.Payload.Message = strings.Repeat("я", 50)
	msg.Payload.Status = "ok"

	sr := buildSendRequestFromWork(msg, 10, "")

	if sr.Notification == nil {
		t.Fatal("notification is nil")
	}
	if got := sr.Notification.Body; got != strings.Repeat("я", 10) {
		t.Fatalf("async path ignored the cap: %q", got)
	}
}
