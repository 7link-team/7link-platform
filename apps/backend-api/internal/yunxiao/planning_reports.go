package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"time"
)

type PlanningCarryoverFact struct {
	WorkItemID, FromIterationID guid.ID
	ToIterationID               *guid.ID
	CarriedAt                   time.Time
}

// PlanningCarryovers is consumed inside the report scheduler's authorized tenant
// transaction. It never broadens the explicit project/principal dimensions.
type PlanningCarryovers struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) ([]PlanningCarryoverFact, error)
}

func (m *Module) providePlanningReports(s *module.Services) error {
	return module.Provide[PlanningCarryovers](s, PlanningCarryovers{ReadOn: m.planningCarryoversOn})
}
func (m *Module) planningCarryoversOn(ctx context.Context, tx *gorm.DB, tenant, project, principal guid.ID, start, end time.Time) ([]PlanningCarryoverFact, error) {
	if tx == nil || tenant <= 0 || project <= 0 || principal < 0 || !end.After(start) {
		return nil, ErrInvalid
	}
	q := query.BindPlanningCarryovers(tx, m.names)
	rows := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.CarriedAt.Gte(start), q.CarriedAt.Lt(end))
	if principal > 0 {
		rows = rows.Where(q.PrincipalID.Eq(principal))
	}
	found, err := rows.Order(q.CarriedAt, q.ID).Limit(501).Find()
	if err != nil {
		return nil, err
	}
	if len(found) > 500 {
		return nil, ErrUnavailable
	}
	out := []PlanningCarryoverFact{}
	for _, row := range found {
		out = append(out, PlanningCarryoverFact{WorkItemID: row.WorkItemID, FromIterationID: row.FromIterationID, ToIterationID: row.ToIterationID, CarriedAt: row.CarriedAt})
	}
	return out, nil
}
