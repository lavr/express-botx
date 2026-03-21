package server

import (
	"context"
	"testing"
	"time"
)

// Verify ExecHandler implements CallbackHandler.
var _ CallbackHandler = (*ExecHandler)(nil)

func TestExecHandler_Type(t *testing.T) {
	h := NewExecHandler("echo hello", 5*time.Second)
	if got := h.Type(); got != "exec" {
		t.Errorf("Type() = %q, want %q", got, "exec")
	}
}

func TestExecHandler_Handle(t *testing.T) {
	h := NewExecHandler("cat > /dev/null", 5*time.Second)
	payload := []byte(`{"sync_id":"test-123","bot_id":"bot-1"}`)

	err := h.Handle(context.Background(), EventMessage, payload)
	if err != nil {
		t.Fatalf("Handle() unexpected error: %v", err)
	}
}

func TestExecHandler_StdinPayload(t *testing.T) {
	// Verify the command receives the payload on stdin by using grep to check content.
	h := NewExecHandler(`grep -q "sync_id"`, 5*time.Second)
	payload := []byte(`{"sync_id":"test-456"}`)

	err := h.Handle(context.Background(), EventMessage, payload)
	if err != nil {
		t.Fatalf("Handle() error: %v; expected payload to be passed on stdin", err)
	}
}

func TestExecHandler_NonZeroExit(t *testing.T) {
	h := NewExecHandler("exit 1", 5*time.Second)

	err := h.Handle(context.Background(), EventMessage, []byte(`{}`))
	if err == nil {
		t.Fatal("Handle() expected error for non-zero exit code")
	}
}

func TestExecHandler_Timeout(t *testing.T) {
	h := NewExecHandler("sleep 10", 100*time.Millisecond)

	err := h.Handle(context.Background(), EventMessage, []byte(`{}`))
	if err == nil {
		t.Fatal("Handle() expected error on timeout")
	}
}

func TestExecHandler_ZeroTimeout(t *testing.T) {
	// Zero timeout means no deadline is applied.
	h := NewExecHandler("echo ok", 0)

	err := h.Handle(context.Background(), EventMessage, []byte(`{}`))
	if err != nil {
		t.Fatalf("Handle() unexpected error: %v", err)
	}
}
