package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"database/sql/driver"
	"errors"
	"gorm.io/gorm"
	"time"
)

type changeHistoryView struct {
	ID              guid.ID   `json:"id"`
	FromVersionNo   int       `json:"from_version_no"`
	ToVersionNo     int       `json:"to_version_no"`
	Summary         string    `json:"summary"`
	ActorID         guid.ID   `json:"actor_id"`
	AuthoritySource string    `json:"authority_source"`
	CreatedAt       time.Time `json:"created_at"`
}
type changeImpactView struct {
	WorkItemID    guid.ID `json:"work_item_id"`
	Impact        string  `json:"impact"`
	VersionBefore int     `json:"version_before"`
}

func (m *Module) changeHistoryOn(ctx context.Context, tx *gorm.DB, tenant, project, id, actor guid.ID, offset, limit int) ([]changeHistoryView, int64, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return nil, 0, ErrInvalidArgument
	}
	if _, err := m.readRequirement(ctx, tx, tenant, project, id, actor); err != nil {
		return nil, 0, err
	}
	q := query.BindChanges(tx, m.service.names)
	base := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.RequirementID.Eq(id))
	total, err := base.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := base.Omit(q.Diff).Order(q.ToVersionNo.Desc(), q.ID.Desc()).Offset(offset).Limit(limit).Find()
	if err != nil {
		return nil, 0, err
	}
	items := make([]changeHistoryView, 0, len(rows))
	for _, r := range rows {
		items = append(items, changeHistoryView{ID: r.ID, FromVersionNo: r.FromVersionNo, ToVersionNo: r.ToVersionNo, Summary: r.Summary, ActorID: r.ActorID, AuthoritySource: r.AuthoritySource, CreatedAt: r.CreatedAt})
	}
	return items, total, nil
}
func (m *Module) changeImpactsOn(ctx context.Context, tx *gorm.DB, tenant, project, id, changeID, actor guid.ID, offset, limit int) ([]changeImpactView, int64, error) {
	if offset < 0 || limit < 1 || limit > 200 {
		return nil, 0, ErrInvalidArgument
	}
	if _, err := m.readRequirement(ctx, tx, tenant, project, id, actor); err != nil {
		return nil, 0, err
	}
	c := query.BindChanges(tx, m.service.names)
	if _, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ProjectID.Eq(project), c.RequirementID.Eq(id), c.ID.Eq(changeID)).First(); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = ErrProjectNotFound
		}
		return nil, 0, err
	}
	q := query.BindChangeImpacts(tx, m.service.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ChangeRecordID.Eq(changeID)).Order(q.WorkItemID).Find()
	if err != nil {
		return nil, 0, err
	}
	items := []changeImpactView{}
	if len(rows) == 0 {
		return items, 0, nil
	}
	ids := make([]driver.Valuer, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.WorkItemID)
	}
	w := query.BindWorkItems(tx, m.service.names)
	constraint, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
	if err != nil {
		return nil, 0, err
	}
	scope, err := workScope(constraint, project, w.CreatedBy)
	if err != nil {
		return nil, 0, err
	}
	visible := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.In(ids...), w.ArchivedAt.IsNull())
	if scope != nil {
		visible = visible.Where(scope)
	}
	works, err := visible.Select(w.ID).Find()
	if err != nil {
		return nil, 0, err
	}
	allowed := map[guid.ID]bool{}
	for _, r := range works {
		allowed[r.ID] = true
	}
	for _, r := range rows {
		if allowed[r.WorkItemID] {
			items = append(items, changeImpactView{WorkItemID: r.WorkItemID, Impact: r.Impact, VersionBefore: r.VersionBefore})
		}
	}
	total := len(items)
	start := min(offset, total)
	end := start + min(limit, total-start)
	return items[start:end], int64(total), nil
}
