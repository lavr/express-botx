package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	vlog "github.com/lavr/express-botx/internal/log"
)

type EditRequest struct {
	SyncID  string      `json:"sync_id"`
	Payload EditPayload `json:"payload"`
}

type EditPayload struct {
	Body string `json:"body"`
}

type EditParams struct {
	SyncID           string
	Message          string
	MaxMessageLength int
	TruncateSuffix   string
}

func BuildEditRequest(p *EditParams) *EditRequest {
	body, truncated := TruncateMessage(p.Message, p.TruncateSuffix, p.MaxMessageLength)
	if truncated {
		vlog.V1("edit: message truncated to %d runes", p.MaxMessageLength)
	}
	return &EditRequest{
		SyncID:  p.SyncID,
		Payload: EditPayload{Body: body},
	}
}

func (c *Client) EditMessage(ctx context.Context, er *EditRequest) error {
	body, err := json.Marshal(er)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	url := c.BaseURL + "/api/v3/botx/events/edit_event"
	vlog.V2("edit: POST %s", url)
	vlog.V2("edit: -> Authorization: Bearer %s", vlog.MaskBearer(c.Token))
	vlog.V3("edit: -> %s", string(body))

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		vlog.V1("edit: <- transport error (%dms): %v", time.Since(start).Milliseconds(), err)
		return fmt.Errorf("editing: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	elapsed := time.Since(start)

	if resp.StatusCode == http.StatusUnauthorized {
		vlog.V1("edit: <- 401 Unauthorized (%dms)", elapsed.Milliseconds())
		return ErrUnauthorized
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		vlog.V1("edit: <- %d %s (%dms)", resp.StatusCode, http.StatusText(resp.StatusCode), elapsed.Milliseconds())
		return nil
	default:
		respBody, _ := io.ReadAll(resp.Body)
		logBody, truncated := truncateErrorBody(respBody)
		vlog.V1("edit: <- %d (%dms): %s", resp.StatusCode, elapsed.Milliseconds(), logBody)
		if truncated {
			vlog.V3("edit: <- %s", string(respBody))
		}
		return fmt.Errorf("edit failed: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
}
