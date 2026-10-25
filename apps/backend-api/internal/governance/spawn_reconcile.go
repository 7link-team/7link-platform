package governance

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// reconcileSpawnGrants visits immutable ID pages so healthy grants cannot
// starve later grants. Each grant has its own bounded, replay-safe transaction.
func (m *Module) reconcileSpawnGrants(ctx context.Context, job jobs.Job) error {
	if job.TenantID != 0 {
		return jobs.Permanent(ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	if m.principals.Lock == nil || m.principals.LockActiveRow == nil || m.spawnDirectory.AgentOn == nil || m.delegations.members == nil {
		return ErrUnavailable
	}
	var after guid.ID
	for {
		q := query.BindSpawnGrants(db, m.seed.names)
		rows, err := q.WithContext(ctx).Where(q.ID.Gt(after), q.State.In("ACTIVE", "EXHAUSTED")).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		for _, candidate := range rows {
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				// Match reservation's principal -> grant lock order.
				if err := m.principals.Lock(ctx, tx, candidate.GranteePrincipalID); err != nil {
					return err
				}
				q := query.BindSpawnGrants(tx, m.seed.names)
				row, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(candidate.TenantID), q.ID.Eq(candidate.ID)).First()
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				if err != nil {
					return err
				}
				if row.State != "ACTIVE" && row.State != "EXHAUSTED" {
					return nil
				}
				if !row.EndsAt.After(m.seed.now()) {
					return m.endSpawnGrantOn(ctx, tx, row, "EXPIRED", "WINDOW_ENDED", "Spawn grant window ended", 0)
				}
				_, active, err := m.principals.LockActiveRow(ctx, tx, row.GranteePrincipalID)
				if err != nil {
					return err
				}
				member, err := m.delegations.members.IsMemberActiveOn(ctx, tx, row.TenantID, row.GranteePrincipalID)
				if err != nil {
					return err
				}
				agent, found, err := m.spawnDirectory.AgentOn(ctx, tx, row.TenantID, row.GranteeAgentID)
				if err != nil {
					return err
				}
				if !active || !member || !found || agent.PrincipalID != row.GranteePrincipalID || agent.State != "ACTIVE" || !slices.Contains(agent.Roles, "LEAD") || (agent.Origin == "AGENT_SPAWNED" && agent.AdoptedAt == nil) {
					return m.endSpawnGrantOn(ctx, tx, row, "REVOKED", "GRANTEE_INACTIVE", "Spawn supervisor is no longer eligible", 0)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}
