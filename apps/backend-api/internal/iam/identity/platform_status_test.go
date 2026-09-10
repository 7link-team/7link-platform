package identity

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestPlatformStatusReachesAPrincipalWithNoTenant is the case the route exists
// for: the tenant suspension route is addressed through a membership, and the
// closure route needs a request the person made, so an account in no tenant had
// no way to reach the state erasure requires.
func TestPlatformStatusReachesAPrincipalWithNoTenant(t *testing.T) {
	t.Parallel()
	h := newIdentityHarness(t, "platsus_")
	ctx := t.Context()
	created, err := h.service.Create(ctx, 7, "no-tenant-login", testPassword, "No Tenant", "no-tenant@example.test")
	require.NoError(t, err)
	require.NoError(t, h.db.WithContext(ctx).Table(h.table("iam_tenant_members")).
		Where("principal_id = ?", created.ID).Delete(map[string]any{}).Error)
	h.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return nil }

	suspended, err := h.service.SetPlatformStatus(ctx, created.ID, "disabled")
	require.NoError(t, err)
	assert.Equal(t, "disabled", suspended.Status)

	restored, err := h.service.SetPlatformStatus(ctx, created.ID, "active")
	require.NoError(t, err)
	assert.Equal(t, "active", restored.Status)
}

func TestPlatformStatusRefusesATombstone(t *testing.T) {
	t.Parallel()
	h := newIdentityHarness(t, "platanon_")
	ctx := t.Context()
	created, err := h.service.Create(ctx, 7, "tombstoned-login", testPassword, "Tombstoned", "tombstoned@example.test")
	require.NoError(t, err)
	anonymizedAt := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, h.db.WithContext(ctx).Table(h.table("iam_principal_lifecycles")).
		Create(map[string]any{
			"principal_id": created.ID, "version": 2, "retain_until": anonymizedAt,
			"legal_hold": false, "reference": "acceptance", "anonymized_at": anonymizedAt,
			"updated_at": anonymizedAt,
		}).Error)

	// The row is the last thing that says who this was, and the database refuses
	// the write as well; this refusal names the reason instead of surfacing a
	// constraint failure.
	h.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	_, err = h.service.SetPlatformStatus(ctx, created.ID, "active")
	require.ErrorIs(t, err, ErrPrincipalAnonymized)
}

func TestPlatformStatusPropagatesThePlatformAccessGuard(t *testing.T) {
	t.Parallel()
	h := newIdentityHarness(t, "platguard_")
	ctx := t.Context()
	created, err := h.service.Create(ctx, 7, "guarded-login", testPassword, "Guarded", "guarded@example.test")
	require.NoError(t, err)

	refused := errors.New("protected platform administrator")
	h.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return refused }

	_, err = h.service.SetPlatformStatus(ctx, created.ID, "disabled")
	require.ErrorIs(t, err, refused)
	var status string
	require.NoError(t, h.db.WithContext(ctx).Table(h.table("iam_principals")).
		Select("status").Where("id = ?", created.ID).Scan(&status).Error)
	assert.Equal(t, "active", status, "a refused suspension must not have written the status")
}

func TestPlatformSuspensionRoutesRefuseWithoutPlatformAuthority(t *testing.T) {
	// A tenant administrator holds every tenant capability the module has and
	// still gets nowhere: disabling a principal from the platform is a global
	// decision, the same reason the lifecycle routes are platform-scoped.
	h := newIdentityHTTPHarnessWithPlatform(t, true, false)
	p := h.seedLifecyclePrincipal(t)

	assert.Equal(t, http.StatusForbidden, h.api.Put(lifecyclePath+p.ID.String()+"/suspension", map[string]any{}).Code)
	assert.Equal(t, http.StatusForbidden, h.api.Delete(lifecyclePath+p.ID.String()+"/suspension").Code)

	status, err := h.service.PrincipalLifecycleStatus(t.Context(), p.ID)
	require.NoError(t, err)
	assert.False(t, status.Suspended, "a refused call must not have suspended anything")
}

func TestPlatformSuspensionRouteSuspendsAndRestores(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	h.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	p := h.seedLifecyclePrincipal(t)

	response := h.api.Put(lifecyclePath+p.ID.String()+"/suspension", map[string]any{})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"status":"disabled"`)
	status, err := h.service.PrincipalLifecycleStatus(t.Context(), p.ID)
	require.NoError(t, err)
	assert.True(t, status.Suspended)

	response = h.api.Delete(lifecyclePath + p.ID.String() + "/suspension")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), `"status":"active"`)
	status, err = h.service.PrincipalLifecycleStatus(t.Context(), p.ID)
	require.NoError(t, err)
	assert.False(t, status.Suspended)
}

func TestPlatformSuspensionRouteReportsAnUnknownPrincipal(t *testing.T) {
	h := newIdentityHTTPHarnessWithPlatform(t, true, true)
	h.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	assert.Equal(t, http.StatusNotFound, h.api.Put(lifecyclePath+"424242/suspension", map[string]any{}).Code)
}

func TestPlatformStatusRejectsAnUnknownStatus(t *testing.T) {
	t.Parallel()
	h := newIdentityHarness(t, "platarg_")
	ctx := t.Context()
	created, err := h.service.Create(ctx, 7, "status-arg-login", testPassword, "Status Arg", "status-arg@example.test")
	require.NoError(t, err)
	_, err = h.service.SetPlatformStatus(ctx, created.ID, "deleted")
	require.ErrorIs(t, err, ErrInvalid)
}
