package core

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPIDParsingRejectsUnusableIdentifiers(t *testing.T) {
	id, err := requireID(" 42 ")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(42), id)

	for _, value := range []string{"", "0", "-1", "abc", "18446744073709551616"} {
		_, err := requireID(value)
		assertHTTPError(t, err, http.StatusUnprocessableEntity, "respx.invalid_id")
	}

	id, err = optionalID("  ")
	require.NoError(t, err)
	assert.True(t, id.Zero())
	_, err = optionalID("bad")
	assertHTTPError(t, err, http.StatusUnprocessableEntity, "respx.invalid_id")

	ids, err := requireIDs([]string{"1", "2"})
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{1, 2}, ids)
	_, err = requireIDs([]string{"1", "bad", "2"})
	assertHTTPError(t, err, http.StatusUnprocessableEntity, "respx.invalid_id")
}

func TestHTTPReferenceParsingRejectsEachInvalidComponent(t *testing.T) {
	for _, test := range []struct {
		name string
		call func() error
	}{
		{name: "role tenant", call: func() error { _, _, err := roleRefIDs("bad", "1"); return err }},
		{name: "role id", call: func() error { _, _, err := roleRefIDs("1", "bad"); return err }},
		{name: "member tenant", call: func() error {
			_, _, _, err := memberRef(&memberInput{TenantID: "bad", RoleID: "1", Subject: "2"})
			return err
		}},
		{name: "member role", call: func() error {
			_, _, _, err := memberRef(&memberInput{TenantID: "1", RoleID: "bad", Subject: "2"})
			return err
		}},
		{name: "member subject", call: func() error {
			_, _, _, err := memberRef(&memberInput{TenantID: "1", RoleID: "2", Subject: "bad"})
			return err
		}},
		{name: "tenant member tenant", call: func() error {
			_, _, err := tenantMemberRef(&tenantMemberInput{TenantID: "bad", Subject: "2"})
			return err
		}},
		{name: "tenant member subject", call: func() error {
			_, _, err := tenantMemberRef(&tenantMemberInput{TenantID: "1", Subject: "bad"})
			return err
		}},
		{name: "menu tenant", call: func() error { _, _, err := menuRef(&menuInput{TenantID: "bad", MenuID: "2"}); return err }},
		{name: "menu id", call: func() error { _, _, err := menuRef(&menuInput{TenantID: "1", MenuID: "bad"}); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertHTTPError(t, test.call(), http.StatusUnprocessableEntity, "respx.invalid_id")
		})
	}
}

func TestHTTPRelationshipParsingPreservesOptionalFields(t *testing.T) {
	startsAt := time.Date(2026, time.September, 3, 8, 0, 0, 0, time.UTC)
	endsAt := startsAt.Add(time.Hour)
	fields := relationshipFields{
		EntityID: "2", SubjectType: "entity", SubjectID: "3", SubjectRelation: "owner",
		Relation: "viewer", ResourceType: "store", ResourceID: "4", StartsAt: &startsAt,
		EndsAt: &endsAt, Condition: policyCondition(`{"version":1}`),
	}
	relationship, err := relationshipToDomain("1", fields)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(1), relationship.TenantID)
	assert.Equal(t, guid.ID(2), relationship.EntityID)
	assert.Equal(t, startsAt, *relationship.StartsAt)
	assert.JSONEq(t, `{"version":1}`, string(relationship.Condition))

	for _, test := range []struct {
		name     string
		tenantID string
		mutate   func(*relationshipFields)
	}{
		{name: "tenant", tenantID: "bad"},
		{name: "entity", tenantID: "1", mutate: func(value *relationshipFields) { value.EntityID = "bad" }},
		{name: "subject", tenantID: "1", mutate: func(value *relationshipFields) { value.SubjectID = "bad" }},
		{name: "resource", tenantID: "1", mutate: func(value *relationshipFields) { value.ResourceID = "bad" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := fields
			if test.mutate != nil {
				test.mutate(&invalid)
			}
			_, err := relationshipToDomain(test.tenantID, invalid)
			assertHTTPError(t, err, http.StatusUnprocessableEntity, "respx.invalid_id")
		})
	}
}

func TestHTTPErrorMapsEveryDomainError(t *testing.T) {
	for _, test := range []struct {
		err     error
		status  int
		message string
	}{
		{ErrRoleNotFound, http.StatusNotFound, corei18n.ResourceNotFound},
		{ErrRoleNameConflict, http.StatusConflict, corei18n.RoleNameConflict},
		{ErrMemberNotFound, http.StatusNotFound, corei18n.ResourceNotFound},
		{ErrMenuNotFound, http.StatusNotFound, corei18n.ResourceNotFound},
		{ErrMenuConflict, http.StatusConflict, corei18n.ResourceConflict},
		{ErrMenuHasChildren, http.StatusConflict, corei18n.ResourceConflict},
		{ErrMemberInactive, http.StatusConflict, corei18n.TenantMemberInactive},
		{ErrDirectoryAssigneeNotFound, http.StatusNotFound, corei18n.DirectoryAssigneeNotFound},
		{ErrDirectoryAssigneeInactive, http.StatusConflict, corei18n.DirectoryAssigneeInactive},
		{ErrLastTenantAdministrator, http.StatusConflict, corei18n.LastTenantAdministrator},
		{ErrEntityNotFound, http.StatusNotFound, corei18n.EntityNotFound},
		{ErrEntityConflict, http.StatusConflict, corei18n.EntityConflict},
		{ErrEntityHasChildren, http.StatusConflict, corei18n.EntityHasChildren},
		{ErrEntityHasBindings, http.StatusConflict, corei18n.EntityHasRoleBindings},
		{ErrEntityHasRelationships, http.StatusConflict, corei18n.EntityHasRelationships},
		{ErrEntityHierarchy, http.StatusConflict, corei18n.EntityHierarchyConflict},
		{ErrInvalidRelationship, http.StatusUnprocessableEntity, corei18n.InvalidRelationship},
		{ErrRelationshipSubjectMissing, http.StatusNotFound, corei18n.RelationshipSubjectNotFound},
		{ErrRelationshipSubjectInactive, http.StatusConflict, corei18n.RelationshipSubjectInactive},
		{ErrRelationshipResourceInactive, http.StatusConflict, corei18n.RelationshipResourceInactive},
		{ErrRelationshipHierarchy, http.StatusConflict, corei18n.RelationshipHierarchyConflict},
		{ErrInvalidRelationshipWindow, http.StatusUnprocessableEntity, corei18n.InvalidRelationshipWindow},
		{ErrInvalidRelationshipCondition, http.StatusUnprocessableEntity, corei18n.InvalidRelationshipCondition},
		{ErrInvalidResourceAuthorization, http.StatusUnprocessableEntity, corei18n.InvalidResourceAuthorization},
		{ErrAuthorizationChanged, http.StatusServiceUnavailable, corei18n.AuthorizationPolicyChanged},
		{ErrInvalidRoleDataScope, http.StatusUnprocessableEntity, corei18n.InvalidRoleDataScope},
		{ErrRoleScopeDepartmentNeeded, http.StatusUnprocessableEntity, corei18n.RoleDataScopeDepartmentRequired},
		{ErrRoleScopeDepartmentsExtra, http.StatusUnprocessableEntity, corei18n.RoleDataScopeDepartmentsNotAllowed},
		{ErrRoleScopeDepartmentMissing, http.StatusNotFound, corei18n.RoleScopeDepartmentNotFound},
		{ErrRoleScopeDepartmentInactive, http.StatusConflict, corei18n.RoleScopeDepartmentInactive},
		{ErrMemberDepartmentMissing, http.StatusNotFound, corei18n.MemberDepartmentNotFound},
		{ErrMemberDepartmentInactive, http.StatusConflict, corei18n.MemberDepartmentInactive},
		{ErrPermissionNotFound, http.StatusUnprocessableEntity, corei18n.PermissionNotFound},
		{ErrRolePermissionNotGranted, http.StatusNotFound, corei18n.RolePermissionNotGranted},
		{ErrInvalidRolePermissionCondition, http.StatusUnprocessableEntity, corei18n.InvalidRolePermissionCondition},
		{ErrPrivilegeEscalation, http.StatusForbidden, corei18n.PrivilegeEscalationDenied},
		{ErrInvalidArgument, http.StatusUnprocessableEntity, corei18n.InvalidRequest},
		{ErrUnavailable, http.StatusServiceUnavailable, corei18n.Unavailable},
	} {
		t.Run(test.err.Error(), func(t *testing.T) {
			assertHTTPError(t, apiError("test", errors.Join(errors.New("context"), test.err)), test.status, test.message)
		})
	}

	assertHTTPError(t, apiError("test", errors.New("unknown")), http.StatusInternalServerError, "respx.internal_server_error")
}

func TestPlatformHTTPErrorMapsEveryDomainError(t *testing.T) {
	for _, test := range []struct {
		err     error
		status  int
		message string
	}{
		{ErrPlatformAdministratorNotFound, http.StatusNotFound, corei18n.PlatformAdministratorNotFound},
		{ErrLastPlatformAdministrator, http.StatusConflict, corei18n.LastPlatformAdministrator},
		{ErrPlatformPermissionScope, http.StatusUnprocessableEntity, corei18n.PlatformPermissionScope},
		{ErrUnavailable, http.StatusServiceUnavailable, corei18n.Unavailable},
	} {
		assertHTTPError(t, platformAPIError("test", test.err), test.status, test.message)
	}
}

func TestHTTPProjectionAndPaginationPreserveWireSemantics(t *testing.T) {
	now := time.Date(2026, time.September, 3, 9, 0, 0, 0, time.UTC)
	binding := entityBindingFromDomain(EntityRoleBinding{EntityID: 1, RoleID: 2, PrincipalID: 3, ExpiresAt: now})
	require.NotNil(t, binding.ExpiresAt)
	assert.Equal(t, now, *binding.ExpiresAt)

	member := memberFromDomain(TenantMember{TenantID: 1, PrincipalID: 2, DepartmentID: 3, DisabledAt: now})
	require.NotNil(t, member.DisabledAt)
	assert.Equal(t, guid.ID(3), member.DepartmentID)
	assert.Equal(t, now, *member.DisabledAt)

	administrator := platformAdministratorFromDomain(PlatformAdministrator{PrincipalID: 1})
	assert.NotNil(t, administrator.Permissions)
	assert.Empty(t, administrator.Permissions)

	page := paginateList(context.Background(), []int{0, 1, 2, 3}, 1, 2)
	assert.Equal(t, []int{1, 2}, page.Body.Data)
	require.NotNil(t, page.Body.Meta.Page)
	assert.Equal(t, int64(4), page.Body.Meta.Page.Total)

	assert.Empty(t, paginateList(context.Background(), []int{1}, 2, 10).Body.Data)
	assert.Equal(t, []int{0, 1, 2, 3}, paginateList(context.Background(), []int{0, 1, 2, 3}, -1, -1).Body.Data)
	assert.Equal(t, []int{2, 3}, paginateList(context.Background(), []int{0, 1, 2, 3}, 2, 0).Body.Data)
	assert.Equal(t, []int{0, 1, 2, 3}, paginateList(context.Background(), []int{0, 1, 2, 3}, 0, 201).Body.Data)
	assert.Equal(t, []int{2, 3}, paginateList(context.Background(), []int{0, 1, 2, 3}, 2, 10).Body.Data)
}

func assertHTTPError(t *testing.T, err error, status int, message string) {
	t.Helper()
	var statusError huma.StatusError
	require.ErrorAs(t, err, &statusError)
	assert.Equal(t, status, statusError.GetStatus())
	assert.Equal(t, message, statusError.Error())
}
