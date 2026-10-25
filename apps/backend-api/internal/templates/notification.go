package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"context"
	"errors"
	"gorm.io/gorm"
)

// PublishedNotificationFacts exposes current project pins, without template bodies.
type PublishedNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)

func (m *Module) publishedNotificationProjectsOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) ([]guid.ID, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || id <= 0 {
		return nil, ErrInvalid
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalid
	}
	v := query.BindVersions(tx, m.names)
	current, err := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ID.Eq(id), v.State.Eq("PUBLISHED")).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []guid.ID{}, nil
	}
	if err != nil {
		return nil, err
	}
	b := query.BindBindings(tx, m.names)
	// The most recent binding for each project and template key is its current pin.
	// A project already upgraded to this or a later publication needs no notice.
	rows, err := b.WithContext(ctx).Join(v, v.TenantID.EqCol(b.TenantID), v.ID.EqCol(b.TemplateVersionID)).Where(b.TenantID.Eq(tenant), v.Key.Eq(current.Key)).Order(b.ID.Desc()).Find()
	if err != nil {
		return nil, err
	}
	seen := map[guid.ID]bool{}
	out := []guid.ID{}
	for _, row := range rows {
		if seen[row.ProjectID] {
			continue
		}
		seen[row.ProjectID] = true
		old, e := v.WithContext(ctx).Where(v.TenantID.Eq(tenant), v.ID.Eq(row.TemplateVersionID)).First()
		if e != nil {
			return nil, e
		}
		if old.ID != current.ID && old.UpdatedAt.Before(current.UpdatedAt) {
			out = append(out, row.ProjectID)
		}
	}
	return out, nil
}
