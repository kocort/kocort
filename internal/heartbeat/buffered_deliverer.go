package heartbeat

import (
	"context"
	"sync"

	"github.com/kocort/kocort/internal/core"
)

type BufferedDeliverer struct {
	mu      sync.Mutex
	records []core.DeliveryRecord
}

func (d *BufferedDeliverer) Deliver(_ context.Context, kind core.ReplyKind, payload core.ReplyPayload, target core.DeliveryTarget) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, core.DeliveryRecord{
		Kind:    kind,
		Payload: payload,
		Target:  target,
	})
	return nil
}

func (d *BufferedDeliverer) RecordsSnapshot() []core.DeliveryRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]core.DeliveryRecord, len(d.records))
	copy(out, d.records)
	return out
}
