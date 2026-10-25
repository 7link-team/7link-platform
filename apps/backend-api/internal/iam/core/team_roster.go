package core

import (
	"context"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TeamRoster publishes current tenant membership and effective role keys. It
// intentionally carries no email, login identifier, token or directory tables.
type TeamRoster struct {
	ListOn    func(context.Context, *gorm.DB, guid.ID, int, int, string) (TeamRosterPage, error)
	ProjectOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (map[guid.ID][]string, error)
}
type TeamRosterMember struct {
	PrincipalID guid.ID   `json:"principal_id"`
	Name        string    `json:"name"`
	State       string    `json:"state"`
	Roles       []string  `json:"roles"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedAt   time.Time `json:"created_at"`
}
type TeamRosterPage struct {
	Items   []TeamRosterMember
	HasMore bool
}

func (m *Module) provideTeamRoster(s *module.Services) error {
	if err := module.Provide(s, EntitySubjectBindingRevoker(m.revokeEntitySubjectBindingsOn)); err != nil {
		return err
	}
	return module.Provide(s, TeamRoster{ListOn: m.teamRosterOn, ProjectOn: m.teamRosterProjectOn})
}

// EntitySubjectBindingRevoker removes direct entity bindings through the
// existing audited command and policy revision fence. Its trusted domain caller
// must authorize the lifecycle transition before calling it, on the same tx.
type EntitySubjectBindingRevoker func(context.Context, *gorm.DB, guid.ID, guid.ID) error

func (m *Module) revokeEntitySubjectBindingsOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
	if tenant <= 0 || principal <= 0 {
		return ErrInvalidArgument
	}
	service, err := m.entityServiceOn(tx)
	if err != nil {
		return err
	}
	if m.repository == nil {
		return ErrUnavailable
	}
	// Preserve IAM's authorization -> policy revision -> binding lock order.
	// The locked revision also prevents a concurrent grant from being omitted
	// by a retirement transaction running under MySQL repeatable-read.
	repo := m.repository.withDB(tx)
	if err = service.requireGrantablePermissionsOn(ctx, repo, tenant); err != nil {
		return err
	}
	if err = repo.revisions.Lock(ctx, tx, tenant); err != nil {
		return err
	}
	q := query.BindWithNames(tx, m.repository.names).RoleBindings
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.PrincipalID.Eq(principal), q.ScopeType.Eq(entityBindingScope)).Order(q.ScopeID, q.RoleID).Find()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err = service.DeleteEntityRoleBinding(ctx, tenant, row.ScopeID, row.RoleID, principal); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) teamRosterProjectOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID) (map[guid.ID][]string, error) {
	if m.repository == nil || tx == nil {
		return nil, ErrUnavailable
	}
	if tenant <= 0 || project <= 0 {
		return nil, ErrInvalidArgument
	}
	repo := m.repository.withDB(tx)
	rows, e := repo.ListEntityRoleBindings(ctx, tenant, project)
	if e != nil {
		return nil, e
	}
	out := map[guid.ID][]string{}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.Effect != BindingAllow || !row.ExpiresAt.IsZero() && !row.ExpiresAt.After(now) {
			continue
		}
		role, e := repo.GetRole(ctx, tenant, row.RoleID)
		if e != nil {
			return nil, e
		}
		out[row.PrincipalID] = append(out[row.PrincipalID], role.Name)
	}
	return out, nil
}
func (m *Module) teamRosterOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, limit, offset int, search string) (TeamRosterPage, error) {
	out := TeamRosterPage{Items: []TeamRosterMember{}}
	if m.repository == nil || tx == nil || tx.Statement == nil {
		return out, ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return out, ErrInvalidArgument
	}
	if tenant <= 0 || limit < 1 || limit > 200 || offset < 0 || !utf8.ValidString(search) || utf8.RuneCountInString(search) > 200 {
		return out, ErrInvalidArgument
	}
	q := query.BindWithNames(tx, m.repository.names).TenantMembers
	r := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
	if search != "" {
		r = r.Where(q.DisplayName.Like("%" + search + "%"))
	}
	rows, e := r.Order(q.PrincipalID).Offset(offset).Limit(limit + 1).Find()
	if e != nil {
		return out, e
	}
	if len(rows) > limit {
		out.HasMore = true
		rows = rows[:limit]
	}
	repo := m.repository.withDB(tx)
	for _, row := range rows {
		v := TeamRosterMember{PrincipalID: row.PrincipalID, Name: row.DisplayName, State: row.Status, Roles: []string{}, UpdatedAt: row.UpdatedAt, CreatedAt: row.CreatedAt}
		roles, e := repo.ListMemberRoleIDs(ctx, tenant, row.PrincipalID)
		if e != nil {
			return out, e
		}
		for _, id := range roles {
			role, e := repo.GetRole(ctx, tenant, id)
			if e != nil {
				return out, e
			}
			v.Roles = append(v.Roles, role.Name)
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}
