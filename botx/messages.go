package botx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

// SendRequest is the body of notifications/direct.
type SendRequest struct {
	GroupChatID  string            `json:"group_chat_id"`
	Notification *SendNotification `json:"notification,omitempty"`
	File         *SendFile         `json:"file,omitempty"`
	Opts         *SendOpts         `json:"opts,omitempty"`
}

// SendNotification is the message part of a SendRequest.
type SendNotification struct {
	// Status is "ok" or "error".
	Status   string               `json:"status"`
	Body     string               `json:"body"`
	Metadata json.RawMessage      `json:"metadata,omitempty"`
	Mentions json.RawMessage      `json:"mentions,omitempty"`
	Opts     *NotificationMsgOpts `json:"opts,omitempty"`
}

// NotificationMsgOpts controls per-message notification behavior.
type NotificationMsgOpts struct {
	SilentResponse bool `json:"silent_response,omitempty"`
}

// SendFile is a file attachment sent inline as a base64 data URI.
type SendFile struct {
	FileName string `json:"file_name"`
	// Data is "data:<mime>;base64,<content>".
	Data string `json:"data"`
}

// SendOpts controls delivery-level options.
type SendOpts struct {
	StealthMode      bool          `json:"stealth_mode,omitempty"`
	NotificationOpts *DeliveryOpts `json:"notification_opts,omitempty"`
}

// DeliveryOpts controls push notification delivery.
type DeliveryOpts struct {
	Send     *bool `json:"send,omitempty"`
	ForceDND bool  `json:"force_dnd,omitempty"`
}

// EditRequest is the body of events/edit_event.
type EditRequest struct {
	SyncID  string      `json:"sync_id"`
	Payload EditPayload `json:"payload"`
}

// EditPayload is the new content of an edited message.
type EditPayload struct {
	Body   string `json:"body"`
	Status string `json:"status,omitempty"`
}

// Send posts a message and returns its sync_id, which EditMessage takes.
// A 2xx response is a success only when its body is JSON with status "ok";
// otherwise Send fails with ErrInvalidResponse, and the message may or may not
// have been delivered, so do not resend it blindly.
func (c *Client) Send(ctx context.Context, sr *SendRequest) (string, error) {
	body, err := json.Marshal(sr)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	vlog.V2("send: POST %s", c.baseURL+"/api/v4/botx/notifications/direct")
	vlog.V2("send: -> Authorization: Bearer %s", c.bearer())
	vlog.V3("send: -> %s", string(body))

	start := time.Now()
	resp, err := c.do(ctx, http.MethodPost, "/api/v4/botx/notifications/direct", "application/json", body)
	if err != nil {
		vlog.V1("send: <- transport error (%dms): %v", time.Since(start).Milliseconds(), err)
		return "", fmt.Errorf("sending: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	respBody, readErr := readResponse("send", resp)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		vlog.V1("send: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return "", unauthorized("send", respBody)
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		vlog.V1("send: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
		if readErr != nil {
			return "", readErr
		}
		var apiResp struct {
			Status string `json:"status"`
			Result struct {
				SyncID string `json:"sync_id"`
			} `json:"result"`
		}
		if err := json.Unmarshal(respBody, &apiResp); err != nil {
			return "", newAPIError("send", resp.StatusCode, respBody, "send failed: HTTP %d with a non-JSON body: %s", resp.StatusCode, truncateBody(respBody))
		}
		if apiResp.Status != "ok" {
			return "", newAPIError("send", resp.StatusCode, respBody, "send failed: HTTP %d with status %q: %s", resp.StatusCode, apiResp.Status, truncateBody(respBody))
		}
		vlog.V3("send: <- sync_id=%s", apiResp.Result.SyncID)
		return apiResp.Result.SyncID, nil
	default:
		logBody, truncated := truncateErrorBody(respBody)
		vlog.V1("send: <- %d (%dms): %s", resp.StatusCode, elapsed.Milliseconds(), logBody)
		if truncated {
			vlog.V3("send: <- %s", string(respBody))
		}
		return "", newAPIError("send", resp.StatusCode, respBody, "send failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
}

// EditMessage replaces the text and status of a message sent earlier.
func (c *Client) EditMessage(ctx context.Context, er *EditRequest) error {
	body, err := json.Marshal(er)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	vlog.V2("edit: POST %s", c.baseURL+"/api/v3/botx/events/edit_event")
	vlog.V2("edit: -> Authorization: Bearer %s", c.bearer())
	vlog.V3("edit: -> %s", string(body))

	start := time.Now()
	resp, err := c.do(ctx, http.MethodPost, "/api/v3/botx/events/edit_event", "application/json", body)
	if err != nil {
		vlog.V1("edit: <- transport error (%dms): %v", time.Since(start).Milliseconds(), err)
		return fmt.Errorf("editing: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	respBody, readErr := readResponse("edit", resp)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		vlog.V1("edit: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return unauthorized("edit", respBody)
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		if readErr != nil {
			vlog.V1("edit: <- %d but the body could not be read (%dms): %v", resp.StatusCode, elapsed.Milliseconds(), readErr.cause)
			return readErr
		}
		var apiResp struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(respBody, &apiResp); err != nil {
			logBody, _ := truncateErrorBody(respBody)
			vlog.V1("edit: <- %d with a non-JSON body (%dms): %s", resp.StatusCode, elapsed.Milliseconds(), logBody)
			return newAPIError("edit", resp.StatusCode, respBody, "edit failed: HTTP %d with a non-JSON body: %s", resp.StatusCode, string(respBody))
		}
		if apiResp.Status != "ok" {
			logBody, _ := truncateErrorBody(respBody)
			vlog.V1("edit: <- %d but status is %q (%dms): %s", resp.StatusCode, apiResp.Status, elapsed.Milliseconds(), logBody)
			return newAPIError("edit", resp.StatusCode, respBody, "edit failed: HTTP %d with status %q: %s", resp.StatusCode, apiResp.Status, string(respBody))
		}
		vlog.V1("edit: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
		return nil
	default:
		logBody, truncated := truncateErrorBody(respBody)
		vlog.V1("edit: <- %d (%dms): %s", resp.StatusCode, elapsed.Milliseconds(), logBody)
		if truncated {
			vlog.V3("edit: <- %s", string(respBody))
		}
		return newAPIError("edit", resp.StatusCode, respBody, "edit failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
}
