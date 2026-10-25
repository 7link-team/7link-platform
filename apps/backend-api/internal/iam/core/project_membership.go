package core

import (
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// TemporaryGrantFact is current persisted grant provenance, not an authorization decision.
type TemporaryGrantFact struct {
	RoleID           guid.ID
	StartsAt, EndsAt time.Time
	SourceType       string
}
type TemporaryGrantReader func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (TemporaryGrantFact, bool, error)

func (m *Module) provideProjectMembership(s *module.Services) error {
	if err := module.Provide[ProjectMemberBindings](s, ProjectMemberBindings{ListOn: m.projectMemberBindingsOn, DeleteOn: m.deleteProjectMemberBindingOn}); err != nil {
		return err
	}
	return module.Provide[TemporaryGrantReader](s, m.temporaryGrantOn)
}
func (m *Module) temporaryGrantOn(ctx context.Context, tx *gorm.DB, tenant, principal, id guid.ID) (TemporaryGrantFact, bool, error) {
	if m == nil || m.runtime == nil || tx == nil || tx.Statement == nil || tenant <= 0 || principal <= 0 || id <= 0 {
		return TemporaryGrantFact{}, false, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return TemporaryGrantFact{}, false, ErrInvalidArgument
	}
	q := query.Bind(tx, m.runtime.TablePrefix()).TemporaryRoleGrants
	row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal), q.ID.Eq(id)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TemporaryGrantFact{}, false, nil
	}
	if err != nil {
		return TemporaryGrantFact{}, false, err
	}
	return TemporaryGrantFact{RoleID: row.RoleID, StartsAt: row.StartsAt, EndsAt: row.EndsAt, SourceType: row.SourceType}, true, nil
}

// ProjectMemberBindings is the reserved project owner's counterpart to
// EntityRoleBindingStore. The owner authorizes project_manage and validates
// seeded member roles first. Deny bindings are never removable through it.
type ProjectMemberBindings struct {
	ListOn   func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]EntityRoleBinding, error)
	DeleteOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error)
}

func (m *Module) projectMemberBindingsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) ([]EntityRoleBinding, error) {
	svc, e := m.entityServiceOn(tx)
	if e != nil {
		return nil, e
	}
	entity, e := svc.repo.GetEntity(ctx, tenant, project)
	if e != nil {
		return nil, e
	}
	if entity.Type != "project" {
		return nil, ErrPrivilegeEscalation
	}
	tables, _, e := svc.repo.tables()
	if e != nil {
		return nil, e
	}
	q := tables.RoleBindings
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ScopeType.Eq(entityBindingScope), q.ScopeID.Eq(project)).Order(q.RoleID, q.PrincipalID).Find()
	if e != nil {
		return nil, e
	}
	out := make([]EntityRoleBinding, 0, len(rows))
	for _, r := range rows {
		out = append(out, entityRoleBindingFromRow(r))
	}
	return out, nil
}
func (m *Module) deleteProjectMemberBindingOn(ctx context.Context, tx *gorm.DB, tenant, project, role, principal guid.ID) (bool, error) {
	if e := validateEntityBinding(tenant, project, role, principal, BindingAllow, time.Time{}); e != nil {
		return false, e
	}
	svc, e := m.entityServiceOn(tx)
	if e != nil {
		return false, e
	}
	record := newAuditRecord(ctx, tenant, "entity_role_binding_deleted", "entity", project)
	record.Detail["role_id"], record.Detail["principal_id"] = role, principal
	changed := false
	e = svc.writes.Run(ctx, record, func(repo *Repository) error {
		if e := svc.requireGrantablePermissionsOn(ctx, repo, tenant); e != nil {
			return e
		}
		entity, e := repo.withDB(systemCurrentRead(repo.writer())).GetEntity(ctx, tenant, project)
		if e != nil {
			return e
		}
		if entity.Type != "project" {
			return ErrPrivilegeEscalation
		}
		if e = repo.revisions.Lock(ctx, repo.writer(), tenant); e != nil {
			return e
		}
		binding, exists, e := repo.withDB(systemCurrentRead(repo.writer())).findEntityRoleBinding(ctx, tenant, project, role, principal)
		if e != nil {
			return e
		}
		if !exists {
			record.SkipAudit = true
			return nil
		}
		if binding.Effect != BindingAllow {
			return ErrPrivilegeEscalation
		}
		changed, e = repo.DeleteEntityRoleBinding(ctx, tenant, project, role, principal)
		record.PolicyChanged, record.SkipAudit = changed, !changed
		return e
	})
	return changed, e
}
