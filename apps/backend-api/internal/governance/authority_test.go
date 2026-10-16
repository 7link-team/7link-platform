package governance

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/governance/query"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRoleAuthorityUsesSeedIdentityAndStablePriority(t *testing.T) {
	service, db := seedHarness(t)
	table := query.BindRoleSeeds(db, service.names)
	for i, key := range []string{"team_owner", "delivery_lead", "engineer", "agent_reviewer", "delegated_decision_approve", "production_operator"} {
		row := model.RoleSeed{ID: guid.ID(i + 1), TenantID: 1, RoleKey: key, RoleID: guid.ID(100 + i), AppliedCodes: "[]", SkippedCodes: "[]", Managed: false, Version: 1, CreatedAt: service.now(), UpdatedAt: service.now()}
		require.NoError(t, table.WithContext(t.Context()).Create(&row))
	}
	grant := func(id string) authz.DecisionMatch {
		return authz.DecisionMatch{RoleID: id, PermissionCode: "decision_approve", Effect: "allow", SourceType: "role_member"}
	}
	for _, tc := range []struct {
		name     string
		matches  []authz.DecisionMatch
		source   string
		carriers int
	}{
		{"detached owner", []authz.DecisionMatch{grant("102"), grant("100")}, "ROLE:team_owner", 0},
		{"lead over engineer", []authz.DecisionMatch{grant("102"), grant("101")}, "ROLE:delivery_lead", 0},
		{"engineer over agent", []authz.DecisionMatch{grant("103"), grant("102")}, "ROLE:engineer", 0},
		{"agent over custom", []authz.DecisionMatch{grant("2"), grant("103")}, "ROLE:agent_reviewer", 0},
		{"numeric custom order", []authz.DecisionMatch{grant("11"), grant("2")}, "ROLE:custom.2", 0},
		{"carrier only", []authz.DecisionMatch{grant("104")}, "", 1},
		{"carrier plus real role", []authz.DecisionMatch{grant("104"), grant("102")}, "ROLE:engineer", 1},
		{"other seeded identity", []authz.DecisionMatch{grant("105")}, "ROLE:production_operator", 0},
		{"nonrole relation", []authz.DecisionMatch{{PermissionCode: "decision_approve", Effect: "allow", SourceType: "relationship"}}, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := service.roleAuthoritySource(t.Context(), db, 1, "decision_approve", authz.Explanation{Allowed: true, Reason: "permission_grant", Matches: tc.matches})
			require.NoError(t, err)
			require.Equal(t, tc.source, actual.source)
			require.Len(t, actual.carriers, tc.carriers)
			for _, match := range actual.carriers {
				require.Equal(t, "104", match.RoleID)
			}
		})
	}
	admin := grant("9")
	admin.PermissionCode = "tenant_administer"
	for _, tc := range []struct {
		matches []authz.DecisionMatch
		source  string
	}{
		{[]authz.DecisionMatch{admin, grant("102")}, "ROLE:tenant_administrator"},
		{[]authz.DecisionMatch{admin, grant("101")}, "ROLE:delivery_lead"},
		{nil, "ROLE:tenant_administrator"},
	} {
		actual, err := service.roleAuthoritySource(t.Context(), db, 1, "decision_approve", authz.Explanation{Allowed: true, Reason: "administrator_grant", Matches: tc.matches})
		require.NoError(t, err)
		require.Equal(t, tc.source, actual.source)
	}
	// Even a misconfigured carrier with tenant_administer remains only a carrier.
	admin.RoleID = "104"
	actual, err := service.roleAuthoritySource(t.Context(), db, 1, "decision_approve", authz.Explanation{Allowed: true, Reason: "administrator_grant", Matches: []authz.DecisionMatch{admin}})
	require.NoError(t, err)
	require.Empty(t, actual.source)
	require.Len(t, actual.carriers, 1)
}

func TestRoleAuthorityDoesNotInventGrantsOrCrossTenantIdentity(t *testing.T) {
	service, db := seedHarness(t)
	grant := authz.DecisionMatch{RoleID: "21", PermissionCode: "project_manage", Effect: "allow"}
	table := query.BindRoleSeeds(db, service.names)
	require.NoError(t, table.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 2, RoleKey: "team_owner", RoleID: 21, AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: service.now(), UpdatedAt: service.now()}))
	allowed := authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{grant}}
	actual, err := service.roleAuthoritySource(t.Context(), db, 1, "project_manage", allowed)
	require.NoError(t, err)
	require.Equal(t, "ROLE:custom.21", actual.source)
	require.NoError(t, table.WithContext(t.Context()).Create(&model.RoleSeed{ID: 2, TenantID: 1, RoleKey: "team_owner", RoleID: 21, SeedConflict: true, AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: service.now(), UpdatedAt: service.now()}))
	actual, err = service.roleAuthoritySource(t.Context(), db, 1, "project_manage", allowed)
	require.NoError(t, err)
	require.Equal(t, "ROLE:custom.21", actual.source)
	for _, reason := range []string{"explicit_deny", "administrator_scope_denied"} {
		blocked := allowed
		blocked.Reason = reason
		actual, err = service.roleAuthoritySource(t.Context(), nil, 1, "project_manage", blocked)
		require.NoError(t, err)
		require.Empty(t, actual.source)
	}
	blocked := allowed
	blocked.Allowed = false
	actual, err = service.roleAuthoritySource(t.Context(), nil, 1, "project_manage", blocked)
	require.NoError(t, err)
	require.Empty(t, actual.source)
	for _, field := range []string{"effect", "permission"} {
		unrelated := grant
		if field == "effect" {
			unrelated.Effect = "deny"
		} else {
			unrelated.PermissionCode = "role_view"
		}
		actual, err = service.roleAuthoritySource(t.Context(), db, 1, "project_manage", authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{unrelated}})
		require.NoError(t, err)
		require.Empty(t, actual.source)
	}
	// The caller's transaction sees its ledger changes and rollback restores them.
	aborted := errors.New("business transaction aborted")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		rows := query.BindRoleSeeds(tx, service.names)
		_, err := rows.WithContext(t.Context()).Where(rows.TenantID.Eq(guid.ID(1))).Update(rows.SeedConflict, false)
		require.NoError(t, err)
		actual, err := service.roleAuthoritySource(t.Context(), tx, 1, "project_manage", allowed)
		require.NoError(t, err)
		require.Equal(t, "ROLE:team_owner", actual.source)
		return aborted
	}), aborted)
	actual, err = service.roleAuthoritySource(t.Context(), db, 1, "project_manage", allowed)
	require.NoError(t, err)
	require.Equal(t, "ROLE:custom.21", actual.source)
}

func TestRoleAuthorityFailsOnUnavailableOrInvalidEvidence(t *testing.T) {
	service, db := seedHarness(t)
	grant := authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{RoleID: "12", PermissionCode: "project_manage", Effect: "allow"}}}
	_, err := service.roleAuthoritySource(t.Context(), db, 0, "project_manage", grant)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = service.roleAuthoritySource(t.Context(), db, 1, "", grant)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = (*seedService)(nil).roleAuthoritySource(t.Context(), db, 1, "project_manage", grant)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = service.roleAuthoritySource(t.Context(), nil, 1, "project_manage", grant)
	require.ErrorIs(t, err, ErrUnavailable)
	invalid := grant
	invalid.Matches = []authz.DecisionMatch{{RoleID: "invalid", PermissionCode: "project_manage", Effect: "allow"}}
	_, err = service.roleAuthoritySource(t.Context(), db, 1, "project_manage", invalid)
	require.ErrorIs(t, err, ErrInvalidArgument)
	table := query.BindRoleSeeds(db, service.names)
	require.NoError(t, table.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleKey: "unknown", RoleID: 12, AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: service.now(), UpdatedAt: service.now()}))
	_, err = service.roleAuthoritySource(t.Context(), db, 1, "project_manage", grant)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Migrator().DropTable(service.names.RoleSeeds.String()))
	actual, err := service.roleAuthoritySource(t.Context(), db, 1, "project_manage", grant)
	require.Error(t, err)
	require.Empty(t, actual.source)
}

func TestProjectCreationAuthorityEnforcesHumanRoleAndNondelegableGate(t *testing.T) {
	for _, tc := range []struct {
		name, kind, iamReason, wantReason string
		allowed                           bool
		roleKey                           string
		wantSource                        string
	}{
		{name: "owner", kind: "HUMAN", allowed: true, roleKey: "team_owner", wantSource: "ROLE:team_owner"},
		{name: "custom role", kind: "HUMAN", allowed: true, wantSource: "ROLE:custom.21"},
		{name: "agent owner", kind: "AGENT", allowed: true, roleKey: "team_owner", wantReason: "NON_DELEGABLE"},
		{name: "machine owner", kind: "MACHINE", allowed: true, roleKey: "team_owner", wantReason: "NON_DELEGABLE"},
		{name: "inactive member", kind: "HUMAN", iamReason: "inactive_membership", wantReason: "PRINCIPAL_INACTIVE"},
		{name: "inactive tenant", kind: "HUMAN", iamReason: "inactive_tenant", wantReason: "PRINCIPAL_INACTIVE"},
		{name: "explicit deny", kind: "HUMAN", iamReason: "explicit_deny", wantReason: "EXPLICIT_DENY"},
		{name: "admin deny", kind: "HUMAN", iamReason: "administrator_scope_denied", wantReason: "EXPLICIT_DENY"},
		{name: "no role", kind: "HUMAN", iamReason: "no_matching_grant", wantReason: "NON_DELEGABLE"},
		{name: "carrier cannot create", kind: "HUMAN", allowed: true, roleKey: "delegated_decision_approve", wantReason: "NON_DELEGABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed, db, rt := seedHarnessRuntime(t)
			require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant, DataScoped: true}}, nil))
			if tc.roleKey != "" {
				table := query.BindRoleSeeds(db, seed.names)
				require.NoError(t, table.WithContext(t.Context()).Create(&model.RoleSeed{ID: 1, TenantID: 1, RoleKey: tc.roleKey, RoleID: 21, AppliedCodes: "[]", SkippedCodes: "[]", CreatedAt: seed.now(), UpdatedAt: seed.now()}))
			}
			owner := &Module{runtime: rt, seed: seed}
			calls := 0
			for _, preview := range []bool{true, false} {
				run := func(tx *gorm.DB) error {
					owner.explain = func(_ context.Context, actual *gorm.DB, req core.AuthorizationExplanationRequest) (authz.Explanation, error) {
						calls++
						require.Same(t, tx, actual)
						require.Equal(t, core.AuthorizationExplanationRequest{TenantID: 1, PrincipalID: 11, Permission: "project_manage"}, req)
						return authz.Explanation{Allowed: tc.allowed, Reason: tc.iamReason, Revision: 7, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: "project_manage", Effect: "allow"}}}, nil
					}
					result, err := owner.checkProjectCreate(t.Context(), tx, 1, 11, tc.kind)
					require.NoError(t, err)
					require.Equal(t, tc.wantSource != "", result.Allowed)
					require.Equal(t, tc.wantSource, result.AuthoritySource)
					require.Equal(t, tc.wantReason, result.ReasonCode)
					require.EqualValues(t, 7, result.PolicyRevision)
					require.Zero(t, result.DelegationID)
					require.NotEmpty(t, result.Steps)
					last := result.Steps[len(result.Steps)-1]
					if result.Allowed {
						require.Equal(t, "S4", last.Step)
						require.Equal(t, "MATCH", last.Outcome)
					} else {
						require.Equal(t, "DENY", last.Outcome)
					}
					for _, step := range result.Steps {
						require.NotContains(t, []string{"S6", "S7", "S8"}, step.Step, "new projects cannot reach delegation or policy fallbacks")
					}
					return nil
				}
				if preview {
					require.NoError(t, run(nil))
				} else {
					require.NoError(t, db.Transaction(run))
				}
			}
			require.Equal(t, 2, calls)
			require.EqualValues(t, 0, seedCount(t, db, "audit_probe"), "authority preflight must not produce a business audit")
		})
	}
}

func TestProjectCreationAuthorityRejectsInvalidInputAndProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		tenant, principal guid.ID
		kind              string
	}{{0, 11, "HUMAN"}, {1, 0, "HUMAN"}, {1, 11, "unknown"}, {1, 11, "SYSTEM"}} {
		decision, err := (*Module)(nil).checkProjectCreate(t.Context(), nil, tc.tenant, tc.principal, tc.kind)
		require.NoError(t, err)
		require.False(t, decision.Allowed)
		require.Equal(t, "INVALID_REQUEST", decision.ReasonCode)
	}
	_, err := (*Module)(nil).checkProjectCreate(t.Context(), nil, 1, 11, "HUMAN")
	require.ErrorIs(t, err, ErrUnavailable)
	seed, db, rt := seedHarnessRuntime(t)
	failure := errors.New("IAM read failed")
	owner := &Module{runtime: rt, seed: seed, explain: func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{}, failure
	}}
	_, err = owner.checkProjectCreate(t.Context(), nil, 1, 11, "HUMAN")
	require.ErrorIs(t, err, ErrUnavailable, "permission owner must contribute project_manage")
	require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant}}, nil))
	decision, err := owner.checkProjectCreate(t.Context(), nil, 1, 11, "HUMAN")
	require.ErrorIs(t, err, failure)
	require.Zero(t, decision)
	owner.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{RoleID: "21", PermissionCode: "project_manage", Effect: "allow"}}}, nil
	}
	owner.runtime = nil
	decision, err = owner.checkProjectCreate(t.Context(), nil, 1, 11, "HUMAN")
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, decision)
	require.NoError(t, db.Migrator().DropTable(seed.names.RoleSeeds.String()))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		decision, err = owner.checkProjectCreate(t.Context(), tx, 1, 11, "HUMAN")
		require.Error(t, err)
		require.Zero(t, decision)
		return nil
	}))
}

func TestAuthorityDispatcherRejectsInvalidOrUnimplementedContexts(t *testing.T) {
	seed, _, rt := seedHarnessRuntime(t)
	owner := &Module{runtime: rt, seed: seed}
	base := AuthorityRequest{TenantID: 1, PrincipalID: 11, ActorKind: "HUMAN", Code: "project_create"}
	for _, change := range []func(*AuthorityRequest){
		func(r *AuthorityRequest) { r.TenantID = 0 }, func(r *AuthorityRequest) { r.Code = "" },
		func(r *AuthorityRequest) { r.Code = "unknown_action" }, func(r *AuthorityRequest) { r.Environment = "unknown" },
		func(r *AuthorityRequest) { r.ActorKind = "unknown" }, func(r *AuthorityRequest) { r.PrincipalID = 0 },
		func(r *AuthorityRequest) { r.SystemJob = "job" }, func(r *AuthorityRequest) { r.ActorKind = "SYSTEM" },
		func(r *AuthorityRequest) { r.ActorKind = "SYSTEM"; r.PrincipalID = 0 },
		func(r *AuthorityRequest) { r.ProjectID = 2 }, func(r *AuthorityRequest) { r.Resource.ID = 2 },
		func(r *AuthorityRequest) { r.Resource.Kind = "work_item" }, func(r *AuthorityRequest) { r.Spawn = &SpawnRequestFacts{} },
	} {
		req := base
		change(&req)
		result, err := owner.Check(t.Context(), nil, req)
		require.NoError(t, err)
		require.False(t, result.Allowed)
		require.Equal(t, "INVALID_REQUEST", result.ReasonCode)
	}
	_, err := (*Module)(nil).Check(t.Context(), nil, base)
	require.ErrorIs(t, err, ErrUnavailable)
	req := base
	req.Code = "user_view"
	result, err := owner.Check(t.Context(), nil, req)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, result)
	req = base
	req.Environment = "PRODUCTION"
	result, err = owner.Check(t.Context(), nil, req)
	require.ErrorIs(t, err, ErrUnavailable)
	require.Zero(t, result)
	req = base
	req.ActorKind = "SYSTEM"
	req.PrincipalID = 0
	req.SystemJob = "worker"
	result, err = owner.Check(t.Context(), nil, req)
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, "SYSTEM_NOT_REGISTERED", result.ReasonCode)
}
