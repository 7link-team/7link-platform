package governance

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func spawnHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *governanceTestPublisher, SpawnGrantInput) {
	t.Helper()
	m, db, ctx, pub, _ := delegationHarness(t)
	m.spawnDirectory = fleet.SpawnGrantDirectory{
		MachineOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
			return fleet.MachineFact{State: "ONLINE"}, nil
		},
		AgentOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "ACTIVE", Origin: "HUMAN_CREATED", Roles: []string{"LEAD"}}, true, nil
		},
		ModelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (bool, error) { return true, nil },
	}
	m.spawnFence = func(context.Context, *gorm.DB, string) error { return nil }
	in := SpawnGrantInput{GranteeAgentID: 33, DurationHours: 48, Reason: "Bounded project worker capacity", ReauthProof: "proof", SpawnGrantScope: SpawnGrantScope{ProjectIDs: []guid.ID{99}, MaxActiveAgents: 2, MaxSpawnsPerDay: 3, AllowedRoles: []string{"EXECUTOR"}, AllowedMachineIDs: []guid.ID{10}, AllowedModels: []SpawnAllowedModel{{Runtime: "CODEX", Model: "actual-model"}}, MaxEffort: "high", BudgetCapMicros: 2000000, PerAgentCapMicros: 1000000, AgentTTLHours: 24, IdleRetireHours: 4, ApprovalMode: "AUTO"}}
	return m, db, ctx, pub, in
}

func TestSpawnGrantSourceFailuresRollbackAllFacts(t *testing.T) {
	for _, fault := range []string{"agent", "lock", "second agent", "id", "zero id", "audit", "publisher", "insert", "project closed", "machine revoked", "machine error", "replacement missing"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, _, in := spawnGrantHarness(t)
			switch fault {
			case "agent":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, ErrUnavailable
				}
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "second agent":
				original := m.spawnDirectory.AgentOn
				calls := 0
				m.spawnDirectory.AgentOn = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
					calls++
					if calls == 2 {
						return fleet.AgentIdentity{}, false, ErrUnavailable
					}
					return original(ctx, tx, tenant, id)
				}
			case "id":
				m.seed.ids = seedTestIDs{err: ErrUnavailable}
			case "zero id":
				m.seed.ids = seedTestIDs{}
			case "audit":
				m.seed.audit = nil
			case "publisher":
				m.publisher = nil
			case "insert":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_grant BEFORE INSERT ON "+m.seed.names.SpawnGrants.String()+" BEGIN SELECT RAISE(ABORT,'grant insert failed'); END").Error)
			case "project closed":
				m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
					return DelegationProjectState{Stage: "CLOSED"}, nil
				}
			case "machine revoked":
				m.spawnDirectory.MachineOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{State: "REVOKED"}, nil
				}
			case "machine error":
				m.spawnDirectory.MachineOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
					return fleet.MachineFact{}, ErrUnavailable
				}
			case "replacement missing":
				id := guid.ID(999)
				in.ReplacesGrantID = &id
			}
			_, err := grantSpawn(t, m, db, ctx, in)
			require.Error(t, err)
			for _, table := range []string{m.seed.names.SpawnGrants.String(), "audit_probe", "event_probe"} {
				require.Zero(t, seedCount(t, db, table), table)
			}
		})
	}
}

func TestSpawnGrantRequiresExplicitTransactionAndMatchingTenant(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	_, err := m.grantSpawnOn(ctx, nil, 1, in, "cookie")
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.grantSpawnOn(ctx, db, 1, in, "cookie")
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.grantSpawnOn(ctx, tx, 2, in, "cookie"); return e }), ErrForbidden)
	in.MaxActiveAgents = 0
	_, err = m.spawnScopeOn(ctx, db, 1, in.SpawnGrantScope)
	require.ErrorIs(t, err, ErrSpawnScope)
	require.Zero(t, seedCount(t, db, m.seed.names.SpawnGrants.String()))
	require.Zero(t, seedCount(t, db, "event_probe"))
}

func TestSpawnGrantLifecycleReplacementAndRevocationAreAtomic(t *testing.T) {
	m, db, ctx, pub, in := spawnHarness(t)
	grant := func(in SpawnGrantInput) (SpawnGrantView, error) {
		var out SpawnGrantView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.grantSpawnOn(ctx, tx, 1, in, "cookie")
			return err
		})
		return out, err
	}
	first, err := grant(in)
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", first.State)
	require.Equal(t, "AUTO", first.EffectiveApprovalMode)
	require.Equal(t, "0", first.SpentMicros)
	_, err = grant(in)
	require.ErrorIs(t, err, ErrSpawnActive)
	in.ReplacesGrantID = &first.ID
	pub.Fail = true
	_, err = grant(in)
	require.Error(t, err)
	q := query.BindSpawnGrants(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(first.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	require.NotNil(t, row.ActiveKey)
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	pub.Fail = false
	next, err := grant(in)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, next.ID)
	row, err = q.WithContext(ctx).Where(q.ID.Eq(first.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "REVOKED", row.State)
	require.Equal(t, "REPLACED", *row.EndReason)
	require.Nil(t, row.ActiveKey)
	var revoked SpawnGrantView
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		revoked, err = m.revokeSpawnGrantOn(ctx, tx, 1, next.ID, next.Version, "Operator removed spawning authority")
		return err
	})
	require.NoError(t, err)
	require.Equal(t, "REVOKED", revoked.State)
	require.EqualValues(t, 4, seedCount(t, db, "event_probe"))
}

func TestSpawnGrantReadsUseCurrentModeAndEffectiveWindow(t *testing.T) {
	m, db, ctx, _, in := spawnHarness(t)
	var grant SpawnGrantView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		grant, err = m.grantSpawnOn(ctx, tx, 1, in, "cookie")
		return err
	}))
	q := query.BindSpawnGrants(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).Take()
	require.NoError(t, err)
	m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
		return DelegationProjectState{Stage: "ACTIVE", Mode: "HYBRID"}, nil
	}
	at := grant.EndsAt
	m.seed.now = func() time.Time { return at }
	view, err := m.spawnGrantViewOn(ctx, db, row)
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", view.State)
	require.Equal(t, "EXPIRED", view.EffectiveState)
	require.Equal(t, "REQUIRE_APPROVAL", view.EffectiveApprovalMode)
	require.Zero(t, view.SpawnedToday)
	_, err = m.spawnScopeOn(ctx, db, 1, in.SpawnGrantScope)
	require.NoError(t, err)
	m.delegations.projects.InspectOn = nil
	_, err = m.spawnGrantViewOn(ctx, db, row)
	require.ErrorIs(t, err, ErrUnavailable)
	row.ScopeJSON = "broken"
	_, err = m.spawnGrantViewOn(ctx, db, row)
	require.ErrorIs(t, err, ErrUnavailable)
}
