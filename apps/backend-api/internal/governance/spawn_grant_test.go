package governance

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func spawnGrantHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *governanceTestPublisher, SpawnGrantInput) {
	t.Helper()
	m, db, ctx, pub, _ := delegationHarness(t)
	m.spawnDirectory = fleet.SpawnGrantDirectory{
		AgentOn: func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
			if tenant != 1 {
				return fleet.AgentIdentity{}, false, nil
			}
			if id == 33 {
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "ACTIVE", Origin: "HUMAN_CREATED", Roles: []string{"LEAD"}}, true, nil
			}
			parent := guid.ID(33)
			return fleet.AgentIdentity{ID: id, TenantID: 1, PrincipalID: id + 100, State: "ACTIVE", Origin: "AGENT_SPAWNED", ParentAgentID: &parent, RuntimeKind: "CODEX", ModelID: "model", Roles: []string{"EXECUTOR"}}, true, nil
		},
		MachineOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.MachineFact, error) {
			return fleet.MachineFact{State: "ONLINE"}, nil
		},
		ModelOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (bool, error) { return true, nil },
	}
	m.spawnFence = func(_ context.Context, tx *gorm.DB, key string) error {
		require.Contains(t, key, "governance:spawn-grants:1")
		require.NotNil(t, tx)
		return nil
	}
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 3}, true, nil
	}
	require.NoError(t, m.seed.registry.Contribute([]authz.Action{{Resource: "spawn_grant", Verb: "manage", Scope: authz.ActionScopeTenant}, {Resource: "agent", Verb: "spawn", Scope: authz.ActionScopeTenant}}, nil))
	in := SpawnGrantInput{GranteeAgentID: 33, DurationHours: 48, Reason: "Approved bounded project capacity", ReauthProof: "proof", SpawnGrantScope: SpawnGrantScope{ProjectIDs: []guid.ID{99}, MaxActiveAgents: 1, MaxSpawnsPerDay: 2, AllowedRoles: []string{"EXECUTOR"}, AllowedMachineIDs: []guid.ID{5}, AllowedModels: []SpawnAllowedModel{{Runtime: "CODEX", Model: "model"}}, MaxEffort: "medium", BudgetCapMicros: 2000000, PerAgentCapMicros: 1000000, AgentTTLHours: 24, IdleRetireHours: 4, ApprovalMode: "REQUIRE_APPROVAL"}}
	return m, db, ctx, pub, in
}
func grantSpawn(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, in SpawnGrantInput) (SpawnGrantView, error) {
	t.Helper()
	var out SpawnGrantView
	e := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.grantSpawnOn(ctx, tx, 1, in, "cookie"); return e })
	return out, e
}

func TestSpawnGrantValidationAndAtomicSourceFailures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Module, *SpawnGrantInput)
	}{
		{"reviewer", func(_ *Module, in *SpawnGrantInput) { in.AllowedRoles = []string{"REVIEWER"} }},
		{"daily limit", func(_ *Module, in *SpawnGrantInput) { in.MaxSpawnsPerDay = 21 }},
		{"budget", func(_ *Module, in *SpawnGrantInput) { in.PerAgentCapMicros = in.BudgetCapMicros + 1 }},
		{"team auto", func(_ *Module, in *SpawnGrantInput) {
			in.ProjectIDs = []guid.ID{}
			in.ApprovalMode = "AUTO"
			in.TeamScopeConfirmed = true
		}},
		{"unconfirmed team", func(_ *Module, in *SpawnGrantInput) { in.ProjectIDs = []guid.ID{} }},
		{"duplicate project", func(_ *Module, in *SpawnGrantInput) { in.ProjectIDs = []guid.ID{99, 99} }},
		{"unknown effort", func(_ *Module, in *SpawnGrantInput) { in.MaxEffort = "unlimited" }},
		{"duration", func(_ *Module, in *SpawnGrantInput) { in.DurationHours = 2161 }},
		{"scope unavailable", func(m *Module, _ *SpawnGrantInput) { m.spawnDirectory.ModelOn = nil }},
		{"unobserved model", func(m *Module, _ *SpawnGrantInput) {
			m.spawnDirectory.ModelOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (bool, error) { return false, nil }
		}},
		{"bad project mode", func(m *Module, in *SpawnGrantInput) {
			in.ApprovalMode = "AUTO"
			m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
				return DelegationProjectState{Mode: "HYBRID"}, nil
			}
		}},
		{"inactive member", func(m *Module, _ *SpawnGrantInput) { m.delegations.members = delegationMembers{false} }},
		{"step up", func(m *Module, _ *SpawnGrantInput) {
			m.reauth = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return ErrForbidden }
		}},
		{"no fence", func(m *Module, _ *SpawnGrantInput) { m.spawnFence = nil }},
		{"fence failure", func(m *Module, _ *SpawnGrantInput) {
			m.spawnFence = func(context.Context, *gorm.DB, string) error { return errors.New("fence offline") }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, db, ctx, _, in := spawnGrantHarness(t)
			tc.mutate(m, &in)
			_, e := grantSpawn(t, m, db, ctx, in)
			require.Error(t, e)
			require.Zero(t, seedCount(t, db, m.seed.names.SpawnGrants.String()))
			require.Zero(t, seedCount(t, db, "audit_probe"))
			require.Zero(t, seedCount(t, db, "event_probe"))
		})
	}
	m, db, ctx, pub, in := spawnGrantHarness(t)
	pub.Fail = true
	_, e := grantSpawn(t, m, db, ctx, in)
	require.Error(t, e)
	require.Zero(t, seedCount(t, db, m.seed.names.SpawnGrants.String()))
	require.Zero(t, seedCount(t, db, "audit_probe"))
}

func TestSpawnGrantStepUpIsBoundToSupervisor(t *testing.T) {
	m, db, ctx, _, in := spawnGrantHarness(t)
	m.reauth = func(_ context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		require.NotNil(t, tx)
		require.EqualValues(t, 2, actor)
		require.Equal(t, "cookie", cookie)
		require.Equal(t, "proof", proof)
		require.Equal(t, "spawn_grant_manage", purpose)
		require.Equal(t, "agent:33", target)
		return nil
	}
	out, e := grantSpawn(t, m, db, ctx, in)
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", out.EffectiveState)
	require.Equal(t, "REQUIRE_APPROVAL", out.EffectiveApprovalMode)
}

func TestSpawnGrantCurrentSourceFailuresLeaveNoCapacityOrEvents(t *testing.T) {
	for _, scenario := range []string{"agent", "reread", "inactive_reread", "lock", "inventory", "identifier", "zero_identifier", "insert", "audit", "read", "foreign", "nontransaction", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, in := spawnGrantHarness(t)
			fault := errors.New("spawn grant source failed")
			want := fault
			switch scenario {
			case "agent", "reread", "inactive_reread":
				original := m.spawnDirectory.AgentOn
				calls := 0
				m.spawnDirectory.AgentOn = func(c context.Context, tx *gorm.DB, tenant, id guid.ID) (fleet.AgentIdentity, bool, error) {
					calls++
					if scenario == "agent" || calls == 2 {
						if scenario == "inactive_reread" {
							return fleet.AgentIdentity{}, false, nil
						}
						return fleet.AgentIdentity{}, false, fault
					}
					return original(c, tx, tenant, id)
				}
				if scenario == "inactive_reread" {
					want = ErrSpawnScope
				}
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "inventory":
				m.spawnDirectory.ModelOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (bool, error) { return false, fault }
			case "identifier":
				m.seed.ids = seedTestIDs{err: fault}
			case "zero_identifier":
				m.seed.ids = seedTestIDs{}
				want = ErrUnavailable
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:spawn-grant", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.SpawnGrants.String() {
						tx.AddError(fault)
					}
				}))
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:spawn-grant", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.SpawnGrants.String() {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "foreign":
				ctx = t.Context()
				want = ErrForbidden
			case "nontransaction":
				want = ErrInvalidArgument
			case "unavailable":
				m.principals.Lock = nil
				want = ErrUnavailable
			}
			var err error
			if scenario == "nontransaction" {
				_, err = m.grantSpawnOn(ctx, db, 1, in, "cookie")
			} else {
				_, err = grantSpawn(t, m, db, ctx, in)
			}
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Create().Remove("test:spawn-grant"))
			require.NoError(t, db.Callback().Query().Remove("test:spawn-grant"))
			require.Zero(t, seedCount(t, db, m.seed.names.SpawnGrants.String()))
			require.Zero(t, seedCount(t, db, "audit_probe"))
			require.Zero(t, seedCount(t, db, "event_probe"))
		})
	}
}
