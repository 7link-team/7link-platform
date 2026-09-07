package core

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFieldPolicyLifecycleRevisionIsolationAndFailClosed(t *testing.T) {
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	h.seedAdditionalMember(t, 1, 12, MemberActive)
	h.seedMember(t, 2, 21, MemberActive)
	admin, err := h.service.CreateRole(t.Context(), 1, "Policy administrators", "")
	require.NoError(t, err)
	for _, code := range []string{"field_policy_view", "field_policy_update"} {
		_, err = h.service.GrantPermission(t.Context(), 1, admin.ID, code)
		require.NoError(t, err)
	}
	_, err = h.service.AddMember(t.Context(), 1, admin.ID, 11)
	require.NoError(t, err)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11})
	reader, err := h.service.CreateRole(t.Context(), 1, "Field readers", "")
	require.NoError(t, err)
	_, err = h.service.AddMember(t.Context(), 1, reader.ID, 12)
	require.NoError(t, err)
	policy := authz.FieldPolicy{Read: authz.FieldAccess{Default: "deny", AllowRoles: []guid.ID{reader.ID}}, Write: authz.FieldAccess{Default: "deny"}, Mask: "redact"}
	before, err := h.authorizer.FieldConstraint(t.Context(), 1, 0, "todo", 12)
	require.NoError(t, err)
	assert.Equal(t, "allow", before.Fields["description"].Read)
	saved, err := h.service.SetFieldPolicy(ctx, 1, "todo", "description", policy, 0)
	require.NoError(t, err)
	assert.Greater(t, saved.Version, before.Revision)
	after, err := h.authorizer.FieldConstraint(t.Context(), 1, 0, "todo", 12)
	require.NoError(t, err)
	assert.Greater(t, after.Revision, before.Revision)
	assert.Equal(t, "allow", after.Fields["description"].Read, "a role with no endpoint grants still participates in field policy")
	assert.False(t, after.Fields["description"].Update)
	entity, err := h.service.CreateEntity(t.Context(), Entity{TenantID: 1, Type: "store", Name: "Field scope"})
	require.NoError(t, err)
	_, _, err = h.service.PutEntityRoleBinding(t.Context(), 1, entity.ID, reader.ID, 11, BindingAllow, repositoryTestTime.AddDate(1, 0, 0))
	require.NoError(t, err)
	entityFields, err := h.authorizer.FieldConstraint(t.Context(), 1, entity.ID, "todo", 11)
	require.NoError(t, err)
	assert.Equal(t, "allow", entityFields.Fields["description"].Read)
	_, err = h.service.SetFieldPolicy(ctx, 1, "todo", "description", policy, 0)
	assert.ErrorIs(t, err, ErrFieldPolicyConflict)
	_, err = h.service.SetFieldPolicy(ctx, 2, "todo", "description", policy, 0)
	assert.ErrorIs(t, err, ErrPrivilegeEscalation)
	foreign, err := h.service.CreateRole(t.Context(), 2, "Foreign", "")
	require.NoError(t, err)
	policy.Read.AllowRoles = []guid.ID{foreign.ID}
	_, err = h.service.SetFieldPolicy(ctx, 1, "todo", "description", policy, saved.Version)
	assert.ErrorIs(t, err, ErrRoleNotFound)
	policy.Read.AllowRoles, policy.Read.DenyRoles = []guid.ID{reader.ID}, []guid.ID{reader.ID}
	updated, err := h.service.SetFieldPolicy(ctx, 1, "todo", "description", policy, saved.Version)
	require.NoError(t, err)
	denied, err := h.authorizer.FieldConstraint(t.Context(), 1, 0, "todo", 12)
	require.NoError(t, err)
	assert.Equal(t, "redact", denied.Fields["description"].Read)
	other, err := h.authorizer.FieldConstraint(t.Context(), 2, 0, "todo", 21)
	require.NoError(t, err)
	assert.Equal(t, "allow", other.Fields["description"].Read)
	listed, err := h.service.ListFieldPolicies(ctx, 1, "todo")
	require.NoError(t, err)
	assert.NotEmpty(t, listed)
	assert.ErrorIs(t, h.service.DeleteFieldPolicy(ctx, 1, "todo", "description", saved.Version), ErrFieldPolicyConflict)
	require.NoError(t, h.service.DeleteFieldPolicy(ctx, 1, "todo", "description", updated.Version))
	restored, err := h.authorizer.FieldConstraint(t.Context(), 1, 0, "todo", 12)
	require.NoError(t, err)
	assert.Equal(t, "allow", restored.Fields["description"].Read)
	recreated, err := h.service.SetFieldPolicy(ctx, 1, "todo", "description", policy, 0)
	require.NoError(t, err)
	assert.Greater(t, recreated.Version, updated.Version)
	_, err = h.service.SetFieldPolicy(ctx, 1, "todo", "description", policy, saved.Version)
	assert.ErrorIs(t, err, ErrFieldPolicyConflict)
	assert.ErrorIs(t, h.service.DeleteFieldPolicy(ctx, 1, "todo", "description", updated.Version), ErrFieldPolicyConflict)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_field_policies SET policy_json = ?", `{"read":{"default":"typo"}}`).Error)
	_, err = h.authorizer.FieldConstraint(t.Context(), 1, 0, "todo", 12)
	assert.ErrorIs(t, err, ErrUnavailable)
	_, err = h.service.ListFieldPolicies(ctx, 1, "todo")
	assert.ErrorIs(t, err, ErrUnavailable)
	inactive, err := h.authorizer.FieldConstraint(t.Context(), 1, 0, "todo", 99)
	require.NoError(t, err)
	assert.Empty(t, inactive.Fields)
	var missing Authorizer
	_, err = missing.FieldConstraint(t.Context(), 1, 0, "todo", 12)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestFieldPolicyHTTPManagementAndErrors(t *testing.T) {
	h := newHTTPIAMHarness(t, false)
	header := tenantHeader(1)
	path := "/iam/field-policies/todo/description"
	policy := authz.FieldPolicy{Read: authz.FieldAccess{Default: "deny"}, Write: authz.FieldAccess{Default: "deny"}, Mask: "omit"}
	for _, route := range []string{"/iam/field-catalog", "/iam/field-policies", "/iam/field-policies?resource=todo", "/iam/fields/effective?resource=todo&entity_id=1"} {
		response := h.api.Get(route, header)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	response := h.api.Put(path, header, map[string]any{"version": 0, "policy": policy})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	saved := decodeEnvelope[ManagedFieldPolicy](t, response).Data
	assert.Positive(t, saved.Version)
	response = h.api.Get("/iam/field-policies?resource=todo", header)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"default":"deny"`)
	response = h.api.Put(path, header, map[string]any{"version": 0, "policy": policy})
	assert.Equal(t, http.StatusConflict, response.Code)
	response = h.api.Delete(path+"?version="+strconv.FormatInt(saved.Version, 10), header)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = h.api.Delete(path+"?version="+strconv.FormatInt(saved.Version, 10), header)
	assert.Equal(t, http.StatusConflict, response.Code)
	for _, route := range []string{"/iam/field-policies?resource=unknown", "/iam/fields/effective?resource=todo&entity_id=bad"} {
		assert.Equal(t, http.StatusUnprocessableEntity, h.api.Get(route, header).Code)
	}
	for _, name := range []string{"unknown", "id"} {
		response = h.api.Put("/iam/field-policies/todo/"+name, header, map[string]any{"version": 0, "policy": policy})
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())
		assert.Equal(t, http.StatusUnprocessableEntity, h.api.Delete("/iam/field-policies/todo/"+name+"?version=1", header).Code)
	}
	h.module.service.checker = httpFailingAuthorizationEvaluator{err: ErrUnavailable}
	assert.Equal(t, http.StatusServiceUnavailable, h.api.Get("/iam/field-policies", header).Code)
	assert.Equal(t, http.StatusServiceUnavailable, h.api.Get("/iam/fields/effective?resource=todo", header).Code)
	assert.Equal(t, http.StatusServiceUnavailable, h.api.Put(path, header, map[string]any{"version": 0, "policy": policy}).Code)
	assert.Equal(t, http.StatusServiceUnavailable, h.api.Delete(path+"?version=1", header).Code)
}

func TestFieldPolicyAuditFailureRollsBackAndConcurrentVersionsHaveOneWinner(t *testing.T) {
	h := newHTTPIAMHarness(t, false)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, PrincipalID: 11})
	policy := authz.FieldPolicy{Read: authz.FieldAccess{Default: "deny"}, Write: authz.FieldAccess{Default: "deny"}, Mask: "omit"}
	service := h.module.service
	saved, err := service.SetFieldPolicy(ctx, 1, "todo", "description", policy, 0)
	require.NoError(t, err)
	before, err := h.repository.revisions.Current(ctx, h.db, 1)
	require.NoError(t, err)
	want := errors.New("audit offline")
	failing := NewService(authz.DefaultRegistry(), h.repository, httpAuthorizationEvaluator{}, func(context.Context, *gorm.DB, audit.DomainEvent) error { return want })
	_, err = failing.SetFieldPolicy(ctx, 1, "todo", "description", policy, saved.Version)
	assert.ErrorIs(t, err, want)
	assert.ErrorIs(t, failing.DeleteFieldPolicy(ctx, 1, "todo", "description", saved.Version), want)
	_, err = failing.SetFieldPolicy(ctx, 1, "todo", "title", policy, 0)
	assert.ErrorIs(t, err, want)
	after, err := h.repository.revisions.Current(ctx, h.db, 1)
	require.NoError(t, err)
	assert.Equal(t, before, after)
	rows, err := h.repository.fieldPolicyRows(ctx, 1, "todo")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, saved.Version, rows[0].Version)
	const writers = 8
	results := make(chan error, writers)
	var workers sync.WaitGroup
	for range writers {
		workers.Go(func() {
			_, err := service.SetFieldPolicy(ctx, 1, "todo", "description", policy, saved.Version)
			results <- err
		})
	}
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			assert.ErrorIs(t, err, ErrFieldPolicyConflict)
		}
	}
	assert.Equal(t, 1, winners)
	_, err = service.SetFieldPolicy(ctx, 1, "todo", "description", policy, math.MaxInt64)
	assert.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_policy_revisions SET revision = ? WHERE tenant_id = 1", int64(math.MaxInt64)).Error)
	_, err = service.SetFieldPolicy(ctx, 1, "todo", "title", policy, 0)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestFieldPolicyRejectsMalformedStorageAndInvalidScope(t *testing.T) {
	f, ok := authz.DefaultRegistry().FindField("todo", "description")
	require.True(t, ok)
	for _, raw := range []string{"null", "{", "{}", `{"unknown":true}`, `{"read":{"default":"allow"},"write":{"default":"deny"},"mask":"omit"} {}`} {
		_, err := decodeFieldPolicy(f, raw)
		assert.ErrorIs(t, err, ErrUnavailable)
	}
	secret := defaultFieldPolicy(authz.Field{Mutability: "writeOnly", Returned: "never"})
	assert.Equal(t, "deny", secret.Read.Default)
	h := newCoreServiceHarness(t)
	h.seedMember(t, 1, 11, MemberActive)
	for _, scope := range []struct {
		tenant, entity, principal guid.ID
		resource                  string
	}{
		{0, 0, 11, "todo"}, {1, -1, 11, "todo"}, {1, 0, 0, "todo"}, {1, 0, 11, "unknown"},
	} {
		_, err := h.authorizer.FieldConstraint(t.Context(), scope.tenant, scope.entity, scope.resource, scope.principal)
		assert.ErrorIs(t, err, ErrInvalidArgument)
	}
	constraint, err := h.authorizer.FieldConstraint(t.Context(), 1, 999, "todo", 11)
	require.NoError(t, err)
	assert.Empty(t, constraint.Fields)
	require.NoError(t, h.db.Exec(`INSERT INTO sl_iam_field_policies (tenant_id,resource,field_name,policy_json,version,updated_at) VALUES (1,'todo','unknown','{}',1,?)`, repositoryTestTime).Error)
	_, err = h.authorizer.FieldConstraint(t.Context(), 1, 0, "todo", 11)
	assert.ErrorIs(t, err, ErrUnavailable)
}
