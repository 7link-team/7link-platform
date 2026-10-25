package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/knowledge"
	"context"
	"encoding/json"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WorkKnowledgeContext returns canonical ready-field references under current
// work-item authority. Execution input cannot invent another source list.
type WorkKnowledgeContext struct {
	ReadOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error)
}
type workKnowledgeResolver = knowledge.RuntimeContext

func (m *Module) injectKnowledgeContext(s *module.Services) error {
	value, err := module.Resolve[knowledge.RuntimeContext](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	m.knowledgeContext = value
	return err
}
func (m *Module) validateKnowledgeRefsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, refs []workContextRef) error {
	ids := []guid.ID{}
	for _, ref := range refs {
		if ref.Kind == "KNOWLEDGE" {
			id, err := guid.RequireID(ref.Ref)
			if err != nil {
				return ErrInvalidArgument
			}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if m.knowledgeContext.ResolveOn == nil {
		return ErrUnavailable
	}
	_, err := m.knowledgeContext.ResolveOn(ctx, tx, tenant, project, ids)
	return err
}
func (m *Module) provideKnowledgeContext(s *module.Services) error {
	return module.Provide[WorkKnowledgeContext](s, WorkKnowledgeContext{ReadOn: m.workKnowledgeContextOn})
}
func (m *Module) workKnowledgeContextOn(ctx context.Context, tx *gorm.DB, tenant, project, work guid.ID) ([]guid.ID, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || work <= 0 || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if m == nil || m.service == nil || m.principals.LockActiveRow == nil || m.members == nil || m.workPolicy == nil {
		return nil, ErrUnavailable
	}
	principal, active, err := m.principals.LockActiveRow(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	if !active || principal.ID != actor {
		return nil, ErrWorkForbidden
	}
	member, err := m.members.IsMemberActiveOn(ctx, tx, tenant, actor)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrWorkForbidden
	}
	c, err := m.workPolicy(ctx, tx, tenant, project, "work_item_read", actor)
	if err != nil {
		return nil, err
	}
	q := query.BindWorkItems(tx, m.service.names)
	scope, err := workScope(c, project, q.CreatedBy)
	if err != nil {
		return nil, err
	}
	projects := query.BindProjects(tx, m.service.names)
	pr, err := projects.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(projects.TenantID.Eq(tenant), projects.ProjectID.Eq(project), projects.ArchivedAt.IsNull()).First()
	if err != nil {
		return nil, err
	}
	if pr.Stage != "ACTIVE" {
		return nil, ErrWorkProjectReadOnly
	}
	read := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(work), q.ArchivedAt.IsNull())
	if scope != nil {
		read = read.Where(scope)
	}
	row, err := read.First()
	if err != nil {
		return nil, err
	}

	var refs []workContextRef
	if json.Unmarshal([]byte(row.ContextRefs), &refs) != nil {
		return nil, ErrUnavailable
	}
	ids := []guid.ID{}
	for _, ref := range refs {
		if ref.Kind == "KNOWLEDGE" {
			id, err := guid.RequireID(ref.Ref)
			if err != nil {
				return nil, ErrInvalidArgument
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}
