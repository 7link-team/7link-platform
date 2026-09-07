package provisioning

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type provisioningProfileChecker struct {
	fields map[string]authz.FieldDecision
	err    error
}

type provisioningProfileCheckerFunc func(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error)

func (f provisioningProfileCheckerFunc) FieldConstraint(ctx context.Context, tenant, entity guid.ID, resource string, actor guid.ID) (authz.FieldConstraint, error) {
	return f(ctx, tenant, entity, resource, actor)
}

func (c provisioningProfileChecker) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	if c.fields != nil || c.err != nil {
		return authz.FieldConstraint{Fields: c.fields}, c.err
	}
	fields := map[string]authz.FieldDecision{}
	for _, field := range authz.ProfileFields() {
		fields[field.Name] = authz.DecideField(field, nil, nil)
	}
	return authz.FieldConstraint{Fields: fields}, nil
}

func TestSCIMProfileProjectionRejectsInferenceAndPreservesHiddenWrites(t *testing.T) {
	for _, prefix := range []string{"", "fields_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			env := newProvisioningEnvironmentWithPrefix(t, prefix)
			input := activeUserInput("profile", "profile", "private@example.test")
			user, err := env.service.CreateUser(t.Context(), env.auth, input)
			require.NoError(t, err)
			fields, err := env.service.fields.FieldConstraint(t.Context(), env.auth.TenantID, 0, authz.ProfileResource, env.auth.PrincipalID)
			require.NoError(t, err)
			env.service.fields = provisioningProfileChecker{fields: fields.Fields}
			fields.Fields["email"] = authz.FieldDecision{Read: "redact"}
			for _, filter := range []string{`emails.value eq "private@example.test"`, `emails[value co "private"]`, `not (emails.value pr)`, `userName eq "profile" or emails.value pr`} {
				_, err := env.service.ListUsers(t.Context(), env.auth, ListRequest{StartIndex: 1, Count: 10, Filter: filter})
				assert.ErrorIs(t, err, authz.ErrFieldDenied, filter)
			}
			page, err := env.service.ListUsers(t.Context(), env.auth, ListRequest{StartIndex: 1, Count: 10, Filter: `userName eq "profile"`})
			require.NoError(t, err)
			require.Len(t, page.Resources, 1)
			assert.Equal(t, "[REDACTED]", page.Resources[0].Emails[0].Value)
			patch := PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{{Op: "replace", Path: "displayName", Value: json.RawMessage(`"Changed"`)}}}
			changed, err := env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), patch, 1)
			require.NoError(t, err)
			assert.Equal(t, "Changed", changed.DisplayName)
			assert.Equal(t, "[REDACTED]", changed.Emails[0].Value)
			raw, err := env.service.repo.user(t.Context(), env.auth.DirectoryID, parseGUID(user.ID))
			require.NoError(t, err)
			assert.Equal(t, "private@example.test", raw.Email)
			assert.Equal(t, int64(2), raw.Version)
			_, err = env.service.ReplaceUser(t.Context(), env.auth, parseGUID(user.ID), UserInput{Schemas: []string{UserSchema}, UserName: "profile"}, 2)
			assert.ErrorIs(t, err, authz.ErrFieldDenied, "PUT must not clear a denied omitted field")
			for _, operation := range []PatchOperation{
				{Op: "replace", Path: "emails", Value: json.RawMessage(`null`)},
				{Op: "remove", Path: "emails"},
				{Op: "replace", Value: json.RawMessage(`{"emails":null}`)},
				{Op: "replace", Value: json.RawMessage(`{"EMAILS":[]}`)},
			} {
				_, err := env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{operation}}, 2)
				assert.ErrorIs(t, err, authz.ErrFieldDenied)
			}
			var submitted UserInput
			require.NoError(t, json.Unmarshal([]byte(`{"schemas":["`+UserSchema+`"],"userName":"explicit-null","emails":null}`), &submitted))
			_, err = env.service.CreateUser(t.Context(), env.auth, submitted)
			assert.ErrorIs(t, err, authz.ErrFieldDenied, "explicit null must still require write permission")
			bulk, err := env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{
				{Method: "PATCH", Path: "/Users/" + user.ID, Data: json.RawMessage(`{"schemas":["` + PatchSchema + `"],"Operations":[{"op":"remove","path":"emails"}]}`)},
			}})
			require.NoError(t, err)
			require.Len(t, bulk.Operations, 1)
			assert.Equal(t, "403", bulk.Operations[0].Status)
			fields.Fields["email"], fields.Fields["display_name"], fields.Fields["login_name"] = authz.FieldDecision{Read: "omit"}, authz.FieldDecision{Read: "omit"}, authz.FieldDecision{Read: "omit"}
			view, err := env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
			require.NoError(t, err)
			body, err := json.Marshal(view)
			require.NoError(t, err)
			for _, name := range []string{"userName", "name", "displayName", "emails"} {
				assert.NotContains(t, string(body), `"`+name+`"`)
			}
			_, err = env.service.ListUsers(t.Context(), env.auth, ListRequest{StartIndex: 1, Count: 10})
			assert.ErrorIs(t, err, authz.ErrFieldDenied, "fixed login-name sorting also reveals hidden data")
			rawAfter, err := env.service.repo.user(t.Context(), env.auth.DirectoryID, parseGUID(user.ID))
			require.NoError(t, err)
			assert.Equal(t, raw, rawAfter)
			delete(fields.Fields, "id")
			_, err = env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
			assert.ErrorIs(t, err, authz.ErrFieldDenied)
			env.service.fields = nil
			_, err = env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
			assert.ErrorIs(t, err, authz.ErrFieldsUnavailable)
			assert.Equal(t, http.StatusServiceUnavailable, mapSCIMError(t.Context(), err).GetStatus())
		})
	}
}

func TestSCIMUserPatchDisplayAliasesPersistAndSharePermissions(t *testing.T) {
	env := newProvisioningEnvironment(t)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("aliases", "aliases", "aliases@example.test"))
	require.NoError(t, err)
	for _, operation := range []PatchOperation{
		{Op: "replace", Path: UserSchema + ":name.formatted", Value: json.RawMessage(`"First"`)},
		{Op: "replace", Value: json.RawMessage(`{"name":{"formatted":"Second"}}`)},
		{Op: "replace", Path: "displayName", Value: json.RawMessage(`"Third"`)},
	} {
		user, err = env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{operation}}, 0)
		require.NoError(t, err)
		var expected string
		if operation.Path != "" {
			require.NoError(t, json.Unmarshal(operation.Value, &expected))
		} else {
			expected = "Second"
		}
		assert.Equal(t, expected, user.DisplayName)
		require.NotNil(t, user.Name)
		assert.Equal(t, expected, user.Name.Formatted)
	}
	fields, err := env.service.fields.FieldConstraint(t.Context(), env.auth.TenantID, 0, authz.ProfileResource, env.auth.PrincipalID)
	require.NoError(t, err)
	fields.Fields["display_name"] = authz.FieldDecision{Read: "allow"}
	env.service.fields = provisioningProfileChecker{fields: fields.Fields}
	for _, operation := range []PatchOperation{
		{Op: "replace", Path: UserSchema + ":name.formatted", Value: json.RawMessage(`"Denied"`)},
		{Op: "replace", Value: json.RawMessage(`{"name":null}`)},
		{Op: "remove", Path: "displayName"},
	} {
		_, err = env.service.PatchUser(t.Context(), env.auth, parseGUID(user.ID), PatchRequest{Schemas: []string{PatchSchema}, Operations: []PatchOperation{operation}}, 0)
		assert.ErrorIs(t, err, authz.ErrFieldDenied)
	}
}

func TestSCIMUnboundAutomationFailsClosedAndConfigurationRemainsRepairable(t *testing.T) {
	env := newProvisioningEnvironment(t)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("unbound", "unbound", "unbound@example.test"))
	require.NoError(t, err)
	directory, err := env.service.ReplaceDirectory(t.Context(), env.auth.TenantID, env.auth.DirectoryID, "Primary IdP", DirectoryActive, 1, nil)
	require.NoError(t, err)
	_, err = env.service.Authenticate(t.Context(), "Bearer "+env.token)
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, err = env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrUnauthorized)
	_, err = env.service.ReplaceDirectory(t.Context(), env.auth.TenantID, env.auth.DirectoryID, "Primary IdP", DirectoryActive, directory.Version, &env.auth.PrincipalID)
	require.NoError(t, err)
	_, err = env.service.Authenticate(t.Context(), "Bearer "+env.token)
	require.NoError(t, err)
	provider, server := newSCIMProviderEndpoint(t, http.StatusOK, `{"id":"remote"}`)
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Unbound", server.URL, "secret", nil)
	require.NoError(t, err)
	_, err = env.service.PushResource(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.ErrorIs(t, env.service.DeprovisionResource(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID)), ErrUnauthorized)
	_, err = env.service.SyncTarget(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID))
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.Zero(t, provider.count())
}

func TestSCIMOutboundUsesBoundActorFieldsNotInitiatingAdministrator(t *testing.T) {
	env := newProvisioningEnvironment(t)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("outbound", "outbound", "outbound@example.test"))
	require.NoError(t, err)
	provider, server := newSCIMProviderEndpoint(t, http.StatusOK, `{"id":"remote"}`)
	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Bound", server.URL, "secret", &env.auth.PrincipalID)
	require.NoError(t, err)
	fields, err := env.service.fields.FieldConstraint(t.Context(), env.auth.TenantID, 0, authz.ProfileResource, env.auth.PrincipalID)
	require.NoError(t, err)
	fields.Fields["email"] = authz.FieldDecision{Read: "redact"}
	env.service.fields = provisioningProfileCheckerFunc(func(_ context.Context, tenant, entity guid.ID, resource string, actor guid.ID) (authz.FieldConstraint, error) {
		assert.Equal(t, env.auth.TenantID, tenant)
		assert.Zero(t, entity)
		assert.Equal(t, authz.ProfileResource, resource)
		assert.Equal(t, env.auth.PrincipalID, actor)
		return fields, nil
	})
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{TenantID: env.auth.TenantID, PrincipalID: testID("initiating-admin")})
	_, err = env.service.PushResource(ctx, env.auth.TenantID, parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	require.NoError(t, err)
	request := provider.last(t)
	assert.NotContains(t, request.Body, "outbound@example.test")
	assert.Contains(t, request.Body, "[REDACTED]")
	before := provider.count()
	fields.Fields["login_name"] = authz.FieldDecision{Read: "redact"}
	_, err = env.service.PushResource(ctx, env.auth.TenantID, parseGUID(target.Target.ID), ResourceUser, parseGUID(user.ID))
	assert.ErrorIs(t, err, authz.ErrFieldDenied)
	assert.Equal(t, before, provider.count(), "never send an invalid or masked required userName")
}

func TestSCIMFieldGuardsRejectMalformedInputAndFilterLimits(t *testing.T) {
	fields, err := (provisioningProfileChecker{}).FieldConstraint(t.Context(), 1, 0, authz.ProfileResource, 11)
	require.NoError(t, err)
	for _, raw := range []string{"[", strings.Repeat("x", maxFilterBytes+1), strings.Repeat("not (", 18) + "userName pr" + strings.Repeat(")", 18)} {
		assert.ErrorIs(t, requireUserFilterFields(raw, fields), ErrInvalidFilter)
	}
	for _, operation := range []PatchOperation{
		{Op: "replace", Value: json.RawMessage(`null`)},
		{Op: "replace", Value: json.RawMessage(`{`)},
		{Op: "replace", Path: "["},
		{Op: "replace", Path: GroupSchema + ":displayName"},
		{Op: "replace", Path: `emails[value eq "private"]`},
	} {
		assert.ErrorIs(t, requireUserPatchFields(PatchRequest{Operations: []PatchOperation{operation}}, fields), ErrInvalidPath)
	}
	for _, raw := range []string{`null`, `{`, `{"unknown":true}`} {
		var input UserInput
		assert.Error(t, json.Unmarshal([]byte(raw), &input))
	}
}
