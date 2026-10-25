package planning

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// loopProjectWriteOn adds only project lifecycle fencing. Loop authority
// remains with the owner and tenant policy checked by the domain command.
func (m *Module) loopProjectWriteOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, project *guid.ID) error {
	if project == nil {
		return nil
	}
	if m.project.ArchivedOn == nil {
		return ErrUnavailable
	}
	archived, err := m.project.ArchivedOn(ctx, tx, tenant, *project)
	if err != nil {
		return err
	}
	if archived {
		return ErrConflict
	}
	return nil
}

func (m *Module) injectArchiveResources(s *module.Services) error {
	c, err := module.Resolve[delivery.ProjectArchiveConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.Bind("planning", m.projectArchiveResourcesOn)
}
func (m *Module) projectArchiveResourcesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]delivery.ProjectArchiveResource, error) {
	if m == nil || tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	q := query.BindLoopContracts(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project)).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	out := []delivery.ProjectArchiveResource{}
	for _, r := range rows {
		out = append(out, delivery.ProjectArchiveResource{Kind: "PERIODIC_CONTRACT", ID: r.ID, State: r.State, Version: r.Version, Ready: r.State == "PAUSED" || r.State == "ARCHIVED" || r.State == "DRAFT"})
		q := query.BindLoopRuns(tx, m.names)
		runs, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.LoopContractID.Eq(r.ID), q.CompletedAt.IsNull()).Clauses(clause.Locking{Strength: "UPDATE"}).Order(q.ID).Find()
		if e != nil {
			return nil, e
		}
		for _, v := range runs {
			out = append(out, delivery.ProjectArchiveResource{Kind: "PERIODIC_RUN", ID: v.ID, State: v.State, Version: 1, Ready: false})
		}
	}
	p := query.BindPlans(tx, m.names)
	plans, err := p.WithContext(ctx).Where(p.TenantID.Eq(tenant), p.ProjectID.Eq(project), p.State.Eq("SUBMITTED")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(p.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range plans {
		out = append(out, delivery.ProjectArchiveResource{Kind: "PLAN_CONFIRMATION", ID: row.ID, State: row.State, Version: row.Version})
	}
	r := query.BindReports(tx, m.names)
	reports, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.State.Eq("SUBMITTED")).Clauses(clause.Locking{Strength: "UPDATE"}).Order(r.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range reports {
		out = append(out, delivery.ProjectArchiveResource{Kind: "REPORT_CONFIRMATION", ID: row.ID, State: row.State, Version: row.Version})
	}
	return out, nil
}
