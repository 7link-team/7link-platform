package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/policyx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthorizerConcurrentTenantDecisionsRemainIsolated(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantA, tenantB := guid.ID(1), guid.ID(2)
	principalA, principalB := guid.ID(11), guid.ID(22)
	harness.seedMember(t, tenantA, principalA, MemberActive)
	harness.seedMember(t, tenantB, principalB, MemberActive)
	_, err := harness.service.PutTenantMember(
		t.Context(), tenantA, principalB, "Principal B", "principal-b@example.test", 0, MemberActive,
	)
	require.NoError(t, err)
	_, err = harness.service.PutTenantMember(
		t.Context(), tenantB, principalA, "Principal A", "principal-a@example.test", 0, MemberActive,
	)
	require.NoError(t, err)

	roleA, err := harness.service.CreateRole(t.Context(), tenantA, "Tenant A reader", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantA, roleA.ID, "role_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantA, roleA.ID, principalA)
	require.NoError(t, err)

	roleB, err := harness.service.CreateRole(t.Context(), tenantB, "Tenant B reader", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantB, roleB.ID, "menu_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantB, roleB.ID, principalB)
	require.NoError(t, err)

	cases := []struct {
		tenant     guid.ID
		principal  guid.ID
		permission string
		allowed    bool
	}{
		{tenant: tenantA, principal: principalA, permission: "role_view", allowed: true},
		{tenant: tenantA, principal: principalB, permission: "role_view", allowed: false},
		{tenant: tenantB, principal: principalB, permission: "menu_view", allowed: true},
		{tenant: tenantB, principal: principalA, permission: "menu_view", allowed: false},
	}
	type result struct {
		caseIndex int
		allowed   bool
		err       error
	}
	const workers = 32
	start := make(chan struct{})
	results := make(chan result, workers)
	var wait sync.WaitGroup
	for worker := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			caseIndex := worker % len(cases)
			test := cases[caseIndex]
			allowed, checkErr := harness.authorizer.Check(
				context.Background(), test.tenant, test.permission, test.principal,
			)
			results <- result{caseIndex: caseIndex, allowed: allowed, err: checkErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for result := range results {
		test := cases[result.caseIndex]
		name := fmt.Sprintf("tenant=%s principal=%s permission=%s", test.tenant, test.principal, test.permission)
		require.NoError(t, result.err, name)
		assert.Equal(t, test.allowed, result.allowed, name)
	}
}

func TestAuthorizerEntityInheritanceExplicitDenyAndInspection(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	company, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "company", Name: "Company",
	})
	require.NoError(t, err)
	store, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, ParentID: company.ID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)
	allowRole, err := harness.service.CreateRole(t.Context(), tenantID, "Company viewer", "")
	require.NoError(t, err)
	denyRole, err := harness.service.CreateRole(t.Context(), tenantID, "Store deny", "")
	require.NoError(t, err)
	for _, role := range []Role{allowRole, denyRole} {
		_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "store_view")
		require.NoError(t, err)
	}
	_, _, err = harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, company.ID, allowRole.ID, principalID, BindingAllow, time.Time{},
	)
	require.NoError(t, err)
	_, _, err = harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, store.ID, denyRole.ID, principalID, BindingDeny, time.Time{},
	)
	require.NoError(t, err)

	explanation, err := harness.authorizer.ExplainEntity(
		t.Context(), tenantID, store.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.False(t, explanation.Allowed)
	assert.Equal(t, "explicit_deny", explanation.Reason)
	assert.NotEmpty(t, explanation.Matches)
	constraint, err := harness.service.AuthorizationConstraint(
		t.Context(), tenantID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.Contains(t, constraint.DeniedIDs, store.ID.String())
	require.Len(t, constraint.Ancestors, 1)
	assert.Equal(t, company.ID.String(), constraint.Ancestors[0].ID)

	changed, err := harness.service.DeleteEntityRoleBinding(
		t.Context(), tenantID, store.ID, denyRole.ID, principalID,
	)
	require.NoError(t, err)
	assert.True(t, changed)
	explanation, err = harness.service.ExplainEntityAuthorization(
		t.Context(), tenantID, store.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.True(t, explanation.Allowed)
	assert.Equal(t, "permission_grant", explanation.Reason)

	_, err = harness.service.AuthorizationConstraint(t.Context(), tenantID, "role_view", principalID)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	_, err = harness.service.ExplainEntityAuthorization(t.Context(), tenantID, store.ID, "missing", principalID)
	assert.ErrorIs(t, err, ErrPermissionNotFound)
}

func TestAuthorizerTemporaryAndConditionalGrants(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Conditional", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	condition := json.RawMessage(`{"version":1,"gte":[{"context":"auth.acr"},{"value":2}]}`)
	grant, changed, err := harness.service.SetPermissionCondition(
		t.Context(), tenantID, role.ID, "role_view", condition,
	)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.JSONEq(t, string(condition), string(grant.Condition))

	startsAt := repositoryTestTime.Add(-time.Minute)
	endsAt := repositoryTestTime.Add(time.Hour)
	require.NoError(t, harness.repository.GrantTemporaryRole(
		t.Context(), tenantID, guid.ID(301), role.ID, principalID, principalID, startsAt, endsAt,
	))
	low := policyx.WithTrustedContext(t.Context(), policyx.TrustedContext{
		Time: repositoryTestTime, ACR: 1,
	})
	allowed, err := harness.authorizer.Check(low, tenantID, "role_view", principalID)
	require.NoError(t, err)
	assert.False(t, allowed)
	high := policyx.WithTrustedContext(t.Context(), policyx.TrustedContext{
		Time: repositoryTestTime, ACR: 2,
	})
	allowed, err = harness.authorizer.Check(high, tenantID, "role_view", principalID)
	require.NoError(t, err)
	assert.True(t, allowed)

	*harness.now = endsAt
	allowed, err = harness.authorizer.Check(high, tenantID, "role_view", principalID)
	require.NoError(t, err)
	assert.False(t, allowed, "temporary role must expire at its end instant")
	changed, err = harness.repository.RevokeTemporaryRole(t.Context(), tenantID, guid.ID(301))
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestAuthorizerRelationshipAndBusinessResourceLifecycle(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	store, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)

	entityGrant := Relationship{
		TenantID: tenantID, SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "store", ResourceID: store.ID,
	}
	stored, changed, err := harness.service.PutRelationship(t.Context(), entityGrant)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "viewer", stored.Relation)
	allowed, err := harness.authorizer.CheckEntity(
		t.Context(), tenantID, store.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.True(t, allowed)

	resourceID := guid.ID(501)
	resourceGrant := Relationship{
		TenantID: tenantID, EntityID: store.ID,
		SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "store", ResourceID: resourceID,
	}
	_, changed, err = harness.service.PutRelationship(t.Context(), resourceGrant)
	require.NoError(t, err)
	assert.True(t, changed)
	explanation, err := harness.service.ExplainResourceAuthorization(
		t.Context(), tenantID, store.ID, "store", resourceID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.True(t, explanation.Allowed)
	assert.Equal(t, "relationship_grant", explanation.Reason)
	_, err = harness.service.ExplainResourceAuthorization(
		t.Context(), tenantID, store.ID, "merchant", resourceID, "store_view", principalID,
	)
	assert.ErrorIs(t, err, ErrInvalidResourceAuthorization)

	items, err := harness.service.ListRelationships(t.Context(), tenantID, RelationshipFilter{})
	require.NoError(t, err)
	assert.Len(t, items, 1)
	resources, err := harness.service.ListRelationships(
		t.Context(), tenantID, RelationshipFilter{EntityID: store.ID},
	)
	require.NoError(t, err)
	assert.Len(t, resources, 1)
	changed, err = harness.service.DeleteRelationship(t.Context(), entityGrant)
	require.NoError(t, err)
	assert.True(t, changed)
	allowed, err = harness.authorizer.CheckEntity(t.Context(), tenantID, store.ID, "store_view", principalID)
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestAuthorizerCompilesSelectedDepartmentAndOwnerConstraints(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (?, ?, NULL, 'Engineering', 'engineering', 'active', 0, 1, ?, ?)`,
		tenantID, guid.ID(101), now, now).Error)

	selected, err := harness.service.CreateRole(t.Context(), tenantID, "Selected", "")
	require.NoError(t, err)
	self, err := harness.service.CreateRole(t.Context(), tenantID, "Self", "")
	require.NoError(t, err)
	for _, role := range []Role{selected, self} {
		_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "user_view")
		require.NoError(t, err)
		_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID)
		require.NoError(t, err)
	}
	_, _, err = harness.service.SetRoleDataScope(
		t.Context(), tenantID, selected.ID, DataScopeSelectedDepartments, []guid.ID{101},
	)
	require.NoError(t, err)
	_, _, err = harness.service.SetRoleDataScope(t.Context(), tenantID, self.ID, DataScopeSelf, nil)
	require.NoError(t, err)

	constraint, err := harness.authorizer.Constraint(t.Context(), tenantID, "user_view", principalID)
	require.NoError(t, err)
	assert.False(t, constraint.AllowAll)
	assert.Equal(t, []string{principalID.String()}, constraint.OwnerIDs)
	assert.Equal(t, []string{guid.ID(101).String()}, constraint.DepartmentIDs)
}

func TestAuthorizerEntityConstraintKeepsRowScopesBelowEntityBoundary(t *testing.T) {
	for _, scope := range []DataScope{DataScopeSelf, DataScopeSelectedDepartments, DataScopeAll} {
		t.Run(string(scope), func(t *testing.T) {
			harness := newCoreServiceHarness(t)
			tenantID, subject := guid.ID(1), guid.ID(11)
			harness.seedMember(t, tenantID, subject, MemberActive)
			harness.seedMember(t, 2, 22, MemberActive)
			foreign, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: 2, Type: "store", Name: "Foreign"})
			require.NoError(t, err)
			entity, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "Local"})
			require.NoError(t, err)
			require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
				(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
				VALUES (?,101,NULL,'Engineering','engineering','active',0,1,?,?)`, tenantID, repositoryTestTime, repositoryTestTime).Error)
			role, err := harness.service.CreateRole(t.Context(), tenantID, "Data access", "")
			require.NoError(t, err)
			_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "store_view")
			require.NoError(t, err)
			_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, subject)
			require.NoError(t, err)
			var departments []guid.ID
			if scope == DataScopeSelectedDepartments {
				departments = []guid.ID{101}
			}
			_, _, err = harness.service.SetRoleDataScope(t.Context(), tenantID, role.ID, scope, departments)
			require.NoError(t, err)

			constraint, err := harness.authorizer.EntityConstraint(t.Context(), tenantID, entity.ID, "store_view", subject)
			require.NoError(t, err)
			assert.True(t, constraint.AllowsEntityRows(entity.ID))
			assert.Equal(t, scope == DataScopeAll, constraint.AllowAll)
			if scope == DataScopeSelf {
				assert.Equal(t, []string{subject.String()}, constraint.OwnerIDs)
			}
			if scope == DataScopeSelectedDepartments {
				assert.Equal(t, []string{"101"}, constraint.DepartmentIDs)
			}
			whole, err := harness.authorizer.CheckEntity(t.Context(), tenantID, entity.ID, "store_view", subject)
			require.NoError(t, err)
			assert.Equal(t, scope == DataScopeAll, whole, "row scope must not expand whole-entity permission")
			for _, id := range []guid.ID{foreign.ID, 999} {
				denied, checkErr := harness.authorizer.EntityConstraint(t.Context(), tenantID, id, "store_view", subject)
				require.NoError(t, checkErr)
				assert.False(t, denied.AllowsEntityRows(id))
				assert.Empty(t, denied.OwnerIDs)
				assert.Empty(t, denied.DepartmentIDs)
			}
			for _, permission := range []string{"unknown_view", "role_view", "tenant_view"} {
				denied, checkErr := harness.authorizer.EntityConstraint(t.Context(), tenantID, entity.ID, permission, subject)
				require.NoError(t, checkErr)
				assert.False(t, denied.AllowsEntityRows(entity.ID), permission)
			}

			denyRole, err := harness.service.CreateRole(t.Context(), tenantID, "Entity deny", "")
			require.NoError(t, err)
			_, err = harness.service.GrantPermission(t.Context(), tenantID, denyRole.ID, "store_view")
			require.NoError(t, err)
			_, _, err = harness.service.PutEntityRoleBinding(t.Context(), tenantID, entity.ID, denyRole.ID, subject, BindingDeny, time.Time{})
			require.NoError(t, err)
			denied, err := harness.authorizer.EntityConstraint(t.Context(), tenantID, entity.ID, "store_view", subject)
			require.NoError(t, err)
			assert.Greater(t, denied.Revision, constraint.Revision)
			assert.Equal(t, []string{entity.ID.String()}, denied.DeniedIDs)
			assert.False(t, denied.AllowAll)
			assert.Empty(t, denied.OwnerIDs)
			assert.Empty(t, denied.DepartmentIDs)
			assert.False(t, denied.AllowsEntityRows(entity.ID))
			_, err = harness.service.DeleteEntityRoleBinding(t.Context(), tenantID, entity.ID, denyRole.ID, subject)
			require.NoError(t, err)

			disabled := EntityDisabled
			_, err = harness.service.UpdateEntity(t.Context(), tenantID, entity.ID, EntityPatch{Status: &disabled})
			require.NoError(t, err)
			denied, err = harness.authorizer.EntityConstraint(t.Context(), tenantID, entity.ID, "store_view", subject)
			require.NoError(t, err)
			assert.False(t, denied.AllowsEntityRows(entity.ID))
			active := EntityActive
			_, err = harness.service.UpdateEntity(t.Context(), tenantID, entity.ID, EntityPatch{Status: &active})
			require.NoError(t, err)
			_, err = harness.service.RevokePermission(t.Context(), tenantID, role.ID, "store_view")
			require.NoError(t, err)
			denied, err = harness.authorizer.EntityConstraint(t.Context(), tenantID, entity.ID, "store_view", subject)
			require.NoError(t, err)
			assert.False(t, denied.AllowsEntityRows(entity.ID), "revocation must take effect without rewiring")
		})
	}
}

func TestAuthorizerEntityConstraintInheritedGrantDenyAndMembership(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, subject := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, subject, MemberActive)
	root, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "company", Name: "Root"})
	require.NoError(t, err)
	child, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, ParentID: root.ID, Type: "store", Name: "Child"})
	require.NoError(t, err)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Inherited", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "store_view")
	require.NoError(t, err)
	_, _, err = harness.service.PutEntityRoleBinding(t.Context(), tenantID, root.ID, role.ID, subject, BindingAllow, time.Time{})
	require.NoError(t, err)
	constraint, err := harness.authorizer.EntityConstraint(t.Context(), tenantID, child.ID, "store_view", subject)
	require.NoError(t, err)
	assert.Equal(t, []string{child.ID.String()}, constraint.ResourceIDs)
	require.Len(t, constraint.Ancestors, 1)
	assert.Equal(t, root.ID.String(), constraint.Ancestors[0].ID)
	assert.True(t, constraint.AllowsEntityRows(child.ID))
	_, _, err = harness.service.PutEntityRoleBinding(t.Context(), tenantID, child.ID, role.ID, subject, BindingDeny, time.Time{})
	require.NoError(t, err)
	constraint, err = harness.authorizer.EntityConstraint(t.Context(), tenantID, child.ID, "store_view", subject)
	require.NoError(t, err)
	assert.Empty(t, constraint.ResourceIDs)
	assert.False(t, constraint.AllowsEntityRows(child.ID), "ancestor provenance cannot override a child deny")
	_, err = harness.service.PutTenantMember(t.Context(), tenantID, subject, "Member", "member@example.test", 0, MemberDisabled)
	require.NoError(t, err)
	constraint, err = harness.authorizer.EntityConstraint(t.Context(), tenantID, root.ID, "store_view", subject)
	require.NoError(t, err)
	assert.False(t, constraint.AllowsEntityRows(root.ID))
	assert.Empty(t, constraint.ResourceIDs)
}

func TestAuthorizerEntityConstraintFailsClosedOnInvalidInputAndUnavailableStore(t *testing.T) {
	var absent *Authorizer
	_, err := absent.EntityConstraint(t.Context(), 1, 2, "store_view", 11)
	assert.ErrorIs(t, err, ErrUnavailable)
	harness := newCoreServiceHarness(t)
	_, err = harness.authorizer.EntityConstraint(t.Context(), 1, 0, "store_view", 11)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	harness.seedMember(t, 1, 11, MemberActive)
	require.NoError(t, harness.db.Exec("DROP TABLE sl_iam_policy_revisions").Error)
	constraint, err := harness.authorizer.EntityConstraint(t.Context(), 1, 2, "store_view", 11)
	require.Error(t, err)
	assert.False(t, constraint.AllowsEntityRows(2))
}

func TestAuthorizerInactiveMembershipEntityStateAndAliases(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberDisabled)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)

	explanation, err := harness.service.CheckEntityAuthorization(
		t.Context(), tenantID, entity.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.False(t, explanation.Allowed)
	assert.Equal(t, "inactive_membership", explanation.Reason)
	constraint, err := harness.authorizer.Constraint(t.Context(), tenantID, "store_view", principalID)
	require.NoError(t, err)
	assert.False(t, constraint.AllowAll)
	assert.NotNil(t, constraint.ResourceIDs)

	status := EntityDisabled
	_, err = harness.service.UpdateEntity(t.Context(), tenantID, entity.ID, EntityPatch{Status: &status})
	require.NoError(t, err)
	explanation, err = harness.authorizer.ExplainEntity(
		t.Context(), tenantID, entity.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.Equal(t, "inactive_resource", explanation.Reason)
	_, err = harness.authorizer.ExplainEntity(t.Context(), tenantID, 999, "store_view", principalID)
	assert.ErrorIs(t, err, ErrEntityNotFound)

	_, err = harness.authorizer.CheckResource(
		t.Context(), tenantID, entity.ID, "store", 501, "store_view", principalID,
	)
	require.NoError(t, err)
	_, err = harness.service.CheckResourceAuthorization(
		t.Context(), tenantID, entity.ID, "store", 501, "store_view", principalID,
	)
	require.NoError(t, err)
	allowed, err := harness.authorizer.Check(t.Context(), 0, "store_view", principalID)
	require.NoError(t, err)
	assert.False(t, allowed)
	_, err = harness.authorizer.Check(t.Context(), tenantID, "", principalID)
	assert.ErrorContains(t, err, "permission is empty")
	for _, call := range []func() error{
		func() error {
			_, err := harness.authorizer.CheckEntity(t.Context(), tenantID, 0, "store_view", principalID)
			return err
		},
		func() error {
			_, err := harness.authorizer.CheckResource(t.Context(), tenantID, entity.ID, "", 501, "store_view", principalID)
			return err
		},
	} {
		assert.ErrorIs(t, call(), ErrInvalidArgument)
	}
}

func TestAuthorizerDirectoryAssignmentSourcesAndDataScopeDescendants(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_departments
		(tenant_id,id,parent_id,name,name_key,status,sort_order,version,created_at,updated_at)
		VALUES (?, 101, NULL, 'Root', 'root', 'active', 0, 1, ?, ?),
		       (?, 102, 101, 'Child', 'child', 'active', 0, 1, ?, ?)`,
		tenantID, now, now, tenantID, now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_department_closure
		(tenant_id,ancestor_id,descendant_id,depth)
		VALUES (?,101,101,0),(?,101,102,1),(?,102,102,0)`, tenantID, tenantID, tenantID).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_member_departments
		(tenant_id,principal_id,department_id,updated_at) VALUES (?, ?, 101, ?)`,
		tenantID, principalID, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_groups
		(tenant_id,id,name,name_key,group_type,rule_json,description,status,sort_order,version,created_at,updated_at)
		VALUES (?,201,'Static','static','static','','','active',0,1,?,?),
		       (?,202,'Dynamic','dynamic','dynamic',?,'','active',0,1,?,?)`,
		tenantID, now, now, tenantID,
		`{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`,
		now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_group_members
		(tenant_id,group_id,principal_id,starts_at,ends_at,created_at,updated_at)
		VALUES (?,201,?,NULL,NULL,?,?)`, tenantID, principalID, now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_positions
		(tenant_id,id,code,name,status,sort_order,version,created_at,updated_at)
		VALUES (?,301,'operator','Operator','active',0,1,?,?)`, tenantID, now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_position_members
		(tenant_id,position_id,principal_id,starts_at,ends_at,created_at,updated_at)
		VALUES (?,301,?,NULL,NULL,?,?)`, tenantID, principalID, now, now).Error)

	type source struct {
		name         string
		permission   string
		assigneeType DirectoryAssigneeType
		assigneeID   guid.ID
	}
	sources := []source{
		{"Static", "role_view", DirectoryAssigneeGroup, 201},
		{"Dynamic", "menu_view", DirectoryAssigneeGroup, 202},
		{"Position", "entity_view", DirectoryAssigneePosition, 301},
	}
	for _, item := range sources {
		role, createErr := harness.service.CreateRole(t.Context(), tenantID, item.name, "")
		require.NoError(t, createErr)
		_, createErr = harness.service.GrantPermission(t.Context(), tenantID, role.ID, item.permission)
		require.NoError(t, createErr)
		_, createErr = harness.service.AddDirectoryBinding(
			t.Context(), tenantID, role.ID, item.assigneeType, item.assigneeID,
		)
		require.NoError(t, createErr)
		allowed, checkErr := harness.authorizer.Check(t.Context(), tenantID, item.permission, principalID)
		require.NoError(t, checkErr)
		assert.True(t, allowed, item.name)
		pinned, pinErr := harness.authorizer.pinWriter()
		require.NoError(t, pinErr)
		fieldRoles, fieldErr := pinned.fieldPolicyRoles(t.Context(), tenantID, 0, principalID)
		require.NoError(t, fieldErr)
		assert.Contains(t, fieldRoles, role.ID, item.name+" also participates in field policies")
	}

	role, err := harness.service.CreateRole(t.Context(), tenantID, "Department descendants", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "user_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID)
	require.NoError(t, err)
	_, _, err = harness.service.SetRoleDataScope(
		t.Context(), tenantID, role.ID, DataScopeDepartmentAndDescendants, nil,
	)
	require.NoError(t, err)
	constraint, err := harness.authorizer.Constraint(t.Context(), tenantID, "user_view", principalID)
	require.NoError(t, err)
	assert.Equal(t, []string{"101", "102"}, constraint.DepartmentIDs)
}

func TestRelationshipValidationFactsWindowsConditionsAndGraph(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	harness.seedAdditionalMember(t, tenantID, 12, MemberDisabled)
	a, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "A"})
	require.NoError(t, err)
	b, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "B"})
	require.NoError(t, err)

	invalidShapes := []Relationship{
		{},
		{TenantID: tenantID, SubjectType: "principal", SubjectID: principalID, SubjectRelation: "member", Relation: "viewer", ResourceType: "store", ResourceID: a.ID},
		{TenantID: tenantID, SubjectType: "group", SubjectID: 1, Relation: "viewer", ResourceType: "store", ResourceID: a.ID},
		{TenantID: tenantID, SubjectType: "position", SubjectID: 1, SubjectRelation: "bad", Relation: "viewer", ResourceType: "store", ResourceID: a.ID},
		{TenantID: tenantID, SubjectType: "entity", SubjectID: a.ID, SubjectRelation: "bad", Relation: "viewer", ResourceType: "store", ResourceID: b.ID},
		{TenantID: tenantID, SubjectType: "unknown", SubjectID: 1, Relation: "viewer", ResourceType: "store", ResourceID: a.ID},
	}
	for _, relationship := range invalidShapes {
		_, _, err = harness.service.PutRelationship(t.Context(), relationship)
		assert.ErrorIs(t, err, ErrInvalidRelationship)
	}

	missing := Relationship{TenantID: tenantID, SubjectType: "principal", SubjectID: 999, Relation: "viewer", ResourceType: "store", ResourceID: a.ID}
	_, _, err = harness.service.PutRelationship(t.Context(), missing)
	assert.ErrorIs(t, err, ErrRelationshipSubjectMissing)
	inactive := missing
	inactive.SubjectID = 12
	_, _, err = harness.service.PutRelationship(t.Context(), inactive)
	assert.ErrorIs(t, err, ErrRelationshipSubjectInactive)
	wrongType := Relationship{TenantID: tenantID, SubjectType: "principal", SubjectID: principalID, Relation: "viewer", ResourceType: "company", ResourceID: a.ID}
	_, _, err = harness.service.PutRelationship(t.Context(), wrongType)
	assert.ErrorIs(t, err, ErrInvalidRelationship)

	status := EntityDisabled
	_, err = harness.service.UpdateEntity(t.Context(), tenantID, b.ID, EntityPatch{Status: &status})
	require.NoError(t, err)
	disabledResource := Relationship{TenantID: tenantID, SubjectType: "principal", SubjectID: principalID, Relation: "viewer", ResourceType: "store", ResourceID: b.ID}
	_, _, err = harness.service.PutRelationship(t.Context(), disabledResource)
	assert.ErrorIs(t, err, ErrRelationshipResourceInactive)
	_, _, err = harness.service.PutRelationship(t.Context(), Relationship{
		TenantID: tenantID, SubjectType: "entity", SubjectID: 999, SubjectRelation: "owner",
		Relation: "viewer", ResourceType: "store", ResourceID: a.ID,
	})
	assert.ErrorIs(t, err, ErrRelationshipSubjectMissing)
	_, _, err = harness.service.PutRelationship(t.Context(), Relationship{
		TenantID: tenantID, SubjectType: "entity", SubjectID: b.ID, SubjectRelation: "owner",
		Relation: "viewer", ResourceType: "store", ResourceID: a.ID,
	})
	assert.ErrorIs(t, err, ErrRelationshipSubjectInactive)

	past := repositoryTestTime.Add(-time.Millisecond)
	future := repositoryTestTime.Add(time.Hour)
	for _, window := range []Relationship{
		{TenantID: tenantID, SubjectType: "principal", SubjectID: principalID, Relation: "viewer", ResourceType: "store", ResourceID: a.ID, EndsAt: &past},
		{TenantID: tenantID, SubjectType: "principal", SubjectID: principalID, Relation: "viewer", ResourceType: "store", ResourceID: a.ID, StartsAt: &future, EndsAt: &future},
	} {
		_, _, err = harness.service.PutRelationship(t.Context(), window)
		assert.ErrorIs(t, err, ErrInvalidRelationshipWindow)
	}
	conditioned := Relationship{
		TenantID: tenantID, SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "store", ResourceID: a.ID,
		Condition: json.RawMessage(`{"version":1,"gte":[{"context":"auth.acr"},{"value":2}]}`),
	}
	_, changed, err := harness.service.PutRelationship(t.Context(), conditioned)
	require.NoError(t, err)
	assert.True(t, changed)
	allowed, err := harness.authorizer.CheckEntity(t.Context(), tenantID, a.ID, "store_view", principalID)
	require.NoError(t, err)
	assert.False(t, allowed)
	trusted := policyx.WithTrustedContext(t.Context(), policyx.TrustedContext{Time: repositoryTestTime, ACR: 2})
	allowed, err = harness.authorizer.CheckEntity(trusted, tenantID, a.ID, "store_view", principalID)
	require.NoError(t, err)
	assert.True(t, allowed)
	conditioned.Condition = json.RawMessage(`{"version":2,"eq":[{"context":"auth.acr"},{"value":2}]}`)
	_, _, err = harness.service.PutRelationship(t.Context(), conditioned)
	assert.ErrorIs(t, err, ErrInvalidRelationshipCondition)

	status = EntityActive
	_, err = harness.service.UpdateEntity(t.Context(), tenantID, b.ID, EntityPatch{Status: &status})
	require.NoError(t, err)
	first := Relationship{TenantID: tenantID, SubjectType: "entity", SubjectID: a.ID, SubjectRelation: "owner", Relation: "editor", ResourceType: "store", ResourceID: b.ID}
	second := Relationship{TenantID: tenantID, SubjectType: "entity", SubjectID: b.ID, SubjectRelation: "editor", Relation: "owner", ResourceType: "store", ResourceID: a.ID}
	_, _, err = harness.service.PutRelationship(t.Context(), first)
	require.NoError(t, err)
	_, _, err = harness.service.PutRelationship(t.Context(), second)
	assert.ErrorIs(t, err, ErrRelationshipHierarchy)
	items, err := harness.service.ListRelationships(t.Context(), tenantID, RelationshipFilter{})
	require.NoError(t, err)
	assert.Len(t, items, 2, "the rejected cycle must roll back")
	_, err = harness.service.ListRelationships(t.Context(), tenantID, RelationshipFilter{ResourceType: "bad type"})
	assert.ErrorIs(t, err, ErrInvalidArgument)
}

func TestRelationshipGroupAndPositionSubjectValidation(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{TenantID: tenantID, Type: "store", Name: "Store"})
	require.NoError(t, err)
	now := repositoryTestTime
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_groups
		(tenant_id,id,name,name_key,group_type,rule_json,description,status,sort_order,version,created_at,updated_at)
		VALUES (?,201,'Active','active','static','','','active',0,1,?,?),
		       (?,202,'Disabled','disabled','static','','','disabled',0,1,?,?)`,
		tenantID, now, now, tenantID, now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_positions
		(tenant_id,id,code,name,status,sort_order,version,created_at,updated_at)
		VALUES (?,301,'active','Active','active',0,1,?,?),
		       (?,302,'disabled','Disabled','disabled',0,1,?,?)`,
		tenantID, now, now, tenantID, now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_group_members
		(tenant_id,group_id,principal_id,starts_at,ends_at,created_at,updated_at)
		VALUES (?,201,?,NULL,NULL,?,?)`, tenantID, principalID, now, now).Error)
	require.NoError(t, harness.db.Exec(`INSERT INTO sl_iam_position_members
		(tenant_id,position_id,principal_id,starts_at,ends_at,created_at,updated_at)
		VALUES (?,301,?,NULL,NULL,?,?)`, tenantID, principalID, now, now).Error)

	for _, item := range []struct {
		typeName string
		id       guid.ID
		want     error
	}{
		{"group", 999, ErrRelationshipSubjectMissing},
		{"position", 999, ErrRelationshipSubjectMissing},
		{"group", 202, ErrRelationshipSubjectInactive},
		{"position", 302, ErrRelationshipSubjectInactive},
		{"group", 201, nil},
		{"position", 301, nil},
	} {
		relationship := Relationship{
			TenantID: tenantID, SubjectType: item.typeName, SubjectID: item.id, SubjectRelation: "member",
			Relation: "viewer", ResourceType: "store", ResourceID: entity.ID,
		}
		_, _, putErr := harness.service.PutRelationship(t.Context(), relationship)
		if item.want == nil {
			require.NoError(t, putErr)
		} else {
			assert.ErrorIs(t, putErr, item.want)
		}
	}
	items, err := harness.service.ListRelationships(t.Context(), tenantID, RelationshipFilter{})
	require.NoError(t, err)
	assert.Len(t, items, 2)
	allowed, err := harness.authorizer.CheckEntity(
		t.Context(), tenantID, entity.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.True(t, allowed, "group and position relationship subjects expand from active membership")
}

func TestRelationshipDepthLimit(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	nodes := make([]Entity, maxRelationshipDepth+1)
	for index := range nodes {
		var err error
		nodes[index], err = harness.service.CreateEntity(t.Context(), Entity{
			TenantID: tenantID, Type: "unit", Name: fmt.Sprintf("Unit %02d", index),
		})
		require.NoError(t, err)
	}
	_, _, err := harness.service.PutRelationship(t.Context(), Relationship{
		TenantID: tenantID, SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "unit", ResourceID: nodes[0].ID,
	})
	require.NoError(t, err)
	for index := 0; index < len(nodes)-1; index++ {
		_, _, err = harness.service.PutRelationship(t.Context(), Relationship{
			TenantID: tenantID, SubjectType: "entity", SubjectID: nodes[index].ID, SubjectRelation: "viewer",
			Relation: "viewer", ResourceType: "unit", ResourceID: nodes[index+1].ID,
		})
		if index == len(nodes)-2 {
			assert.ErrorIs(t, err, ErrRelationshipHierarchy)
		} else {
			require.NoError(t, err)
		}
	}
	allowed, err := harness.authorizer.CheckEntity(
		t.Context(), tenantID, nodes[len(nodes)-2].ID, "entity_view", principalID,
	)
	require.NoError(t, err)
	assert.True(t, allowed, "every relationship depth accepted by the writer must be reachable by authorization")
	allowed, err = harness.authorizer.CheckEntity(
		t.Context(), tenantID, nodes[len(nodes)-1].ID, "entity_view", principalID,
	)
	require.NoError(t, err)
	assert.False(t, allowed, "the rejected edge must not become reachable")
}

func TestStoredInvalidRoleConditionReturnsEvaluationError(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	role, err := harness.service.CreateRole(t.Context(), tenantID, "Broken", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, role.ID, "role_view")
	require.NoError(t, err)
	_, err = harness.service.AddMember(t.Context(), tenantID, role.ID, principalID)
	require.NoError(t, err)
	require.NoError(t, harness.db.Table("sl_iam_role_permissions").
		Where("tenant_id = ? AND role_id = ?", tenantID, role.ID).
		Update("condition_json", "{broken").Error)
	_, err = harness.authorizer.Check(t.Context(), tenantID, "role_view", principalID)
	assert.ErrorContains(t, err, "evaluate role permission condition")
}

func TestStoredInvalidRelationshipConditionReturnsEvaluationError(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)
	_, _, err = harness.service.PutRelationship(t.Context(), Relationship{
		TenantID: tenantID, SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "store", ResourceID: entity.ID,
	})
	require.NoError(t, err)
	require.NoError(t, harness.db.Table("sl_iam_relationships").
		Where("tenant_id = ? AND resource_id = ?", tenantID, entity.ID).
		Update("condition_json", "{broken").Error)
	_, err = harness.authorizer.CheckEntity(t.Context(), tenantID, entity.ID, "store_view", principalID)
	assert.ErrorContains(t, err, "evaluate relationship condition")
}

func TestResourceRelationshipDoesNotAuthorizeCollidingEntityID(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	owner, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "company", Name: "Owner",
	})
	require.NoError(t, err)
	colliding, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "store", Name: "Unrelated entity",
	})
	require.NoError(t, err)
	_, _, err = harness.service.PutRelationship(t.Context(), Relationship{
		TenantID: tenantID, EntityID: owner.ID,
		SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "store", ResourceID: colliding.ID,
	})
	require.NoError(t, err)

	constraint, err := harness.authorizer.Constraint(t.Context(), tenantID, "store_view", principalID)
	require.NoError(t, err)
	assert.NotContains(t, constraint.ResourceIDs, colliding.ID.String())
	allowed, err := harness.authorizer.CheckEntity(
		t.Context(), tenantID, colliding.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.False(t, allowed)
	allowed, err = harness.authorizer.CheckResource(
		t.Context(), tenantID, owner.ID, "store", colliding.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestResourceRelationshipWindowUpdateAndIdempotency(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	entity, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)
	startsAt := repositoryTestTime.Add(time.Minute)
	endsAt := repositoryTestTime.Add(time.Hour)
	relationship := Relationship{
		TenantID: tenantID, EntityID: entity.ID,
		SubjectType: "principal", SubjectID: principalID,
		Relation: "viewer", ResourceType: "store", ResourceID: 501,
		StartsAt: &startsAt, EndsAt: &endsAt,
	}
	stored, changed, err := harness.service.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.True(t, changed)
	createdAt := stored.CreatedAt
	allowed, err := harness.authorizer.CheckResource(
		t.Context(), tenantID, entity.ID, "store", 501, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.False(t, allowed, "future relationship must not activate early")
	*harness.now = repositoryTestTime.Add(30 * time.Minute)
	allowed, err = harness.authorizer.CheckResource(
		t.Context(), tenantID, entity.ID, "store", 501, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.True(t, allowed)

	updatedEnd := repositoryTestTime.Add(2 * time.Hour)
	relationship.StartsAt, relationship.EndsAt = nil, &updatedEnd
	stored, changed, err = harness.service.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, createdAt, stored.CreatedAt)
	assert.Nil(t, stored.StartsAt)
	_, changed, err = harness.service.PutRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.False(t, changed)
	filtered, err := harness.service.ListRelationships(t.Context(), tenantID, RelationshipFilter{
		EntityID: entity.ID, ResourceType: "store", ResourceID: 501,
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, guid.ID(501), filtered[0].ResourceID)
	changed, err = harness.service.DeleteRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = harness.service.DeleteRelationship(t.Context(), relationship)
	require.NoError(t, err)
	assert.False(t, changed)

	_, _, err = harness.service.PutRelationship(t.Context(), Relationship{
		TenantID: tenantID, EntityID: entity.ID,
		SubjectType: "principal", SubjectID: principalID,
		Relation: "owner", ResourceType: "service_account", ResourceID: 502,
	})
	assert.ErrorIs(t, err, ErrInvalidRelationship)
}

func TestAuthorizerScopedAdministratorDenyAndPlatformExpansion(t *testing.T) {
	harness := newCoreServiceHarness(t)
	tenantID, principalID := guid.ID(1), guid.ID(11)
	harness.seedMember(t, tenantID, principalID, MemberActive)
	company, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, Type: "company", Name: "Company",
	})
	require.NoError(t, err)
	store, err := harness.service.CreateEntity(t.Context(), Entity{
		TenantID: tenantID, ParentID: company.ID, Type: "store", Name: "Store",
	})
	require.NoError(t, err)
	admin, err := harness.service.CreateRole(t.Context(), tenantID, "Scoped administrator", "")
	require.NoError(t, err)
	_, err = harness.service.GrantPermission(t.Context(), tenantID, admin.ID, "tenant_administer")
	require.NoError(t, err)
	_, _, err = harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, company.ID, admin.ID, principalID, BindingAllow, time.Time{},
	)
	require.NoError(t, err)
	_, _, err = harness.service.PutEntityRoleBinding(
		t.Context(), tenantID, store.ID, admin.ID, principalID, BindingDeny, time.Time{},
	)
	require.NoError(t, err)
	explanation, err := harness.authorizer.ExplainEntity(
		t.Context(), tenantID, store.ID, "store_view", principalID,
	)
	require.NoError(t, err)
	assert.False(t, explanation.Allowed)
	assert.Equal(t, "administrator_scope_denied", explanation.Reason)

	platformID := guid.ID(21)
	harness.seedAdditionalMember(t, tenantID, platformID, MemberActive)
	_, err = harness.service.SetPlatformAdministrator(t.Context(), platformID, true, nil)
	require.NoError(t, err)
	bulk, err := harness.authorizer.CheckBulk(
		t.Context(), tenantID, []string{"store_view", "platform_administer", "missing"}, platformID,
	)
	require.NoError(t, err)
	assert.True(t, bulk["store_view"])
	assert.True(t, bulk["platform_administer"])
	assert.False(t, bulk["missing"], "administrator expansion applies only to declared permissions")
}

// TestAuthorizerRefusesEveryDecisionUntilTheJoinedTablesArePublished is the
// authorization half of the same property: until the owners publish the tables
// the evaluator JOINs, no decision may be produced. Answering "denied" would be
// just as wrong as answering "allowed" -- both are decisions taken from no
// policy at all -- so every entry point has to report unavailability instead.
func TestAuthorizerRefusesEveryDecisionUntilTheJoinedTablesArePublished(t *testing.T) {
	t.Parallel()

	harness := newRepositoryHarness(t, "sl_")
	unbound := NewAuthorizer(harness.runtime)
	unbound.now = harness.repository.now
	ctx := t.Context()
	tenantID, entityID, subject := guid.ID(1), guid.ID(2), guid.ID(11)

	for name, call := range map[string]func() error{
		"check":          func() error { _, err := unbound.Check(ctx, tenantID, "user_view", subject); return err },
		"check bulk":     func() error { _, err := unbound.CheckBulk(ctx, tenantID, []string{"user_view"}, subject); return err },
		"check platform": func() error { _, err := unbound.CheckPlatform(ctx, "platform_administer", subject); return err },
		"constraint":     func() error { _, err := unbound.Constraint(ctx, tenantID, "user_view", subject); return err },
		"entity check":   func() error { _, err := unbound.CheckEntity(ctx, tenantID, entityID, "user_view", subject); return err },
		"entity constraint": func() error {
			_, err := unbound.EntityConstraint(ctx, tenantID, entityID, "user_view", subject)
			return err
		},
		"resource check": func() error {
			_, err := unbound.CheckResource(ctx, tenantID, entityID, "merchant", guid.ID(7), "merchant_view", subject)
			return err
		},
		"explain entity": func() error {
			_, err := unbound.ExplainEntity(ctx, tenantID, entityID, "user_view", subject)
			return err
		},
		"explain resource": func() error {
			_, err := unbound.ExplainResource(ctx, tenantID, entityID, "merchant", guid.ID(7), "merchant_view", subject)
			return err
		},
		"field constraint": func() error {
			_, err := unbound.FieldConstraint(ctx, tenantID, entityID, authz.ProfileResource, subject)
			return err
		},
		"member authorization sources": func() error {
			_, err := unbound.MemberAuthorizationSources(ctx, tenantID, subject)
			return err
		},
		"authorization subjects": func() error {
			return harness.db.Transaction(func(tx *gorm.DB) error {
				_, err := unbound.AuthorizationSubjectsOn(ctx, tx, tenantID, entityID, subject)
				return err
			})
		},
		"authorization references": func() error {
			return harness.db.Transaction(func(tx *gorm.DB) error {
				_, err := unbound.AuthorizationReferencesOn(ctx, tx, tenantID, AuthorizationReferences{EntityID: entityID, PrincipalID: subject})
				return err
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, call(), ErrUnavailable,
				"%s produced a decision from tables that were never published", name)
		})
	}

	// Once the owners publish, the same evaluator answers instead of refusing --
	// so the refusals above came from the missing names, not from a broken
	// evaluator that would refuse whatever it was given.
	require.NoError(t, unbound.BindForeignTables(publishedDirectoryTables("sl_"), publishedPrincipalTable("sl_")))
	allowed, err := unbound.Check(ctx, tenantID, "user_view", subject)
	require.NoError(t, err)
	require.False(t, allowed, "a subject with no grant is denied once the names are known")
}
