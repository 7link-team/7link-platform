package governance

import (
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
)

// ProjectTerminalCleanup is used by the authorized project owner, under its
// project lock and the complete principal fence. Existing revocation commands
// own IAM carrier removal, audit/outbox and policy revision changes.
type ProjectTerminalCleanup struct {
	PrincipalsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	EndOn        func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string, []guid.ID) error
}

func (m *Module) provideProjectTerminal(s *module.Services) error {
	return module.Provide[ProjectTerminalCleanup](s, ProjectTerminalCleanup{PrincipalsOn: m.modeDelegationPrincipalsOn, EndOn: m.endProjectAuthorityOn})
}
func (m *Module) endProjectAuthorityOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, source, reason string, locked []guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 || !strings.HasPrefix(source, "ROLE:") {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	q := query.BindDelegations(tx, m.seed.names)
	rows, e := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.State.Eq("ACTIVE")).Order(q.ID).Find()
	if e != nil {
		return e
	}
	for _, r := range rows {
		if !slices.Contains(locked, r.GranteePrincipalID) {
			return ErrVersion
		}
		if e = m.endDelegationRowOn(ctx, tx, r, "REVOKED", "PROJECT_TERMINAL", reason, source); e != nil {
			return e
		}
	}
	grants, e := m.modeScopedSpawnGrantsOn(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), tenant, project)
	if e != nil {
		return e
	}
	g := query.BindSpawnGrants(tx, m.seed.names)
	for _, r := range grants {
		if !slices.Contains(locked, r.GranteePrincipalID) {
			return ErrVersion
		}
		current, e := g.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(g.TenantID.Eq(tenant), g.ID.Eq(r.ID), g.Version.Eq(r.Version)).First()
		if e != nil {
			return e
		}
		if e = m.endSpawnGrantOn(ctx, tx, current, "REVOKED", "PROJECT_TERMINAL", reason, authn.PrincipalIDFromContext(ctx), source); e != nil {
			return e
		}
	}
	return nil
}
