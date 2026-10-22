package botapi

import (
	"context"

	"github.com/lavr/express-botx/botx"
	vlog "github.com/lavr/express-botx/internal/log"
)

type (
	EditRequest = botx.EditRequest
	EditPayload = botx.EditPayload
)

type EditParams struct {
	SyncID           string
	Message          string
	Status           string
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
		Payload: EditPayload{Body: body, Status: p.Status},
	}
}

func (c *Client) EditMessage(ctx context.Context, er *EditRequest) error {
	return c.api().EditMessage(ctx, er)
}
