package identity

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const lifecyclePath = "/platform/iam/principals/"

// TestPrincipalLifecycleRoutesRefuseWithoutPlatformAuthority is the property the
// audit asked to be made structural: erasing a principal is a global decision,
// so no tenant permission may reach it. A tenant administrator here holds every
// tenant capability the module has and still gets nowhere.
func TestPrincipalLifecycleRoutesRefuseWithoutPlatformAuthority(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, false)
	p := h.seedLifecyclePrincipal(t)

	assert.Equal(t, http.StatusForbidden,
		h.api.Get(lifecyclePath+p.ID.String()+"/lifecycle").Code)
	assert.Equal(t, http.StatusForbidden,
		h.api.Put(lifecyclePath+p.ID.String()+"/lifecycle/retention",
			map[string]any{"version": 0, "retain_until": time.Now().UTC().Add(time.Hour), "legal_hold": false, "reference": "case-1"}).Code)
	assert.Equal(t, http.StatusForbidden,
		h.api.Post(lifecyclePath+p.ID.String()+"/lifecycle/anonymization",
			map[string]any{"version": 1, "confirmation": p.LoginName}).Code)

	status, err := h.service.PrincipalLifecycleStatus(t.Context(), p.ID)
	require.NoError(t, err)
	assert.Zero(t, status.Lifecycle.Version, "a refused call must not have recorded anything")
	assert.False(t, status.Anonymized)
}

func TestPrincipalLifecycleRouteReportsBlockersAndTombstone(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	p := h.seedLifecyclePrincipal(t)

	response := h.api.Get(lifecyclePath + p.ID.String() + "/lifecycle")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), LifecycleBlockerNotSuspended)
	assert.Contains(t, response.Body.String(), LifecycleBlockerNoRetention)

	require.Equal(t, http.StatusOK, h.api.Put(lifecyclePath+p.ID.String()+"/lifecycle/retention",
		map[string]any{
			"version": 0, "retain_until": time.Now().UTC().Add(-time.Hour),
			"legal_hold": false, "reference": "case-1",
		}).Code)
	_, err := h.service.SetStatus(t.Context(), 7, p.ID, "disabled")
	require.NoError(t, err)

	response = h.api.Get(lifecyclePath + p.ID.String() + "/lifecycle")
	require.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), LifecycleBlockerNotSuspended)
	assert.NotContains(t, response.Body.String(), LifecycleBlockerNoRetention)

	require.Equal(t, http.StatusOK, h.api.Post(lifecyclePath+p.ID.String()+"/lifecycle/anonymization",
		map[string]any{"version": 1, "confirmation": p.LoginName}).Code)

	response = h.api.Get(lifecyclePath + p.ID.String() + "/lifecycle")
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), LifecycleBlockerAnonymized,
		"a tombstone reports itself rather than looking like an ordinary record")
}

// TestPrincipalLifecycleRoutesRefuseAStaleDecision covers two administrators
// working from the same screen. The second one's version is the one they read,
// not the one that is current, and neither retention nor erasure may quietly
// apply to a policy that moved underneath them.
func TestPrincipalLifecycleRoutesRefuseAStaleDecision(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	p := h.seedLifecyclePrincipal(t)

	require.Equal(t, http.StatusOK, h.api.Put(lifecyclePath+p.ID.String()+"/lifecycle/retention",
		map[string]any{
			"version": 0, "retain_until": time.Now().UTC().Add(-time.Hour),
			"legal_hold": false, "reference": "case-1",
		}).Code)

	assert.Equal(t, http.StatusConflict, h.api.Put(lifecyclePath+p.ID.String()+"/lifecycle/retention",
		map[string]any{
			"version": 0, "retain_until": time.Now().UTC().Add(time.Hour),
			"legal_hold": true, "reference": "case-2",
		}).Code, "the version they read is no longer the current one")

	_, err := h.service.SetStatus(t.Context(), 7, p.ID, "disabled")
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, h.api.Post(lifecyclePath+p.ID.String()+"/lifecycle/anonymization",
		map[string]any{"version": 99, "confirmation": p.LoginName}).Code)

	assert.Equal(t, http.StatusUnprocessableEntity, h.api.Post(lifecyclePath+p.ID.String()+"/lifecycle/anonymization",
		map[string]any{"version": 1, "confirmation": "not-this-principal"}).Code,
		"naming the wrong subject is a mistake in the request, not a race")

	status, err := h.service.PrincipalLifecycleStatus(t.Context(), p.ID)
	require.NoError(t, err)
	assert.False(t, status.Anonymized, "none of the refusals may have erased anything")
}

func TestPrincipalLifecycleRouteRefusesErasureWhileRetentionStands(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	p := h.seedLifecyclePrincipal(t)
	require.Equal(t, http.StatusOK, h.api.Put(lifecyclePath+p.ID.String()+"/lifecycle/retention",
		map[string]any{
			"version": 0, "retain_until": time.Now().UTC().Add(72 * time.Hour),
			"legal_hold": true, "reference": "case-1",
		}).Code)
	_, err := h.service.SetStatus(t.Context(), 7, p.ID, "disabled")
	require.NoError(t, err)

	assert.Equal(t, http.StatusConflict, h.api.Post(lifecyclePath+p.ID.String()+"/lifecycle/anonymization",
		map[string]any{"version": 1, "confirmation": p.LoginName}).Code,
		"a deadline in the future and a hold both have to be cleared first")
}

func (h identityHTTPHarness) seedLifecyclePrincipal(t *testing.T) Principal {
	t.Helper()
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error { return nil }
	p, err := h.service.Create(t.Context(), 7, "subject", testPassword, "Subject Name", "subject@example.test")
	require.NoError(t, err)
	return p
}
