package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type identityProfileChecker struct{ constraint authz.FieldConstraint }

func (c identityProfileChecker) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	return c.constraint, nil
}

func TestIdentityProfilePolicyHTTPFailuresAndProjection(t *testing.T) {
	h := newIdentityHTTPHarness(t, true)
	header, auth := identityTenantPath(7), "Authorization: Bearer test"
	created := h.api.Post(header+"/principals", auth, map[string]any{
		"login_name": "profile-user", "password": testPassword, "display_name": "Private", "email": "private@example.test",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	principal := decodeIdentityEnvelope[Principal](t, created).Data
	path := "/principals/" + principal.ID.String()
	fields, err := (identityRouteAuthorizer{}).FieldConstraint(t.Context(), 7, 0, authz.ProfileResource, 11)
	require.NoError(t, err)
	fields.Fields["email"] = authz.FieldDecision{Read: "redact"}
	fields.Fields["display_name"] = authz.FieldDecision{Read: "omit"}
	h.service.fields = identityProfileChecker{fields}
	view := h.api.Get(header+path, auth)
	require.Equal(t, http.StatusOK, view.Code)
	body := decodeIdentityEnvelope[map[string]any](t, view).Data
	assert.Equal(t, "[REDACTED]", body["email"])
	assert.NotContains(t, body, "email_verified")
	assert.NotContains(t, body, "display_name")
	assert.Equal(t, http.StatusForbidden, h.api.Get(header+"/principals?search=private", auth).Code)
	for _, value := range []any{nil, "other@example.test"} {
		assert.Equal(t, http.StatusForbidden, h.api.Patch(header+path, auth, map[string]any{"email": value}).Code)
		assert.Equal(t, http.StatusForbidden, h.api.Post(header+"/principals", auth, map[string]any{
			"login_name": "denied", "password": testPassword, "email": value,
		}).Code)
	}
	assert.Equal(t, "private@example.test", h.principal(t, principal.ID).Email)
	fields.Fields["login_name"] = authz.FieldDecision{Read: "omit"}
	assert.Equal(t, http.StatusForbidden, h.api.Get(header+"/principals", auth).Code)
	for _, unavailable := range []bool{false, true} {
		status := http.StatusForbidden
		h.service.fields = identityProfileChecker{}
		if unavailable {
			h.service.fields = nil
			status = http.StatusServiceUnavailable
		}
		assert.Equal(t, status, h.api.Get(header+path, auth).Code)
		assert.Equal(t, status, h.api.Get(header+"/principals", auth).Code)
		assert.Equal(t, status, h.api.Put(header+path+"/suspension", auth).Code)
		assert.Equal(t, status, h.api.Delete(header+path+"/suspension", auth).Code)
		assert.Equal(t, status, h.api.Patch(header+path, auth, map[string]any{"display_name": "Changed"}).Code)
		assert.Equal(t, status, h.api.Post(header+"/principals", auth, map[string]any{
			"login_name": "unavailable", "password": testPassword,
		}).Code)
		assert.Equal(t, "active", h.principal(t, principal.ID).Status)
		assert.Equal(t, "Private", h.principal(t, principal.ID).DisplayName)
	}
}

func TestIdentityProfileRequiresTenantContextAndValidSubmittedFields(t *testing.T) {
	s := &Service{}
	_, err := s.profileConstraint(t.Context(), 7)
	assert.ErrorIs(t, err, authz.ErrFieldDenied)
	ctx := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 8, PrincipalID: 11})
	_, err = s.profileConstraint(ctx, 7)
	assert.ErrorIs(t, err, authz.ErrFieldDenied)
	assert.ErrorIs(t, requireProfileWrites([]byte("{"), authz.FieldConstraint{}, false), ErrInvalid)
	for _, name := range []string{"login_name", "display_name", "email"} {
		body, err := json.Marshal(map[string]any{name: nil})
		require.NoError(t, err)
		assert.ErrorIs(t, requireProfileWrites(body, authz.FieldConstraint{}, true), authz.ErrFieldDenied)
	}
}
