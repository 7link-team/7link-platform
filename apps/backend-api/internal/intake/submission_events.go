package intake

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake/query"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SubmissionEvents publishes delivery availability for a canonical source in
// the already authorized package command's transaction.
type SubmissionEvents struct {
	DeliveryPublishedOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
}

func (m *Module) provideSubmissionEvents(s *module.Services) error {
	return module.Provide[SubmissionEvents](s, SubmissionEvents{DeliveryPublishedOn: m.deliveryPublishedOn})
}

func (m *Module) deliveryPublishedOn(ctx context.Context, tx *gorm.DB, tenant, project, id guid.ID) error {
	if err := m.changeSourceAuthority(ctx, tx, tenant, project, "delivery_send"); err != nil {
		return err
	}
	if m.brief.audit == nil || m.brief.events == nil {
		return ErrUnavailable
	}
	q := query.BindRecords(tx, m.names)
	r, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return err
	}
	return m.sourceEvent(ctx, tx, tenant, project, id, "intake.delivery_published", r.Version, map[string]any{}, "IAM:delivery_send")
}
