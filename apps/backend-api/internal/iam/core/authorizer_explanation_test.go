package core

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/iam/core/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestExplainOnLocksSystemBeforeTenantPolicy(t *testing.T) {
	h, _, _ := transactionAuthorizationFixture(t)
	root := seedSystemAccessFixture(t, h.db, "sl_", 11)
	var fences []string
	require.NoError(t, h.db.Callback().Update().After("gorm:update").Register("test:explanation_fences", func(db *gorm.DB) {
		if db.Statement.Table == h.authorizer.revisions.table && db.Error == nil {
			fences = append(fences, fmt.Sprint(db.Statement.Vars[len(db.Statement.Vars)-1]))
		}
	}))
	t.Cleanup(func() { _ = h.db.Callback().Update().Remove("test:explanation_fences") })
	request := AuthorizationExplanationRequest{TenantID: 1, PrincipalID: 11, Permission: "user_view"}
	_, err := h.authorizer.ExplainOn(t.Context(), nil, request)
	require.NoError(t, err)
	require.Empty(t, fences, "preview must remain unfenced")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		result, err := h.authorizer.ExplainOn(t.Context(), tx, request)
		require.NoError(t, err)
		require.True(t, result.Allowed, "tenant grant does not require system permission")
		return nil
	}))
	require.Equal(t, []string{root.String(), "1"}, fences, "project provisioning must not invert IAM mutation lock order")
	require.Nil(t, h.authorizer.db)
	fences = nil
	failure := errors.New("policy fence unavailable")
	require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("test:explanation_fence_failure", func(db *gorm.DB) {
		if db.Statement.Table == h.authorizer.revisions.table {
			db.AddError(failure)
		}
	}))
	t.Cleanup(func() { _ = h.db.Callback().Update().Remove("test:explanation_fence_failure") })
	err = h.db.Transaction(func(tx *gorm.DB) error {
		result, err := h.authorizer.ExplainOn(t.Context(), tx, request)
		require.Equal(t, authz.Explanation{}, result)
		return err
	})
	require.ErrorIs(t, err, failure)
	require.Empty(t, fences, "a failed fence cannot proceed to tenant authorization")
}

func TestExplainOnSeesTransactionalRevocationAndPreservesProvenance(t *testing.T) {
	h, entity, role := transactionAuthorizationFixture(t)
	request := AuthorizationExplanationRequest{TenantID: 1, PrincipalID: 11, Permission: "user_view"}
	initial, err := h.authorizer.ExplainOn(t.Context(), nil, request)
	require.NoError(t, err)
	require.True(t, initial.Allowed)
	require.NotEmpty(t, initial.Matches)
	require.Equal(t, role.ID.String(), initial.Matches[0].RoleID)
	require.Equal(t, "tenant", initial.Matches[0].ScopeType)
	aborted := errors.New("business write failed")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		before, err := h.authorizer.ExplainOn(t.Context(), tx, request)
		require.NoError(t, err)
		require.Equal(t, initial, before)
		require.NoError(t, tx.Table(query.BindNames("sl_").RolePermissions.String()).Where("tenant_id = ? AND role_id = ? AND permission_code = ?", 1, role.ID, "user_view").Delete(&model.RolePermission{}).Error)
		require.NoError(t, h.authorizer.revisions.Bump(t.Context(), tx, 1, *h.now))
		denied, err := h.authorizer.ExplainOn(t.Context(), tx, request)
		require.NoError(t, err)
		require.False(t, denied.Allowed)
		require.Empty(t, denied.Matches)
		require.Greater(t, denied.Revision, before.Revision)
		return aborted
	}), aborted)
	restored, err := h.authorizer.ExplainOn(t.Context(), nil, request)
	require.NoError(t, err)
	require.Equal(t, initial, restored)
	require.Nil(t, h.authorizer.db)
	request.EntityID = entity.ID
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		actual, err := h.authorizer.ExplainOn(t.Context(), tx, request)
		require.NoError(t, err)
		expected, err := h.authorizer.ExplainEntity(t.Context(), 1, entity.ID, "user_view", 11)
		require.NoError(t, err)
		require.Equal(t, expected, actual)
		request.ResourceType = "task"
		request.ResourceID = 123
		actual, err = h.authorizer.ExplainOn(t.Context(), tx, request)
		require.NoError(t, err)
		require.True(t, actual.Allowed)
		require.Equal(t, role.ID.String(), actual.Matches[0].RoleID)
		return nil
	}))
}

func TestExplainOnEntityGrantDoesNotGrantTenantAction(t *testing.T) {
	h, entity, role := transactionAuthorizationFixture(t)
	_, err := h.service.RemoveMember(t.Context(), 1, role.ID, 11)
	require.NoError(t, err)
	_, _, err = h.service.PutEntityRoleBinding(t.Context(), 1, entity.ID, role.ID, 11, BindingAllow, time.Time{})
	require.NoError(t, err)
	request := AuthorizationExplanationRequest{TenantID: 1, PrincipalID: 11, Permission: "user_view", EntityID: entity.ID}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		scoped, err := h.authorizer.ExplainOn(t.Context(), tx, request)
		require.NoError(t, err)
		require.True(t, scoped.Allowed)
		require.Equal(t, "entity_binding", scoped.Matches[0].SourceType)
		request.EntityID = 0
		tenant, err := h.authorizer.ExplainOn(t.Context(), tx, request)
		require.NoError(t, err)
		require.False(t, tenant.Allowed)
		require.Empty(t, tenant.Matches)
		return nil
	}))
}

func TestExplainOnRejectsInvalidBoundaryAndTransaction(t *testing.T) {
	h, _, _ := transactionAuthorizationFixture(t)
	valid := AuthorizationExplanationRequest{TenantID: 1, PrincipalID: 11, Permission: "user_view"}
	for _, mutate := range []func(*AuthorizationExplanationRequest){
		func(r *AuthorizationExplanationRequest) { r.TenantID = 0 },
		func(r *AuthorizationExplanationRequest) { r.PrincipalID = 0 },
		func(r *AuthorizationExplanationRequest) { r.Permission = "unknown" },
		func(r *AuthorizationExplanationRequest) { r.Permission = "platform_administer" },
		func(r *AuthorizationExplanationRequest) { r.EntityID = -1 },
		func(r *AuthorizationExplanationRequest) { r.ResourceType = "task" },
		func(r *AuthorizationExplanationRequest) { r.ResourceID = 2 },
		func(r *AuthorizationExplanationRequest) { r.ResourceType = "task"; r.ResourceID = 2 },
	} {
		request := valid
		mutate(&request)
		result, err := h.authorizer.ExplainOn(t.Context(), nil, request)
		require.Error(t, err)
		require.Equal(t, authz.Explanation{}, result)
	}
	for _, tx := range []*gorm.DB{h.db, {}} {
		_, err := h.authorizer.ExplainOn(t.Context(), tx, valid)
		require.ErrorIs(t, err, ErrInvalidArgument)
	}
	_, err := (*Authorizer)(nil).ExplainOn(t.Context(), nil, valid)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestExplainOnPreservesExplicitDenyAndInactiveMembership(t *testing.T) {
	h, entity, role := transactionAuthorizationFixture(t)
	_, _, err := h.service.PutEntityRoleBinding(t.Context(), 1, entity.ID, role.ID, 11, BindingDeny, time.Time{})
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		result, err := h.authorizer.ExplainOn(t.Context(), tx, AuthorizationExplanationRequest{TenantID: 1, EntityID: entity.ID, PrincipalID: 11, Permission: "user_view"})
		require.NoError(t, err)
		require.False(t, result.Allowed)
		require.Equal(t, "explicit_deny", result.Reason)
		require.Len(t, result.Matches, 2)
		return nil
	}))
	inactive := newCoreServiceHarness(t)
	inactive.seedMember(t, 1, 11, MemberDisabled)
	result, err := inactive.authorizer.ExplainOn(t.Context(), nil, AuthorizationExplanationRequest{TenantID: 1, PrincipalID: 11, Permission: "user_view"})
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, "inactive_membership", result.Reason)
	require.Empty(t, result.Matches)
}
