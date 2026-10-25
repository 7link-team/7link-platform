package governance

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnRequestNotificationRequiresCurrentGrantSupervisorAndWindow(t *testing.T) {
	for _, scenario := range []string{"current", "expired_request", "expired_grant", "spent", "supervisor_missing", "supervisor_error", "source_unavailable", "invalid_scope", "request_read", "grant_read"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, _, agent, _, in := agentSpawnFixture(t)
			var id guid.ID
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				row, err := m.createAgentSpawnRequestOn(agent, tx, 1, in)
				id = row.ID
				return err
			}))
			fault := errors.New("notification fact storage unavailable")
			remove := func() {}
			switch scenario {
			case "expired_request":
				require.NoError(t, db.Table(m.seed.names.SpawnRequests.String()).Where("id = ?", id).Update("expires_at", m.seed.now().Add(-time.Second)).Error)
			case "expired_grant":
				now := m.seed.now()
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("tenant_id = ?", 1).Update("ends_at", now.Add(time.Second)).Error)
				m.seed.now = func() time.Time { return now.Add(2 * time.Second) }
			case "spent":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("tenant_id = ?", 1).Update("spent_micros", in.Facts.EstimatedCapMicros*100).Error)
			case "supervisor_missing":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
			case "supervisor_error":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "source_unavailable":
				m.spawnDirectory.AgentOn = nil
			case "invalid_scope":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("tenant_id = ?", 1).Update("scope_json", `{}`).Error)
			case "request_read", "grant_read":
				table := m.seed.names.SpawnRequests.String()
				if scenario == "grant_read" {
					table = m.seed.names.SpawnGrants.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:notification-facts", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, db.Callback().Query().Remove("test:notification-facts")) }
			}
			var found bool
			err := db.Transaction(func(tx *gorm.DB) error {
				fact, ok, err := m.spawnNotificationFacts(agent, tx, 1, id, "request")
				found = ok
				if ok {
					require.Equal(t, "REQUESTED", fact.State)
					require.Equal(t, []guid.ID{99}, fact.ProjectIDs)
					require.EqualValues(t, 2, fact.GrantorID)
				}
				return err
			})
			remove()
			require.Equal(t, scenario == "current", found)
			switch scenario {
			case "supervisor_error", "request_read", "grant_read":
				require.ErrorIs(t, err, fault)
			case "source_unavailable", "invalid_scope":
				require.ErrorIs(t, err, ErrUnavailable)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestDelegationNotificationFactsUsePersistedGrantorAndTenant(t *testing.T) {
	m, db, ctx, _, in := delegationHarness(t)
	created, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, exists, err := m.delegationNotificationFacts(ctx, tx, 1, created.ID)
		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, "ACTIVE", fact.State)
		require.EqualValues(t, 2, fact.GrantorID)
		_, exists, err = m.delegationNotificationFacts(ctx, tx, 999, created.ID)
		require.NoError(t, err)
		require.False(t, exists)
		q := query.BindDelegations(tx, m.seed.names)
		_, err = q.WithContext(ctx).Where(q.ID.Eq(created.ID)).Updates(map[string]any{"state": "EXPIRED", "end_reason": "WINDOW_ENDED", "ended_at": m.seed.now(), "active_key": nil})
		require.NoError(t, err)
		fact, exists, err = m.delegationNotificationFacts(ctx, tx, 1, created.ID)
		require.NoError(t, err)
		require.True(t, exists)
		require.Equal(t, "WINDOW_ENDED", fact.EndReason)
		return nil
	}))
	_, _, err = m.delegationNotificationFacts(ctx, db, 1, created.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = m.delegationNotificationFacts(ctx, nil, 1, created.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestIncidentNotificationFactsRetainTenantAndActualState(t *testing.T) {
	m, db, ctx, _ := settingsHarness(t)
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.recordEscalationOn(ctx, tx, escalationFixture())
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		fact, found, err := m.incidentNotificationFacts(ctx, tx, 1, id)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "OPEN", fact.State)
		require.Equal(t, guid.ID(3), fact.ProjectID)
		require.False(t, fact.CreatedAt.IsZero())
		_, found, err = m.incidentNotificationFacts(ctx, tx, 2, id)
		require.NoError(t, err)
		require.False(t, found)
		_, err = m.incidentDecisionOn(ctx, tx, 1, id, 1, "ACKNOWLEDGED", "")
		require.NoError(t, err)
		fact, found, err = m.incidentNotificationFacts(ctx, tx, 1, id)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "ACKNOWLEDGED", fact.State)
		return err
	}))
	_, _, err := m.incidentNotificationFacts(ctx, db, 1, id)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestSpawnNotificationFactsUseActualGrantSpendAndTenant(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		f, found, err := m.spawnNotificationFacts(ctx, tx, 1, grant.ID, "grant")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, in.BudgetCapMicros, f.Cap)
		require.Zero(t, f.Spent)
		require.Equal(t, grant.GrantedBy, f.GrantorID)
		_, found, err = m.spawnNotificationFacts(ctx, tx, 2, grant.ID, "grant")
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))
	_, _, err = m.spawnNotificationFacts(ctx, nil, 1, grant.ID, "grant")
	require.ErrorIs(t, err, ErrInvalidArgument)
}
