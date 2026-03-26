package runtime

import (
	"context"

	"github.com/kocort/kocort/internal/core"
)

func (r *Runtime) DeliverMessage(ctx context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error {
	if r == nil || r.Deliverer == nil {
		return nil
	}
	return r.Deliverer.Deliver(ctx, kind, payload, target)
}
