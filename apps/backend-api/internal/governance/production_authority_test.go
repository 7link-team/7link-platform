package governance

import (
	"7link.com/platform/internal/governance/model"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestProductionAuthorityRequiresCurrentBoundedHumanTemporaryGrant(t *testing.T) {
	seed, db, rt := seedHarnessRuntime(t)
	require.NoError(t, db.Table(seed.names.Settings.String()).AutoMigrate(&model.Settings{}))
	require.NoError(t, seed.registry.Contribute([]authz.Action{{Resource: "production", Verb: "access", Scope: authz.ActionScopeTenant}, {Resource: "project", Verb: "manage", Scope: authz.ActionScopeTenant}}, nil))
	now := seed.now()
	grant := core.TemporaryGrantFact{RoleID: 21, SourceType: "access_request", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(7 * time.Hour)}
	match := authz.DecisionMatch{PermissionCode: "production_access", RoleID: "21", Effect: "allow", SourceType: "temporary_role", SourceID: "42"}
	allowed, found, human := true, true, true
	owner := &Module{seed: seed, runtime: rt, principals: identity.PrincipalStateStore{Lock: func(context.Context, *gorm.DB, guid.ID) error { return nil }, ActiveHumanByID: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id}, human, nil
	}}}
	owner.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.Equal(t, "production_access", r.Permission)
		return authz.Explanation{Allowed: allowed, Revision: 7, Matches: []authz.DecisionMatch{match}}, nil
	}
	owner.temporaryGrants = func(_ context.Context, tx *gorm.DB, tenant, principal, id guid.ID) (core.TemporaryGrantFact, bool, error) {
		require.NotNil(t, tx)
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(11), principal)
		require.Equal(t, guid.ID(42), id)
		return grant, found, nil
	}
	req := AuthorityRequest{TenantID: 1, PrincipalID: 11, ActorKind: "HUMAN", Code: "production_access"}
	read := func() AuthorityDecision {
		var out AuthorityDecision
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = owner.Check(t.Context(), tx, req); return e }))
		return out
	}
	require.True(t, read().Allowed, "exact eight-hour grant is accepted")
	grant.EndsAt = grant.EndsAt.Add(time.Millisecond)
	require.Equal(t, "PRODUCTION_GRANT_TOO_LONG", read().ReasonCode)
	grant.EndsAt = now.Add(7 * time.Hour)
	match.SourceType = "permanent_role"
	require.Equal(t, "PRODUCTION_GRANT_NOT_TEMPORARY", read().ReasonCode)
	match.SourceType = "temporary_role"
	found = false
	require.False(t, read().Allowed)
	found = true
	for _, ends := range []time.Time{now, now.Add(-time.Hour)} {
		grant.EndsAt = ends
		require.False(t, read().Allowed)
	}
	grant.EndsAt = now.Add(time.Hour)
	grant.StartsAt = now.Add(time.Minute)
	require.False(t, read().Allowed)
	grant.StartsAt = now.Add(-time.Hour)
	allowed = false
	require.Equal(t, "PRODUCTION_ACCESS_REQUIRED", read().ReasonCode)
	allowed = true
	human = false
	require.Equal(t, "AGENT_PROHIBITED", read().ReasonCode)
	human = true
	for _, kind := range []string{"AGENT", "MACHINE"} {
		req.ActorKind = kind
		require.Equal(t, "AGENT_PROHIBITED", read().ReasonCode)
	}
	req.ActorKind = "HUMAN"
	require.True(t, read().Allowed)
	req.Environment = "PRODUCTION"
	req.Code = "project_create"
	owner.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		if r.Permission == "production_access" {
			return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{match}}, nil
		}
		return authz.Explanation{Allowed: false}, nil
	}
	require.False(t, read().Allowed, "production grant cannot replace ordinary action authority")
}

func TestProductionAuthorityFailsClosedOnCurrentSourceFailure(t *testing.T) {
	for _, scenario := range []string{"lock", "human", "explain", "settings", "temporary", "not_transaction", "nil_statement"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, _, _ := delegationHarness(t)
			fault := errors.New("production authority source failed")
			want := fault
			m.temporaryGrants = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (core.TemporaryGrantFact, bool, error) {
				return core.TemporaryGrantFact{}, false, nil
			}
			m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
				return authz.Explanation{Allowed: true, Matches: []authz.DecisionMatch{{Effect: "allow", PermissionCode: "production_access", SourceType: "temporary_role", SourceID: "42"}}}, nil
			}
			switch scenario {
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return fault }
			case "human":
				m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
					return identity.Principal{}, false, fault
				}
			case "explain":
				m.explain = func(context.Context, *gorm.DB, core.AuthorizationExplanationRequest) (authz.Explanation, error) {
					return authz.Explanation{}, fault
				}
			case "settings":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:production", func(tx *gorm.DB) {
					if tx.Statement.Table == m.seed.names.Settings.String() {
						tx.AddError(fault)
					}
				}))
			case "temporary":
				m.temporaryGrants = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (core.TemporaryGrantFact, bool, error) {
					return core.TemporaryGrantFact{}, false, fault
				}
			case "not_transaction", "nil_statement":
				want = ErrInvalidArgument
			}
			req := AuthorityRequest{TenantID: 1, PrincipalID: 2, ActorKind: "HUMAN"}
			var out AuthorityDecision
			var err error
			if scenario == "not_transaction" {
				out, err = m.checkProductionAuthority(ctx, db, req)
			} else if scenario == "nil_statement" {
				out, err = m.checkProductionAuthority(ctx, &gorm.DB{}, req)
			} else {
				err = db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.checkProductionAuthority(ctx, tx, req); return e })
			}
			require.ErrorIs(t, err, want)
			require.False(t, out.Allowed)
		})
	}
	m := &Module{}
	require.ErrorIs(t, m.injectProductionAuthority(module.NewServices()), module.ErrServicesProviding)
	s := module.NewServices()
	require.NoError(t, module.Provide[core.TemporaryGrantReader](s, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (core.TemporaryGrantFact, bool, error) {
		return core.TemporaryGrantFact{}, false, nil
	}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectProductionAuthority(s))
	require.NotNil(t, m.temporaryGrants)
}
