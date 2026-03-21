package server

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ExecHandler runs an external command to handle callback events.
// The callback payload JSON is passed via stdin.
type ExecHandler struct {
	command string
	timeout time.Duration
}

// NewExecHandler creates an ExecHandler that runs the given command
// with the specified timeout. A zero timeout means no deadline.
func NewExecHandler(command string, timeout time.Duration) *ExecHandler {
	return &ExecHandler{
		command: command,
		timeout: timeout,
	}
}

// Type returns "exec".
func (h *ExecHandler) Type() string {
	return "exec"
}

// Handle runs the configured command, passing payload as JSON on stdin.
func (h *ExecHandler) Handle(ctx context.Context, event string, payload []byte) error {
	if h.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", h.command)
	cmd.Stdin = bytes.NewReader(payload)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exec handler %q: %w (stderr: %s)", h.command, err, stderr.String())
	}
	return nil
}
