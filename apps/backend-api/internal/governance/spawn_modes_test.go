package governance

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectModeReviewerRoleRequiresExactCurrentBinding(t *testing.T) {
	for _, scenario := range []string{"allowed", "wrong_role", "unavailable", "membership", "agent_missing", "agent_error", "inactive", "foreign", "unseeded", "explanation_error", "denied", "inherited", "other_project", "other_role"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := delegationHarness(t)
			q := query.BindRoleSeeds(db, m.seed.names)
			require.NoError(t, q.WithContext(ctx).Create(&model.RoleSeed{ID: 2, TenantID: 1, RoleID: 22, RoleKey: "agent_reviewer", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: m.seed.now(), UpdatedAt: m.seed.now()}))
			fault := errors.New("mode authority source unavailable")
			match := authz.DecisionMatch{RoleID: "22", Effect: "allow", SourceType: "entity_binding", ScopeType: "entity", ScopeID: "99"}
			m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{match}}, nil
			}
			role := "agent_reviewer"
			switch scenario {
			case "wrong_role":
				role = "tenant_admin"
			case "unavailable":
				m.explain = nil
			case "membership":
				m.delegations.members = delegationMembers{false}
			case "agent_missing":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
			case "agent_error":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "inactive", "foreign":
				m.delegations.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					a := fleet.AgentIdentity{ID: 33, TenantID: 1, PrincipalID: 3, State: "DISABLED"}
					if scenario == "foreign" {
						a.State = "ACTIVE"
						a.TenantID = 2
					}
					return a, true, nil
				}
			case "unseeded":
				_, err := q.WithContext(ctx).Where(q.RoleKey.Eq(role)).Delete()
				require.NoError(t, err)
			case "explanation_error":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, fault
				}
			case "denied":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, nil
				}
			case "inherited":
				match.Inherited = true
			case "other_project":
				match.ScopeID = "100"
			case "other_role":
				match.RoleID = "21"
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				allowed, err := m.modeAgentRoleOn(ctx, tx, 1, 99, 3, role)
				require.Equal(t, scenario == "allowed", allowed)
				return err
			})
			switch scenario {
			case "wrong_role":
				require.ErrorIs(t, err, ErrInvalidArgument)
			case "unavailable":
				require.ErrorIs(t, err, ErrUnavailable)
			case "unseeded":
				require.ErrorIs(t, err, ErrRoleNotSeeded)
			case "agent_error", "explanation_error":
				require.ErrorIs(t, err, fault)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestProjectModePreconditionsUseLongLivedActivatedReviewerDelegation(t *testing.T) {
	m, db, ctx, _, in := delegationHarness(t)
	in.DurationMinutes = 48 * 60
	grant, err := grantDelegation(t, m, db, ctx, in)
	require.NoError(t, err)
	require.NoError(t, query.BindRoleSeeds(db, m.seed.names).WithContext(ctx).Create(&model.RoleSeed{ID: 2, TenantID: 1, RoleID: 22, RoleKey: "agent_reviewer", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: m.seed.now(), UpdatedAt: m.seed.now()}))
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{RoleID: "22", Effect: "allow", SourceType: "entity_binding", ScopeType: "entity", ScopeID: "99"}}}, nil
	}
	check := func(locked []guid.ID, want bool, wantErr error) {
		t.Helper()
		err := db.Transaction(func(tx *gorm.DB) error {
			ready, missing, err := m.modeGovernancePreconditionsOn(ctx, tx, 1, 99, locked)
			if err == nil {
				require.Equal(t, want, ready)
				require.Empty(t, missing)
			}
			return err
		})
		if wantErr != nil {
			require.ErrorIs(t, err, wantErr)
		} else {
			require.NoError(t, err)
		}
	}
	check(nil, false, ErrVersion)
	check([]guid.ID{3}, true, nil)
	q := query.BindDelegations(db, m.seed.names)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).Update(q.ActivatedAt, nil)
	require.NoError(t, err)
	check([]guid.ID{3}, false, nil)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).Update(q.ActivatedAt, m.seed.now())
	require.NoError(t, err)
	_, err = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).Update(q.ConditionsJSON, `{"actions":["decision_reject"]}`)
	require.NoError(t, err)
	check([]guid.ID{3}, false, nil)
}

func TestProjectModeChangeRevokesDelegationsUnderCompletePrincipalFence(t *testing.T) {
	for _, permission := range []string{"project_takeover", "project_mode_change"} {
		t.Run(permission, func(t *testing.T) {
			m, db, ctx, pub, in := delegationHarness(t)
			grant, err := grantDelegation(t, m, db, ctx, in)
			require.NoError(t, err)
			call := func(locked []guid.ID) ([]guid.ID, error) {
				var ids []guid.ID
				err := db.Transaction(func(tx *gorm.DB) error {
					var err error
					ids, err = m.revokeModeDelegationsOn(ctx, tx, 1, 99, permission, "Human changes project authority", locked)
					return err
				})
				return ids, err
			}
			_, err = call(nil)
			require.ErrorIs(t, err, ErrVersion)
			pub.Fail = true
			_, err = call([]guid.ID{3})
			require.Error(t, err)
			q := query.BindDelegations(db, m.seed.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", row.State)
			pub.Fail = false
			ids, err := call([]guid.ID{3})
			require.NoError(t, err)
			require.Equal(t, []guid.ID{grant.ID}, ids)
			row, err = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "REVOKED", row.State)
			reason := "MODE_CHANGED"
			if permission == "project_takeover" {
				reason = "TAKEOVER"
			}
			require.Equal(t, reason, *row.EndReason)
			require.Zero(t, seedCount(t, db, "binding_probe"))
		})
	}
}

func TestAgentSpawnUsesActualGrantBudgetAndRelease(t *testing.T) {
	m, db, human, _, grantInput := spawnGrantHarness(t)
	grantInput.ApprovalMode = "AUTO"
	grantInput.MaxActiveAgents = 2
	grant, err := grantSpawn(t, m, db, human, grantInput)
	require.NoError(t, err)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 3})
	in := fleet.AgentSpawnInput{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Effort: "medium", EstimatedCapMicros: 1000000, Reason: "Bounded automated implementation", Registration: fleet.AgentRegistration{PrincipalID: 160, MachineID: 5, Runtime: "CODEX", Model: "model", Roles: []string{"EXECUTOR"}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.agentSpawnFenceOn(ctx, tx, 1, 33))
		fact, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, false)
		require.NoError(t, err)
		require.Equal(t, grant.ID, fact.GrantID)
		require.EqualValues(t, 2, fact.OwnerPrincipalID)
		require.NoError(t, m.reserveSpawnOn(ctx, tx, 1, 60, agentSpawnRequest(ctx, 1, in)))
		return nil
	}))
	q := query.BindSpawnGrants(db, m.seed.names)
	r, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, 1, r.ActiveCount)
	// Actual settled spend plus outstanding child caps may exhaust remaining
	// reservation budget before cumulative spend alone reaches the cap.
	require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id=?", grant.ID).Update("spent_micros", 1000001).Error)
	require.NoError(t, db.Table(m.seed.names.SpawnGrantMembers.String()).Where("agent_id=?", 60).Update("spent_micros", 1000001).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, false); return err }), ErrSpawnBudget)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, true); return err }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.agentSpawnRetireOn(ctx, tx, 1, 33, 60, grant.ID, true) }))
	r, err = q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Zero(t, r.ActiveCount)
	require.EqualValues(t, 1000001, r.SpentMicros, "retirement releases capacity, never refunds actual cost")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, false); return err }), ErrSpawnBudget)
	require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("id=?", grant.ID).Updates(map[string]any{"state": "REVOKED", "active_key": nil, "ended_at": m.seed.now(), "end_reason": "HUMAN_REVOKED"}).Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, true); return err }), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.agentSpawnRetireOn(ctx, tx, 1, 33, 60, grant.ID, false) }), ErrForbidden)
}

func TestAgentSpawnRefusesApprovalDowngradeAndPrivilegeExpansion(t *testing.T) {
	m, db, human, _, grantInput := spawnGrantHarness(t)
	grantInput.ApprovalMode = "AUTO"
	_, err := grantSpawn(t, m, db, human, grantInput)
	require.NoError(t, err)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 3})
	in := fleet.AgentSpawnInput{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Effort: "medium", EstimatedCapMicros: 1000000, Reason: "Bounded implementation capacity", Registration: fleet.AgentRegistration{PrincipalID: 160, MachineID: 5, Runtime: "CODEX", Model: "model", Roles: []string{"EXECUTOR"}}}
	call := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, false); return err })
	}
	m.explain = func(_ context.Context, _ *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: req.PrincipalID == 160}, nil
	}
	require.ErrorIs(t, call(), ErrForbidden)
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true}, nil
	}
	m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
		return DelegationProjectState{Stage: "ACTIVE", Mode: "HYBRID"}, nil
	}
	require.ErrorIs(t, call(), ErrForbidden)
	m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
		return DelegationProjectState{Stage: "ACTIVE", Mode: "AUTONOMOUS"}, nil
	}
	in.ProjectIDs = []guid.ID{100}
	require.ErrorIs(t, call(), ErrForbidden)
}

func TestSpawnOutstandingCapsAndDailyUsageSurviveRelease(t *testing.T) {
	m, db, human, _, grantInput := spawnGrantHarness(t)
	grantInput.ApprovalMode = "AUTO"
	grantInput.MaxActiveAgents, grantInput.MaxSpawnsPerDay = 3, 5
	grant, err := grantSpawn(t, m, db, human, grantInput)
	require.NoError(t, err)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 3})
	in := fleet.AgentSpawnInput{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Effort: "medium", EstimatedCapMicros: 1, Reason: "The grant reserves the full child cap", Registration: fleet.AgentRegistration{PrincipalID: 160, MachineID: 5, Runtime: "CODEX", Model: "model", Roles: []string{"EXECUTOR"}}}
	for _, child := range []guid.ID{60, 61} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, false)
			if err != nil {
				return err
			}
			return m.reserveSpawnOn(ctx, tx, 1, child, agentSpawnRequest(ctx, 1, in))
		}))
	}
	// A tiny submitted estimate cannot bypass two full outstanding child caps.
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, false); return err }), ErrSpawnBudget)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, true); return err }))
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.agentSpawnRetireOn(ctx, tx, 1, 33, 60, grant.ID, true) }))
	}
	q := query.BindSpawnGrants(db, m.seed.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
	require.NoError(t, err)
	require.Equal(t, 1, row.ActiveCount, "retirement replay releases the reservation once")
	require.Equal(t, 2, row.SpawnedToday, "retirement cannot restore the daily creation allowance")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.agentSpawnAuthorizeOn(ctx, tx, 1, in, false); return err }))
}

func TestAndonUsesCurrentDirectGrantWithoutDelegation(t *testing.T) {
	seed, db, rt := seedHarnessRuntime(t)
	require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "andon", Verb: "pull", Scope: authz.ActionScopeTenant}, {Resource: "andon", Verb: "clear", Scope: authz.ActionScopeTenant}}, nil))
	require.NoError(t, query.BindRoleSeeds(db, seed.names).WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleID: 21, RoleKey: "agent_executor", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: seed.now(), UpdatedAt: seed.now()}))
	allowed := true
	reason := ""
	m := &Module{runtime: rt, seed: seed, explain: func(_ context.Context, tx *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 99, req.EntityID)
		return authz.Explanation{Allowed: allowed, Reason: reason, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: req.Permission, Effect: "allow"}}}, nil
	}}
	req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 11, ActorKind: "AGENT", Code: "andon_pull", Resource: ResourceRef{Kind: "work_item", ID: 77}}
	check := func(want bool) {
		t.Helper()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			a, err := m.andonAuthorityOn(t.Context(), tx, req)
			require.NoError(t, err)
			require.Equal(t, want, a.Allowed)
			require.Zero(t, a.DelegationID)
			if want {
				require.Equal(t, "ROLE:agent_executor", a.AuthoritySource)
			}
			return nil
		}))
	}
	check(true)
	allowed = false
	check(false)
	allowed, reason = true, "explicit_deny"
	check(false)
	reason = ""
	req.Code = "andon_clear"
	check(false)
	req.ActorKind = "HUMAN"
	check(true)
	_, err := m.andonAuthorityOn(t.Context(), db, req)
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestEnsembleInboxFiltersCurrentAuthorityBeforePagination(t *testing.T) {
	m, db, ctx, _ := settingsHarness(t)
	lead := true
	require.NoError(t, query.BindRoleSeeds(db, m.seed.names).WithContext(ctx).Create(&model.RoleSeed{ID: 90, TenantID: 1, RoleID: 21, RoleKey: "delivery_lead", AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: m.seed.now(), UpdatedAt: m.seed.now()}))
	m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		if !lead {
			return authz.Explanation{Allowed: true}, nil
		}
		return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{RoleID: "21", Effect: "allow"}}}, nil
	}
	s := module.NewServices()
	allowed := true
	require.NoError(t, module.Provide[fleet.EnsembleDecisionFacts](s, fleet.EnsembleDecisionFacts{
		PendingOn: func(_ context.Context, tx *gorm.DB, tenant, project guid.ID) ([]fleet.EnsemblePendingDecision, error) {
			require.EqualValues(t, 1, tenant)
			require.EqualValues(t, 3, project)
			_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
			require.True(t, ok)
			return []fleet.EnsemblePendingDecision{{ID: 6, ProjectID: 3, WorkItemID: 20, Kind: "ENSEMBLE_APPROVAL", CreatedAt: time.Unix(0, 0)}, {ID: 8, ProjectID: 3, WorkItemID: 20, Kind: "ENSEMBLE_APPROVAL", CreatedAt: time.Unix(2, 0)}, {ID: 7, ProjectID: 3, WorkItemID: 20, Kind: "ENSEMBLE_APPROVAL", CreatedAt: time.Unix(1, 0)}, {ID: 9, ProjectID: 3, WorkItemID: 20, Kind: "ENSEMBLE_SELECTION", CreatedAt: time.Unix(3, 0)}}, nil
		},
		EligibleOn: func(_ context.Context, _ *gorm.DB, tenant, id, actor guid.ID) (bool, error) {
			require.EqualValues(t, 2, actor)
			return allowed && id != 6, nil
		},
	}))
	require.NoError(t, m.provideSpawnModes(s))
	require.NoError(t, s.Seal())
	req := InboxRequest{TenantID: 1, PrincipalID: 2, ProjectID: 3, Kind: "ENSEMBLE_APPROVAL", Limit: 1}
	reader := m.inbox.sources[req.Kind]
	rows, err := reader.Read(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 7, rows[0].ID)
	require.Equal(t, "work_item", rows[0].ObjectType)
	req.After = &rows[0].InboxKey
	rows, err = reader.Read(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 8, rows[0].ID)
	allowed = false
	rows, err = reader.Read(ctx, req)
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = reader.Read(context.Background(), req)
	require.ErrorIs(t, err, ErrForbidden)
	req.Kind = "ENSEMBLE_SELECTION"
	req.After = nil
	allowed = true
	rows, err = m.inbox.sources[req.Kind].Read(ctx, req)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 9, rows[0].ID)
	lead = false
	rows, err = m.inbox.sources[req.Kind].Read(ctx, req)
	require.NoError(t, err)
	require.Empty(t, rows, "assignment authority alone cannot receive selection decisions")
}

func TestAgentSpawnConnectorBindsPolicyAndPreservesFleetErrorContract(t *testing.T) {
	m, db, ctx, _, _ := spawnGrantHarness(t)
	var policy fleet.AgentSpawnPolicy
	services := module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentSpawnConnector](services, fleet.AgentSpawnConnector{Bind: func(p fleet.AgentSpawnPolicy) error { policy = p; return nil }}))
	services.Seal()
	require.NoError(t, m.bindAgentSpawn(services))
	require.NotNil(t, policy.FenceOn)
	require.NotNil(t, policy.AuthorizeOn)
	require.NotNil(t, policy.ReserveOn)
	require.NotNil(t, policy.RetireOn)
	require.ErrorIs(t, policy.FenceOn(ctx, nil, 1, 33), fleet.ErrInvalidArgument)
	_, err := policy.AuthorizeOn(ctx, db, 1, fleet.AgentSpawnInput{}, false)
	require.ErrorIs(t, err, fleet.ErrInvalidArgument)
	require.ErrorIs(t, policy.ReserveOn(ctx, nil, 1, 60, fleet.AgentSpawnInput{}), fleet.ErrInvalidArgument)
	require.ErrorIs(t, policy.RetireOn(ctx, nil, 1, 33, 60, 7, true), fleet.ErrInvalidArgument)
	for _, pair := range [][2]error{{ErrForbidden, fleet.ErrMachineForbidden}, {ErrInvalidArgument, fleet.ErrInvalidArgument}, {ErrSpawnScope, fleet.ErrInvalidArgument}, {ErrSpawnBudget, fleet.ErrVersionConflict}, {ErrSpawnQuota, fleet.ErrVersionConflict}, {ErrVersion, fleet.ErrVersionConflict}, {ErrUnavailable, fleet.ErrUnavailable}} {
		require.ErrorIs(t, spawnFleetError(fmt.Errorf("policy: %w", pair[0])), pair[1])
	}
	fault := errors.New("policy storage failed")
	require.ErrorIs(t, spawnFleetError(fault), fault)
	require.NoError(t, spawnFleetError(nil))
}
func TestAgentSpawnConnectorRejectsInvalidRegistryAndProvider(t *testing.T) {
	m := &Module{}
	require.ErrorIs(t, m.bindAgentSpawn(module.NewServices()), module.ErrServicesProviding)
	services := module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentSpawnConnector](services, fleet.AgentSpawnConnector{}))
	services.Seal()
	require.ErrorIs(t, m.bindAgentSpawn(services), ErrUnavailable)
	fault := errors.New("fleet policy already bound")
	services = module.NewServices()
	require.NoError(t, module.Provide[fleet.AgentSpawnConnector](services, fleet.AgentSpawnConnector{Bind: func(fleet.AgentSpawnPolicy) error { return fault }}))
	services.Seal()
	require.ErrorIs(t, m.bindAgentSpawn(services), fault)
}

func TestAgentSpawnRetirementRechecksAuthorityAndPreservesReservationOnFailure(t *testing.T) {
	for _, scenario := range []string{"lock", "inactive", "member", "agent", "supervisor", "explain", "explicit_deny", "grant_read", "member_read", "member_absent", "unavailable", "release_false"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, human, _, in := spawnGrantHarness(t)
			in.ApprovalMode = "AUTO"
			grant, err := grantSpawn(t, m, db, human, in)
			require.NoError(t, err)
			ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 3})
			input := fleet.AgentSpawnInput{SupervisorAgentID: 33, ProjectIDs: []guid.ID{99}, Effort: "medium", EstimatedCapMicros: 1, Registration: fleet.AgentRegistration{PrincipalID: 160, MachineID: 5, Runtime: "CODEX", Model: "model", Roles: []string{"EXECUTOR"}}}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.reserveSpawnOn(ctx, tx, 1, 60, agentSpawnRequest(ctx, 1, input)) }))
			fault := errors.New("retirement source failed")
			want := fault
			release := true
			child := guid.ID(60)
			switch scenario {
			case "lock":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "inactive":
				m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, nil
				}
				want = ErrForbidden
			case "member":
				m.delegations.members = delegationMembers{false}
				want = ErrForbidden
			case "agent":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, fault
				}
			case "supervisor":
				m.spawnDirectory.AgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
					return fleet.AgentIdentity{}, false, nil
				}
				want = ErrForbidden
			case "explain":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, fault
				}
			case "explicit_deny":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{Reason: "explicit_deny"}, nil
				}
				want = ErrForbidden
			case "grant_read", "member_read":
				table := m.seed.names.SpawnGrants.String()
				if scenario == "member_read" {
					table = m.seed.names.SpawnGrantMembers.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:spawn-retire", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
			case "member_absent":
				child = 61
				want = ErrForbidden
			case "unavailable":
				m.explain = nil
				want = ErrUnavailable
			case "release_false":
				release = false
				want = nil
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.agentSpawnRetireOn(ctx, tx, 1, 33, child, grant.ID, release) }), want)
			require.NoError(t, db.Callback().Query().Remove("test:spawn-retire"))
			q := query.BindSpawnGrants(db, m.seed.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(grant.ID)).First()
			require.NoError(t, err)
			require.Equal(t, 1, row.ActiveCount)
			require.Equal(t, 1, row.SpawnedToday)
		})
	}
}
func TestAgentSpawnFenceFailurePreventsSourceAccess(t *testing.T) {
	for _, scenario := range []string{"nontransaction", "unavailable", "missing", "query", "scope", "project"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, human, _, in := spawnGrantHarness(t)
			_, err := grantSpawn(t, m, db, human, in)
			require.NoError(t, err)
			ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 3})
			fault := errors.New("spawn fence source failed")
			want := fault
			switch scenario {
			case "nontransaction":
				want = ErrInvalidArgument
			case "unavailable":
				m.delegations.projects.InspectOn = nil
				want = ErrUnavailable
			case "missing":
				ctx = authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 4})
				want = ErrForbidden
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:spawn-fence", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.SpawnGrants.String() {
						tx.AddError(fault)
					}
				}))
			case "scope":
				require.NoError(t, db.Table(m.seed.names.SpawnGrants.String()).Where("tenant_id=?", 1).Update("scope_json", "{}").Error)
				want = ErrUnavailable
			case "project":
				m.delegations.projects.InspectOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (DelegationProjectState, error) {
					return DelegationProjectState{}, fault
				}
			}
			if scenario == "nontransaction" {
				err = m.agentSpawnFenceOn(ctx, db, 1, 33)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error { return m.agentSpawnFenceOn(ctx, tx, 1, 33) })
			}
			require.ErrorIs(t, err, want)
		})
	}
}

func TestAndonCurrentAuthoritySourceFailureNeverGrantsSafetyClear(t *testing.T) {
	for _, scenario := range []string{"environment", "unavailable", "unregistered", "explanation", "roles", "no_direct"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := delegationHarness(t)
			require.NoError(t, m.seed.registry.Contribute([]authz.Action{{Resource: "andon", Verb: "clear", Scope: authz.ActionScopeTenant}}, nil))
			req := AuthorityRequest{TenantID: 1, ProjectID: 99, PrincipalID: 2, ActorKind: "HUMAN", Code: "andon_clear", Resource: ResourceRef{Kind: "project", ID: 99}}
			fault := errors.New("andon authority source failed")
			want := fault
			switch scenario {
			case "environment":
				req.Environment = "PRODUCTION"
				want = ErrUnavailable
			case "unavailable":
				m.explain = nil
				want = ErrUnavailable
			case "unregistered":
				m.seed.registry, _ = authz.NewRegistry()
				want = ErrUnavailable
			case "explanation":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, fault
				}
			case "roles", "no_direct":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{RoleID: "21", Effect: "allow", PermissionCode: "andon_clear"}}}, nil
				}
				if scenario == "roles" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:andon", func(tx *gorm.DB) {
						if tx.Statement.Table == m.seed.names.RoleSeeds.String() {
							tx.AddError(fault)
						}
					}))
				} else {
					want = nil
				}
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				out, err := m.andonAuthorityOn(ctx, tx, req)
				require.ErrorIs(t, err, want)
				require.False(t, out.Allowed)
				if want == nil {
					require.Equal(t, "NO_DIRECT_AUTHORITY", out.ReasonCode)
				}
				return nil
			}))
		})
	}
}
