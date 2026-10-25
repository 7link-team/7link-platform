package yunxiao

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"gorm.io/gorm"
)

// IngressFailureFacts reads persisted retry exhaustion. Event payloads are only
// wakeups; they cannot assert that processing exhausted its retry budget.
type IngressFailureFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, bool, error)

func (m *Module) ingressFailureOn(ctx context.Context, tx *gorm.DB, tenant, event guid.ID) (guid.ID, bool, error) {
	if tx == nil || tenant <= 0 || event <= 0 {
		return 0, false, ErrInvalid
	}
	q := query.BindInboundEvents(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(event)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return r.ConnectionID, r.State == "FAILED" && r.Error != nil && *r.Error == "RETRIES_EXHAUSTED", nil
}
