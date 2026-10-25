package governance

import (
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetirementGovernanceRevokesDelegationAndReleasesChildWithoutSupervisor(t *testing.T) {
	m, db, human, _, in := spawnGrantHarness(t)
	in.ApprovalMode = "AUTO"
	grant, err := grantSpawn(t, m, db, human, in)
	require.NoError(t, err)
	supervisor := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 3})
	spawn := fleet.AgentSpawnInput{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Effort: "medium", EstimatedCapMicros: 1, Reason: "Bounded child for retirement", Registration: fleet.AgentRegistration{PrincipalID: 160, MachineID: 5, Runtime: "CODEX", Model: "model", Roles: []string{"EXECUTOR"}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.reserveSpawnOn(supervisor, tx, 1, 60, agentSpawnRequest(supervisor, 1, spawn))
	}))
	// A child can be retired by a Human even after its supervisor stops being
	// eligible. Retirement must never borrow the supervisor's identity.
	m.delegations.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 60, TenantID: tenant, PrincipalID: principal, State: "RETIRING", Origin: "AGENT_SPAWNED"}, true, nil
	}
	m.spawnDirectory.AgentOn = nil
	g := query.BindSpawnGrants(db, m.seed.names)
	require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id=?", grant.ID).Update("spent_micros", 123).Error)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.revokeRetiringAgentOn(human, tx, 1, 160, 900) }))
	}
	row, err := g.WithContext(human).Where(g.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Zero(t, row.ActiveCount)
	require.Equal(t, 1, row.SpawnedToday)
	require.EqualValues(t, 123, row.SpentMicros)
	members := query.BindSpawnGrantMembers(db, m.seed.names)
	member, err := members.WithContext(human).Where(members.AgentID.Eq(guid.ID(60))).First()
	require.NoError(t, err)
	require.Equal(t, "RELEASED", member.State)
	require.Equal(t, "RETIRED", *member.ReleaseReason)
}

func TestRetirementGovernanceCurrentAuthorityAndLateFailureRollback(t *testing.T) {
	for _, fault := range []string{"denied", "inactive_human", "foreign_tenant", "nontransaction", "live_agent", "publisher"} {
		t.Run(fault, func(t *testing.T) {
			m, db, human, pub, in := delegationHarness(t)
			delegation, err := grantDelegation(t, m, db, human, in)
			require.NoError(t, err)
			m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				state := "RETIRING"
				if fault == "live_agent" {
					state = "ACTIVE"
				}
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: state}, true, nil
			}
			ctx := human
			switch fault {
			case "denied":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, nil
				}
			case "inactive_human":
				m.delegations.members = delegationMembers{false}
			case "foreign_tenant":
				ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 2, PrincipalID: 2})
			case "publisher":
				pub.Fail = true
			}
			if fault == "nontransaction" {
				err = m.revokeRetiringAgentOn(ctx, db, 1, 3, 900)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error { return m.revokeRetiringAgentOn(ctx, tx, 1, 3, 900) })
			}
			require.Error(t, err)
			q := query.BindDelegations(db, m.seed.names)
			row, err := q.WithContext(human).Where(q.ID.Eq(delegation.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", row.State)
			var bindings int64
			require.NoError(t, db.Table("binding_probe").Count(&bindings).Error)
			require.Positive(t, bindings)
		})
	}
}

func TestRetirementGovernanceEndsActualDelegationAndSupervisorGrant(t *testing.T) {
	m, db, human, _, in := spawnGrantHarness(t)
	grant, err := grantSpawn(t, m, db, human, in)
	require.NoError(t, err)
	delegationInput := DelegationGrantInput{ProjectID: 99, GranteePrincipalID: 3, DelegationConditions: DelegationConditions{Actions: []string{"decision_approve"}, WorkItemTypes: []string{"TASK"}, MaxRisk: "LOW", CostCapMicros: 100, MaxRework: 3}, DurationMinutes: 60, Reason: "Approve low risk changes only", ReauthProof: "proof"}
	delegation, err := grantDelegation(t, m, db, human, delegationInput)
	require.NoError(t, err)
	m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "RETIRING"}, true, nil
	}
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.revokeRetiringAgentOn(human, tx, 1, 3, 900) }))
	}
	d := query.BindDelegations(db, m.seed.names)
	delegationRow, err := d.WithContext(human).Where(d.ID.Eq(delegation.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "REVOKED", delegationRow.State)
	require.Equal(t, "AGENT_RETIRED", *delegationRow.EndReason)
	g := query.BindSpawnGrants(db, m.seed.names)
	grantRow, err := g.WithContext(human).Where(g.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "REVOKED", grantRow.State)
	var count int64
	require.NoError(t, db.Table("binding_probe").Count(&count).Error)
	require.Zero(t, count)
}

func TestRetirementGovernanceMissingSpawnReservationFailsClosed(t *testing.T) {
	m, db, human, _, _ := spawnGrantHarness(t)
	m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 60, TenantID: 1, PrincipalID: 160, State: "RETIRING", Origin: "AGENT_SPAWNED"}, true, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.revokeRetiringAgentOn(human, tx, 1, 160, 900) }), ErrUnavailable)
}

func TestRetirementConnectorRevokesActualDelegationBeforeSubjectBindings(t *testing.T) {
	m, db, ctx, _, in := delegationHarness(t)
	row, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "RETIRING"}, true, nil
	}
	var callback func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
	services := module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentRetirementRevocationConnector](services, fleet.AgentRetirementRevocationConnector{Bind: func(name string, fn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error {
		require.Equal(t, Name, name)
		callback = fn
		return nil
	}}))
	fault := errors.New("subject binding revocation failed")
	calls := 0
	require.NoError(t, module.Provide[core.EntitySubjectBindingRevoker](services, func(c context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
		calls++
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, principal)
		q := query.BindDelegations(tx, m.seed.names)
		r, e := q.WithContext(c).Where(q.ID.Eq(row.ID)).First()
		require.NoError(t, e)
		require.Equal(t, "REVOKED", r.State)
		return fault
	}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.injectRetirement(services))
	require.NotNil(t, callback)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return callback(ctx, tx, 1, 3, 900) }), fault)
	require.Equal(t, 1, calls)
	q := query.BindDelegations(db, m.seed.names)
	r, err := q.WithContext(ctx).Where(q.ID.Eq(row.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", r.State)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return callback(ctx, tx, 1, 0, 900) }), ErrInvalidArgument)
	require.Equal(t, 1, calls)
}
func TestRetirementConnectorRequiresCompleteSealedCapabilities(t *testing.T) {
	m := &Module{}
	require.ErrorIs(t, m.injectRetirement(module.NewServices()), module.ErrServicesProviding)
	s := module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentRetirementRevocationConnector](s, fleet.AgentRetirementRevocationConnector{}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.injectRetirement(s), ErrUnavailable)
	s = module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentRetirementRevocationConnector](s, fleet.AgentRetirementRevocationConnector{Bind: func(string, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error) error {
		t.Fatal("must not bind without subject revoker")
		return nil
	}}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.injectRetirement(s), module.ErrServiceMissing)
}

func TestRetirementSourceFailurePreservesDelegationAndCapacity(t *testing.T) {
	for _, scenario := range []string{"agent", "subject_lock", "delegations", "grants", "members", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, in := delegationHarness(t)
			view, err := grantDelegation(t, m, db, ctx, in)
			require.NoError(t, err)
			fault := errors.New("retirement source failed")
			want := fault
			m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "RETIRING"}, true, nil
			}
			switch scenario {
			case "agent":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "subject_lock":
				m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error {
					if id == 3 {
						return fault
					}
					return nil
				}
			case "unavailable":
				m.publisher = nil
				want = ErrUnavailable
			case "delegations", "grants", "members":
				table := m.seed.names.Delegations.String()
				if scenario == "grants" {
					table = m.seed.names.SpawnGrants.String()
				}
				if scenario == "members" {
					table = m.seed.names.SpawnGrantMembers.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:retirement-source", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.revokeRetiringAgentOn(ctx, tx, 1, 3, 900) }), want)
			require.NoError(t, db.Callback().Query().Remove("test:retirement-source"))
			q := query.BindDelegations(db, m.seed.names)
			saved, err := q.WithContext(ctx).Where(q.ID.Eq(view.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", saved.State)
			require.Equal(t, 1, saved.Version)
			require.EqualValues(t, 1, seedCount(t, db, "binding_probe"))
		})
	}
}
