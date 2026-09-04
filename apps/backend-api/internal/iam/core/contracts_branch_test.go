package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type branchIDSource struct {
	id  int64
	err error
}

func (source branchIDSource) Next() (int64, error) { return source.id, source.err }

func TestCorePublicReadValidationContracts(t *testing.T) {
	harness := newCoreServiceHarness(t)
	service := harness.service
	ctx := t.Context()
	checks := []func() error{
		func() error { _, err := service.ListRoles(ctx, 0); return err },
		func() error { _, err := service.GetRole(ctx, 1, 0); return err },
		func() error { return service.DeleteRole(ctx, 1, 0) },
		func() error { _, err := service.ListPermissionGrants(ctx, 1, 0); return err },
		func() error { _, err := service.ListMenus(ctx, 0); return err },
		func() error { _, err := service.GetMenu(ctx, 0, 1); return err },
		func() error { _, err := service.ListTenantMemberRoles(ctx, 1, 0); return err },
		func() error { _, err := service.ListEntities(ctx, 0); return err },
		func() error { _, err := service.GetEntity(ctx, 1, 0); return err },
		func() error { return service.DeleteEntity(ctx, 1, 0) },
		func() error { _, err := service.ListEntityRoleBindings(ctx, 1, 0); return err },
		func() error { _, err := service.GetRoleDataScope(ctx, 1, 0); return err },
		func() error { _, err := service.ListDirectoryBindings(ctx, 1, 0); return err },
		func() error { _, err := service.EffectiveMenus(ctx, 0, 1); return err },
	}
	for _, check := range checks {
		assert.ErrorIs(t, check(), ErrInvalidArgument)
	}
	_, err := service.ExplainResourceAuthorization(ctx, 1, 1, "store", 1, "missing", 2)
	assert.ErrorIs(t, err, ErrPermissionNotFound)
}

func TestCorePureValidationAndDecisionBranches(t *testing.T) {
	assert.Equal(t, []int{0, 0}, func() []int { offset, limit := normalizePage(-1, -1); return []int{offset, limit} }())
	assert.Equal(t, []int{2, maxPageLimit}, func() []int { offset, limit := normalizePage(2, maxPageLimit+1); return []int{offset, limit} }())
	assert.ErrorIs(t, validateEntityRef(0, 1), ErrInvalidArgument)
	assert.ErrorIs(t, validateEntityBinding(1, 1, 1, 0, BindingAllow, time.Time{}), ErrInvalidArgument)
	assert.ErrorIs(t, normalizeEntity(nil), ErrInvalidArgument)
	zero := time.Time{}
	assert.ErrorIs(t, validateRelationshipWindow(Relationship{StartsAt: &zero}, time.Now()), ErrInvalidRelationshipWindow)
	assert.ErrorIs(t, validateRelationshipWindow(Relationship{EndsAt: &zero}, time.Now()), ErrInvalidRelationshipWindow)
	normalizeRelationship(nil)
	assert.True(t, equalOptionalTime(nil, nil))
	now := time.Now()
	assert.False(t, equalOptionalTime(&now, nil))
	assert.True(t, equalOptionalTime(&now, &now))

	allowed, denied, reason := decideEntity("store_view", false, false, nil, false)
	assert.False(t, allowed)
	assert.False(t, denied)
	assert.Equal(t, "no_matching_grant", reason)
	allowed, denied, reason = decideEntity("store_view", false, false, []scopedGrant{{
		PermissionCode: "tenant_administer", Effect: "allow",
	}}, false)
	assert.True(t, allowed)
	assert.False(t, denied)
	assert.Equal(t, "administrator_grant", reason)
	assert.False(t, relationshipAllowed(authz.Action{Resource: "merchant"}, relationshipGrant{ResourceType: "store"}))
	assert.Empty(t, allowedRelationshipsByEntity(authz.Action{}, nil))
	assert.Equal(t, map[guid.ID]bool{7: true}, allowedRelationshipsByEntity(
		authz.Action{Resource: "store", AllowedRelations: []string{"owner"}},
		[]relationshipGrant{
			{ResourceID: 7, Relation: "viewer", ResourceType: "store"},
			{ResourceID: 7, Relation: "owner", ResourceType: "merchant"},
			{ResourceID: 7, Relation: "owner", ResourceType: "store"},
			{EntityID: 8, ResourceID: 7, Relation: "owner", ResourceType: "store"},
		},
	))
	_, err := encodeEntityMetadata(map[string]any{"unsupported": make(chan int)})
	assert.Error(t, err)
	assert.ErrorIs(t, validateAuthorizationRequest(0, "store_view", 1), ErrInvalidArgument)
	assert.ErrorIs(t, validateAuthorizationRequest(1, "store_view", 0), ErrInvalidArgument)
	assert.ErrorIs(t, validateAuthorizationRequest(1, string(make([]byte, 129)), 1), ErrInvalidArgument)
	_, err = requestedPermissions("")
	assert.Error(t, err)
	_, err = matchingTenantGrants([]tenantGrant{{
		RoleID: 1, PermissionCode: "store_view", ConditionJSON: "{broken",
	}}, policyx.TrustedContext{})
	assert.ErrorContains(t, err, "evaluate role permission condition")
	_, err = matchingScopedGrants([]scopedGrant{{
		RoleID: 1, PermissionCode: "store_view", ConditionJSON: "{broken",
	}}, policyx.TrustedContext{})
	assert.ErrorContains(t, err, "evaluate scoped role permission condition")
}

func TestRelationshipTraversalExpressionsMatchDatabaseDialects(t *testing.T) {
	tests := []struct {
		dialect                                        string
		textParameter, entityLiteral, integerParameter string
	}{
		{"sqlite", "CAST(? AS TEXT)", "CAST('entity' AS TEXT)", "CAST(? AS BIGINT)"},
		{"mysql", "CAST(? AS CHAR(16))", "CAST('entity' AS CHAR(16))", "CAST(? AS SIGNED)"},
		{"postgres", `CAST(? AS VARCHAR(16)) COLLATE "C"`, `CAST('entity' AS VARCHAR(16)) COLLATE "C"`, "CAST(? AS BIGINT)"},
	}
	for _, test := range tests {
		t.Run(test.dialect, func(t *testing.T) {
			textParameter, entityLiteral, integerParameter := relationshipTraversalExpressions(test.dialect)
			assert.Equal(t, test.textParameter, textParameter)
			assert.Equal(t, test.entityLiteral, entityLiteral)
			assert.Equal(t, test.integerParameter, integerParameter)
		})
	}
}

func TestRepositoryPinnedTransactionAndIDGeneratorBranches(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	pinned := harness.repository.withDB(harness.db)
	assert.Equal(t, harness.db, pinned.writer())
	reused, err := pinned.pinWriter()
	require.NoError(t, err)
	assert.Same(t, pinned, reused)
	called := false
	require.NoError(t, pinned.runInTx(t.Context(), func(repo *Repository) error {
		called = true
		assert.Same(t, pinned, repo)
		return nil
	}))
	assert.True(t, called)

	want := errors.New("id source failed")
	failing := NewRepository(harness.runtime, branchIDSource{err: want})
	_, err = failing.CreateRole(t.Context(), 1, "role", "")
	assert.ErrorIs(t, err, want)
	zero := NewRepository(harness.runtime, branchIDSource{})
	_, err = zero.CreateRole(t.Context(), 1, "role", "")
	assert.ErrorContains(t, err, "must be positive")
	assert.Panics(t, func() { NewRepository(harness.runtime, nil) })
}

func TestRoleGrantStoreAndCoordinatorRejectInvalidCalls(t *testing.T) {
	harness := newRepositoryHarness(t, "sl_")
	guard := NewAdministratorGuard(harness.runtime)
	store := NewRoleGrantStore(harness.repository, guard)
	ctx := t.Context()
	_, err := store.RemovePermanent(ctx, nil, 1, 2, 3, time.Now())
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = store.RemoveGroupMembership(ctx, nil, 1, 2, 3)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.repository.removeDirectoryMembershipOn("bad")(ctx, harness.db, 1, 2, 3)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = store.RemoveEntityRoleBinding(ctx, nil, 1, 2, 3, 4)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	assert.ErrorIs(t, harness.repository.grantTemporaryRoleOn(
		ctx, nil, 1, 2, 3, 4, 5, time.Now(), time.Now().Add(time.Hour),
	), ErrUnavailable)
	_, err = harness.repository.revokeTemporaryRoleOn(ctx, nil, 1, 2)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Panics(t, func() { NewRoleGrantStore(harness.repository, nil) })
	assert.Panics(t, func() { NewAdministratorGuard(nil) })

	var coordinator *transactionCoordinator
	assert.Error(t, coordinator.Run(ctx, nil, nil))
	assert.Error(t, newTransactionCoordinator(harness.repository, nil).Run(
		ctx, newAuditRecord(ctx, 1, "event", "target", 2), func(*Repository) error { return nil },
	))
	var repo *Repository
	assert.ErrorIs(t, repo.bumpPolicyRevision(ctx, 1), ErrUnavailable)
}

func TestEscalationAndDynamicGroupBoundaryBranches(t *testing.T) {
	harness := newCoreServiceHarness(t)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: 11})
	require.NoError(t, harness.service.requireGrantablePermissions(ctx, 1, "", ""))
	_, err := matchingDynamicGroupIDs(t.Context(), nil, "sl_", 1, 11)
	assert.ErrorIs(t, err, ErrUnavailable)

	seedTenantAndPrincipal(t, harness.db, "sl_", 1, 11)
	_, err = harness.service.PutTenantMember(t.Context(), 1, 11, "Member", "member@example.test", 0, MemberActive)
	require.NoError(t, err)
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_groups
		(tenant_id,id,name,name_key,group_type,rule_json,description,status,sort_order,version,created_at,updated_at)
		VALUES (1,201,'Broken','broken','dynamic','{broken','','active',0,1,?,?)`, now, now).Error)
	_, err = matchingDynamicGroupIDs(t.Context(), harness.db, "sl_", 1, 11)
	assert.ErrorContains(t, err, "evaluate dynamic group")
}

func TestRepositoryPaginationBranches(t *testing.T) {
	harness := newCoreServiceHarness(t)
	for index := 0; index < 3; index++ {
		_, err := harness.repository.CreateRole(t.Context(), 1, "Role "+guid.ID(index+1).String(), "")
		require.NoError(t, err)
		_, err = harness.repository.CreateEntity(t.Context(), Entity{
			TenantID: 1, Type: "unit", Name: "Unit " + guid.ID(index+1).String(), Status: EntityActive,
		})
		require.NoError(t, err)
	}
	roles, total, err := harness.repository.ListRolesPage(t.Context(), 1, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, roles, 1)
	entities, total, err := harness.repository.ListEntitiesPage(t.Context(), 1, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, entities, 1)

	_, _, err = harness.service.ListRolesPage(t.Context(), 0, 0, 1)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = harness.service.ListEntitiesPage(t.Context(), 0, 0, 1)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	roles, total, err = harness.service.ListRolesPage(t.Context(), 1, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, roles, 1)
	entities, total, err = harness.service.ListEntitiesPage(t.Context(), 1, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, entities, 1)
}

func TestAuthorizerPinnedAndEmptyRequestBranches(t *testing.T) {
	harness := newCoreServiceHarness(t)
	pinned, err := harness.authorizer.pinWriter()
	require.NoError(t, err)
	reused, err := pinned.pinWriter()
	require.NoError(t, err)
	assert.Same(t, pinned, reused)
	result, err := pinned.CheckBulk(context.Background(), 1, nil, 2)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result)
	_, err = pinned.CheckPlatform(context.Background(), "", 2)
	assert.Error(t, err)
	_, err = pinned.Constraint(context.Background(), 0, "store_view", 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = pinned.ExplainEntity(context.Background(), 1, 0, "store_view", 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = pinned.ExplainResource(context.Background(), 1, 1, "", 2, "store_view", 2)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = pinned.loadTenantGrants(context.Background(), 0, 2, []string{"store_view"}, policyx.TrustedContext{})
	assert.Error(t, err)
	grants, err := pinned.loadTenantGrants(context.Background(), 1, 2, nil, policyx.TrustedContext{})
	require.NoError(t, err)
	assert.Empty(t, grants)
}
