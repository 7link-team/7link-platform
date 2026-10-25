package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

func (m *Module) bindAttentionInteraction(s *module.Services) error {
	connector, err := module.Resolve[delivery.AttentionInteractionConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return connector.Bind(m.attentionInteractionOn)
}
func (m *Module) attentionInteractionOn(ctx context.Context, tx *gorm.DB, tenant, project, work, id guid.ID) (err error) {
	defer func() {
		switch {
		case errors.Is(err, ErrForbidden):
			err = delivery.ErrWorkForbidden
		case errors.Is(err, ErrUnavailable), errors.Is(err, ErrCapabilityUnavailable):
			err = delivery.ErrUnavailable
		case errors.Is(err, ErrNotFound), errors.Is(err, gorm.ErrRecordNotFound):
			err = delivery.ErrAttentionClosed
		}
	}()
	if err := m.humanAuthority(ctx, tx, tenant, project, "interaction_respond"); err != nil {
		return err
	}
	q := query.BindApprovals(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	run, err := m.rowOn(ctx, tx, tenant, row.ExecutionID, true)
	if err != nil {
		return err
	}
	row, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ID.Eq(id), q.ExecutionID.Eq(run.ID)).Take()
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	if run.ProjectID != project || run.WorkItemID != work || run.State != "RUNNING" || run.CancelRequestedAt != nil || row.State != "PENDING" || !row.ExpiresAt.After(at) || run.DispatchEpoch != row.DispatchEpoch || run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(at) {
		return delivery.ErrAttentionClosed
	}
	if m.work.AuthorizeOn == nil {
		return ErrUnavailable
	}
	return m.work.AuthorizeOn(ctx, tx, link(run, authn.PrincipalIDFromContext(ctx)), "RECEIPT")
}
