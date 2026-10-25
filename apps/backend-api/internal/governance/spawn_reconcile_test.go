package governance

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnReconcileExpiryAtomicReplay(t *testing.T) {
	m, db, ctx, pub, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	require.NoError(t, m.reconcileSpawnGrants(t.Context(), jobs.Job{}))
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	m.seed.now = func() time.Time { return grant.EndsAt }
	pub.Fail = true
	require.Error(t, m.reconcileSpawnGrants(t.Context(), jobs.Job{}))
	q := query.BindSpawnGrants(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	pub.Fail = false
	appendAudit := m.seed.audit
	var auditKind string
	m.seed.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		auditKind = event.EventType
		return appendAudit(ctx, tx, event)
	}
	require.NoError(t, m.reconcileSpawnGrants(t.Context(), jobs.Job{}))
	require.Equal(t, "governance.spawn_grant_revoked", auditKind)
	row, err = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", row.State)
	require.Equal(t, "WINDOW_ENDED", *row.EndReason)
	require.Nil(t, row.ActiveKey)
	require.Nil(t, row.EndedBy)
	require.Equal(t, grant.Version+1, row.Version)
	require.NoError(t, m.reconcileSpawnGrants(t.Context(), jobs.Job{}))
	require.EqualValues(t, 2, seedCount(t, db, "event_probe"))
	require.Error(t, m.reconcileSpawnGrants(t.Context(), jobs.Job{TenantID: 1}))
}

func TestSpawnReconcileCurrentSupervisorEligibility(t *testing.T) {
	for _, reason := range []string{"principal", "membership", "missing", "role", "identity", "source"} {
		t.Run(reason, func(t *testing.T) {
			m, db, ctx, _, in := spawnGrantHarness(t)
			grant, err := grantSpawn(t, m, db, ctx, in)
			require.NoError(t, err)
			switch reason {
			case "principal":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
			case "membership":
				m.delegations.members = delegationMembers{false}
			default:
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					if reason == "source" {
						return fleet.AgentIdentity{}, false, ErrUnavailable
					}
					principal := guid.ID(3)
					if reason == "identity" {
						principal = 4
					}
					return fleet.AgentIdentity{ID: 33, PrincipalID: principal, State: "ACTIVE", Roles: []string{"EXECUTOR"}}, reason != "missing", nil
				}
			}
			err = m.reconcileSpawnGrants(t.Context(), jobs.Job{})
			q := query.BindSpawnGrants(db, m.seed.names)
			row, readErr := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
			require.NoError(t, readErr)
			if reason == "source" {
				require.ErrorIs(t, err, ErrUnavailable)
				require.Equal(t, "ACTIVE", row.State)
			} else {
				require.NoError(t, err)
				require.Equal(t, "REVOKED", row.State)
				require.Equal(t, "GRANTEE_INACTIVE", *row.EndReason)
			}
		})
	}
}
