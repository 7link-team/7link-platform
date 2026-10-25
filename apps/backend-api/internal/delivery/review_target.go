package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// reviewTargetOn normalizes the frozen review content without changing either
// aggregate's state. The sheet command already holds its principal/project fence.
func (m *Module) reviewTargetOn(ctx context.Context, tx *gorm.DB, project, requirement guid.ID, workID *guid.ID) (*model.Requirement, *model.WorkItem, error) {
	if workID == nil {
		r, e := m.sheetRequirementOn(ctx, tx, project, requirement)
		return r, nil, e
	}
	if *workID <= 0 {
		return nil, nil, ErrInvalidArgument
	}
	q := query.BindWorkItems(tx, m.service.names)
	c, e := m.workPolicy(ctx, tx, authn.TenantIDFromContext(ctx), project, "work_item_read", authn.PrincipalIDFromContext(ctx))
	if e != nil {
		return nil, nil, e
	}
	scope, e := workScope(c, project, q.CreatedBy)
	if e != nil {
		return nil, nil, e
	}
	base := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.ProjectID.Eq(project), q.ID.Eq(*workID), q.ArchivedAt.IsNull())
	if scope != nil {
		base = base.Where(scope)
	}
	w, e := base.Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if e != nil {
		return nil, nil, e
	}
	return &model.Requirement{ID: w.ID, TenantID: w.TenantID, ProjectID: w.ProjectID, Title: w.Title, Description: w.Description, Status: w.State, Version: w.Version}, w, nil
}

func (m *Module) reviewTargetCriteriaOn(ctx context.Context, tx *gorm.DB, req *model.Requirement, work *model.WorkItem) ([]requirementCriterionView, error) {
	out := []requirementCriterionView{}
	if work != nil {
		q := query.BindWorkCriteria(tx, m.service.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(work.TenantID), q.ProjectID.Eq(work.ProjectID), q.WorkItemID.Eq(work.ID)).Order(q.Position, q.ID).Find()
		if e != nil {
			return nil, e
		}
		for i, row := range rows {
			out = append(out, requirementCriterionView{ID: row.ID, Code: fmt.Sprintf("C%d", i+1), Text: row.Text, Required: true, Position: row.Position})
		}
		return out, nil
	}
	q := query.BindCriteria(tx, m.service.names)
	rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(req.TenantID), q.ProjectID.Eq(req.ProjectID), q.RequirementID.Eq(req.ID), q.ArchivedAt.IsNull(), q.RetiredInVersion.IsNull()).Order(q.Position, q.ID).Find()
	if e != nil {
		return nil, e
	}
	for _, row := range rows {
		out = append(out, requirementCriterionRecord(row))
	}
	return out, nil
}
