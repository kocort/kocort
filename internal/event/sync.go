package event

import (
	"github.com/kocort/kocort/internal/core"
	"github.com/kocort/kocort/internal/delivery"
	"github.com/kocort/kocort/internal/infra"
)

// SyncDelivererHooks syncs the outbound hook runner and audit log to the
// deliverer when it is a *delivery.RouterDeliverer. It is a no-op otherwise.
func SyncDelivererHooks(deliverer core.Deliverer, hooks delivery.OutboundHookRunner, audit AuditRecorder) {
	if deliverer == nil {
		return
	}
	if router, ok := deliverer.(*delivery.RouterDeliverer); ok {
		router.Hooks = hooks
		if auditLog, ok := audit.(*infra.AuditLog); ok {
			router.Audit = auditLog
		}
	}
}
