package delivery

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// AgentPerformanceLoad counts actual live EXECUTOR slots, including suspended
// NULL leases, independently of accountable responsibility or historical work.
type AgentPerformanceLoad struct {
	CountOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (int64, error)
}

func (m *Module) provideAgentPerformanceLoad(s *module.Services) error {
	return module.Provide(s, AgentPerformanceLoad{CountOn: m.agentPerformanceLoadOn})
}

func (m *Module) agentPerformanceLoadOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (int64, error) {
	if err := retirementTransaction(tx, tenant, agent); err != nil {
		return 0, err
	}
	if authn.TenantIDFromContext(ctx) != tenant {
		return 0, ErrWorkForbidden
	}
	if m == nil || m.service == nil {
		return 0, ErrUnavailable
	}
	a, w := query.BindAssignments(tx, m.service.names), query.BindWorkItems(tx, m.service.names)
	rows, err := a.WithContext(ctx).Select(a.ProjectID, a.WorkItemID).
		Join(w, a.TenantID.EqCol(w.TenantID), a.ProjectID.EqCol(w.ProjectID), a.WorkItemID.EqCol(w.ID)).
		Where(a.TenantID.Eq(tenant), a.AssigneePrincipalID.Eq(agent), a.Role.Eq("EXECUTOR"), a.Status.Eq("ACTIVE"), a.EndedAt.IsNull(), a.ActiveSlot.EqCol(w.ID), w.ArchivedAt.IsNull()).
		Where(a.WithContext(ctx).Where(a.LeaseExpiresAt.IsNull()).Or(a.LeaseExpiresAt.Gt(time.Now().UTC()))).
		Order(a.ID).Limit(10001).Find()
	if err != nil {
		return 0, err
	}
	if len(rows) > 10000 {
		return 0, ErrUnavailable
	}
	var count int64
	for _, row := range rows {
		_, err = m.readWork(ctx, tx, tenant, row.ProjectID, row.WorkItemID, authn.PrincipalIDFromContext(ctx))
		if errors.Is(err, ErrWorkForbidden) || errors.Is(err, ErrProjectForbidden) || errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		count++
	}
	return count, nil
}
