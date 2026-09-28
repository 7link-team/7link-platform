package core

import (
	"errors"
	"net/http"
	"testing"
	"time"

	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPIDParsingRejectsUnusableIdentifiers(t *testing.T) {
	id, err := guid.RequireID(" 42 ")
	require.NoError(t, err)
	assert.Equal(t, guid.ID(42), id)

	for _, value := range []string{"", "0", "-1", "abc", "18446744073709551616"} {
		_, err := guid.RequireID(value)
		assertHTTPError(t, err, http.StatusUnprocessableEntity, "respx.invalid_id")
	}

	id, err = guid.OptionalID("  ")
	require.NoError(t, err)
	assert.True(t, id.Zero())
	_, err = guid.OptionalID("bad")
	assertHTTPError(t, err, http.StatusUnprocessableEntity, "respx.invalid_id")

	ids, err := requireIDs([]string{"1", "2"})
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{1, 2}, ids)
	_, err = requireIDs([]string{"1", "bad", "2"})
	assertHTTPError(t, err, http.StatusUnprocessableEntity, "respx.invalid_id")
}

func TestHTTPReferenceParsingRejectsEachInvalidComponent(t *testing.T) {
	ownedContext := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1})
	for _, test := range []struct {
		name string
		call func() error
	}{
		{name: "role id", call: func() error { _, _, err := roleRefIDs(ownedContext, "bad"); return err }},
		{name: "member role", call: func() error {
			_, _, _, err := memberRef(ownedContext, &memberInput{RoleID: "bad", Subject: "2"})
			return err
		}},
		{name: "member subject", call: func() error {
			_, _, _, err := memberRef(ownedContext, &memberInput{RoleID: "2", Subject: "bad"})
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
		{name: "menu id", call: func() error { _, _, err := menuRef(ownedContext, &menuInput{MenuID: "bad"}); return err }},
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
		{ErrEntityHasFiles, http.StatusConflict, corei18n.EntityHasFiles},
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

}

func assertHTTPError(t *testing.T, err error, status int, message string) {
	t.Helper()
	var statusError huma.StatusError
	require.ErrorAs(t, err, &statusError)
	assert.Equal(t, status, statusError.GetStatus())
	assert.Equal(t, message, statusError.Error())
}

// TestErrorMappersKeepChosenStatus is this package's half of the set-wide rule in
// tests/architecture (domainErrorMappers): an error that already chose its
// status, headers included, leaves every mapper unchanged instead of becoming 500.
func TestErrorMappersKeepChosenStatus(t *testing.T) {
	chosen := huma.ErrorWithHeaders(huma.Error422UnprocessableEntity(respx.InvalidID), http.Header{"Retry-After": {"1"}})
	for name, mapper := range map[string]func(error) error{
		"apiError":         func(err error) error { return apiError("test", err) },
		"platformAPIError": func(err error) error { return platformAPIError("test", err) },
	} {
		mapped := mapper(chosen)
		var status huma.StatusError
		require.ErrorAs(t, mapped, &status, name)
		assert.Equal(t, http.StatusUnprocessableEntity, status.GetStatus(), name)
		var headers huma.HeadersError
		require.ErrorAs(t, mapped, &headers, name)
		assert.Equal(t, "1", headers.GetHeaders().Get("Retry-After"), name)
	}
}
