package governance

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

type delegationMembers struct{ active bool }
type delegationReplayFixture struct{}

func (delegationReplayFixture) Do(ctx context.Context, tx *gorm.DB, _ idempotency.Key, run func(*gorm.DB) (idempotency.Result, error)) (idempotency.Result, bool, error) {
	r, e := run(tx)
	return r, false, e
}
func (m delegationMembers) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return m.active, nil
}
func (m delegationMembers) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return m.active, nil
}

func delegationHarness(t *testing.T) (*Module, *gorm.DB, context.Context, *governanceTestPublisher, DelegationGrantInput) {
	t.Helper()
	m, db, ctx, pub := settingsHarness(t)
	m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: 2}, true, nil
	}
	m.delegations.members = delegationMembers{true}
	m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "ACTIVE", Origin: "HUMAN_CREATED", Roles: []string{"REVIEWER"}}, true, nil
	}
	m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
		return DelegationProjectState{Stage: "ACTIVE", Mode: "AUTONOMOUS", DelegatedAllowed: true}, nil
	}
	require.NoError(t, db.Exec("CREATE TABLE binding_probe(tenant INTEGER, project INTEGER, role INTEGER, principal INTEGER, ends DATETIME, UNIQUE(tenant,project,role,principal))").Error)
	m.delegations.bindings.PutOn = func(ctx context.Context, tx *gorm.DB, tenant, project, role, principal guid.ID, effect core.BindingEffect, ends time.Time) (core.EntityRoleBinding, bool, error) {
		require.Equal(t, core.BindingAllow, effect)
		return core.EntityRoleBinding{}, true, tx.WithContext(ctx).Exec("INSERT INTO binding_probe VALUES(?,?,?,?,?) ON CONFLICT(tenant,project,role,principal) DO UPDATE SET ends=excluded.ends", tenant, project, role, principal, ends).Error
	}
	m.delegations.grants.RemoveEntityRoleBinding = func(ctx context.Context, tx *gorm.DB, tenant, project, role, principal guid.ID) (bool, error) {
		return true, tx.WithContext(ctx).Exec("DELETE FROM binding_probe WHERE tenant=? AND project=? AND role=? AND principal=?", tenant, project, role, principal).Error
	}
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true, Reason: "administrator_grant"}, nil
	}
	q := query.BindRoleSeeds(db, m.seed.names)
	require.NoError(t, q.WithContext(ctx).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleID: 21, RoleKey: "delegated_decision_approve", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: m.seed.now(), UpdatedAt: m.seed.now()}))
	in := DelegationGrantInput{ProjectID: 99, GranteePrincipalID: 3, DelegationConditions: DelegationConditions{Actions: []string{"decision_approve"}, WorkItemTypes: []string{"TASK"}, MaxRisk: "LOW", CostCapMicros: 100, MaxRework: 3}, DurationMinutes: 60, Reason: "Approve low risk changes only", ReauthProof: "proof"}
	return m, db, ctx, pub, in
}
func grantDelegation(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, in DelegationGrantInput) (DelegationView, error) {
	t.Helper()
	var out DelegationView
	e := db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.grantDelegationOn(ctx, tx, 1, in, "cookie"); return e })
	return out, e
}
func TestDelegationGrantReplacementRevocationAndAtomicEvents(t *testing.T) {
	m, db, ctx, pub, in := delegationHarness(t)
	proofs := 0
	m.reauth = func(_ context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		require.NotNil(t, tx)
		require.EqualValues(t, 2, actor)
		require.Equal(t, "cookie", cookie)
		require.Equal(t, "proof", proof)
		require.Equal(t, "delegation_grant", purpose)
		require.Equal(t, "project:99:grantee:3", target)
		proofs++
		return nil
	}
	first, e := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", first.State)
	require.Equal(t, m.seed.now().Add(time.Hour), first.EndsAt)
	require.Equal(t, 1, proofs)
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "event_probe"))
	_, e = grantDelegation(t, m, db, ctx, in)
	require.ErrorIs(t, e, ErrVersion)
	in.ReplacesDelegationID = &first.ID
	pub.Fail = true
	_, e = grantDelegation(t, m, db, ctx, in)
	require.Error(t, e)
	q := query.BindDelegations(db, m.seed.names)
	old, e := q.WithContext(ctx).Where(q.ID.Eq(first.ID)).Take()
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", old.State)
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 1, seedCount(t, db, "audit_probe"))
	pub.Fail = false
	next, e := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, e)
	require.NotEqual(t, first.ID, next.ID)
	old, e = q.WithContext(ctx).Where(q.ID.Eq(first.ID)).Take()
	require.NoError(t, e)
	require.Equal(t, "REPLACED", *old.EndReason)
	require.Nil(t, old.ActiveKey)
	require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 3, seedCount(t, db, "audit_probe"))
	require.EqualValues(t, 3, seedCount(t, db, "event_probe"))
	var revoked DelegationView
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			revoked, e = m.revokeDelegationOn(ctx, tx, 1, next.ID, "Human took control")
			return e
		}))
		require.Equal(t, "REVOKED", revoked.State)
	}
	require.EqualValues(t, 0, seedCount(t, db, "binding_probe"))
	require.EqualValues(t, 4, seedCount(t, db, "audit_probe"))
	require.EqualValues(t, 4, seedCount(t, db, "event_probe"))
}

func TestDelegationGrantRechecksCurrentHumanAgentAndAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Module, *DelegationGrantInput)
	}{
		{"inactive member", func(m *Module, _ *DelegationGrantInput) { m.delegations.members = delegationMembers{false} }},
		{"agent unadopted", func(m *Module, _ *DelegationGrantInput) {
			m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "ACTIVE", Origin: "AGENT_SPAWNED", Roles: []string{"REVIEWER"}}, true, nil
			}
		}},
		{"no reviewer", func(m *Module, _ *DelegationGrantInput) {
			m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "ACTIVE", Roles: []string{"LEAD"}}, true, nil
			}
		}},
		{"denied", func(m *Module, _ *DelegationGrantInput) {
			m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Reason: "explicit_deny"}, nil
			}
		}},
		{"duration", func(_ *Module, in *DelegationGrantInput) { in.DurationMinutes = 8 * 1440 }},
		{"cost", func(_ *Module, in *DelegationGrantInput) { in.CostCapMicros = 50000001 }},
		{"human only action", func(_ *Module, in *DelegationGrantInput) { in.Actions = []string{"scope_change"} }},
		{"short reason", func(_ *Module, in *DelegationGrantInput) { in.Reason = "no" }},
		{"terminal project", func(m *Module, _ *DelegationGrantInput) {
			m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
				return DelegationProjectState{Stage: "CLOSED"}, nil
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			tc.mutate(m, &in)
			_, e := grantDelegation(t, m, db, ctx, in)
			require.Error(t, e)
			require.Zero(t, seedCount(t, db, "g_governance_delegations"))
			require.Zero(t, seedCount(t, db, "binding_probe"))
			require.Zero(t, seedCount(t, db, "audit_probe"))
		})
	}
}

func TestDelegationGrantSourceFailuresRollBackBindingsAndAudit(t *testing.T) {
	for _, scenario := range []string{"identifier", "zero_identifier", "binding", "insert", "audit", "human_error", "lock_error", "project_error", "agent_error", "settings_read", "active_read", "seed_read"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			fault := errors.New("delegation source failed")
			want := fault
			switch scenario {
			case "identifier":
				m.seed.ids = seedTestIDs{err: fault}
			case "zero_identifier":
				m.seed.ids = seedTestIDs{}
				want = ErrUnavailable
			case "binding":
				m.delegations.bindings.PutOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, core.BindingEffect, time.Time) (core.EntityRoleBinding, bool, error) {
					return core.EntityRoleBinding{}, false, fault
				}
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:delegation", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.Delegations.String() {
						tx.AddError(fault)
					}
				}))
			case "audit":
				m.seed.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "human_error":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "lock_error":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "project_error":
				m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
					return DelegationProjectState{}, fault
				}
			case "agent_error":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "settings_read", "active_read", "seed_read":
				table := m.seed.names.Settings.String()
				if scenario == "active_read" {
					table = m.seed.names.Delegations.String()
				}
				if scenario == "seed_read" {
					table = m.seed.names.RoleSeeds.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:delegation", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			}
			_, err := grantDelegation(t, m, db, ctx, in)
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Create().Remove("test:delegation"))
			require.NoError(t, db.Callback().Query().Remove("test:delegation"))
			for _, table := range []string{m.seed.names.Delegations.String(), "binding_probe", "event_probe", "audit_probe"} {
				require.Zero(t, seedCount(t, db, table), table)
			}
		})
	}
}
func TestDelegationRevocationSourceFailurePreservesActiveGrantAndBindings(t *testing.T) {
	for _, scenario := range []string{"remove", "update", "binding_json", "missing_remover", "inactive_state", "stale_version"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			view, err := grantDelegation(t, m, db, ctx, in)
			require.NoError(t, err)
			q := query.BindDelegations(db, m.seed.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(view.ID)).First()
			require.NoError(t, err)
			fault := errors.New("delegation removal failed")
			want := fault
			switch scenario {
			case "remove":
				m.delegations.grants.RemoveEntityRoleBinding = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) { return false, fault }
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:delegation-revoke", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.Delegations.String() {
						tx.AddError(fault)
					}
				}))
			case "binding_json":
				row.IAMBindings = "[]"
				want = ErrUnavailable
			case "missing_remover":
				m.delegations.grants.RemoveEntityRoleBinding = nil
				want = ErrUnavailable
			case "inactive_state":
				row.State = "REVOKED"
				want = ErrVersion
			case "stale_version":
				row.Version++
				want = ErrVersion
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				return m.endDelegationRowOn(ctx, tx, row, "REVOKED", "MANUAL", "Revoke current source delegation")
			}), want)
			require.NoError(t, db.Callback().Update().Remove("test:delegation-revoke"))
			saved, err := q.WithContext(ctx).Where(q.ID.Eq(view.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", saved.State)
			require.Equal(t, 1, saved.Version)
			require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
		})
	}
}
