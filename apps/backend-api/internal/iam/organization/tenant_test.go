package organization

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantNormalization(t *testing.T) {
	t.Parallel()
	input, err := normalizeTenantCreate(CreateTenant{Slug: " Acme-West ", Name: " Acme West ", OwnerPrincipalID: organizationTestOwnerID})
	require.NoError(t, err)
	assert.Equal(t, "acme-west", input.Slug)
	assert.Equal(t, "Acme West", input.Name)
	for _, input := range []CreateTenant{
		{}, {Slug: "Bad Slug", Name: "Name"}, {Slug: "ab", Name: "Name"},
		{Slug: strings.Repeat("a", 64), Name: "Name"}, {Slug: "valid", Name: "Name"}, {Slug: "valid", Name: "bad\nname"},
	} {
		_, err := normalizeTenantCreate(input)
		assert.ErrorIs(t, err, ErrInvalidTenant)
	}
	name, status := " Updated ", TenantSuspended
	_, update, err := normalizeTenantUpdate(1, UpdateTenant{Name: &name, Status: &status, Version: 1})
	require.NoError(t, err)
	assert.Equal(t, "Updated", *update.Name)
	invalidName, invalidStatus := "bad\nname", TenantDeleted
	for _, input := range []UpdateTenant{
		{Version: 1}, {Name: &name}, {Name: &invalidName, Version: 1},
		{Status: &invalidStatus, Version: 1},
	} {
		_, _, err := normalizeTenantUpdate(1, input)
		assert.ErrorIs(t, err, ErrInvalidTenant)
	}
	assert.Equal(t, "123", tenantSlugForID("123"))
	assert.Equal(t, "tenant-ab", tenantSlugForID("ab"))
	assert.True(t, validTenantSlug(tenantSlugForID("invalid tenant id")))
	assert.True(t, validTenantSlug(tenantSlugForID("!!!")))
	assert.LessOrEqual(t, len(tenantSlugForID(strings.Repeat("Long Tenant ", 20))), 63)
}

func TestTenantLifecycleMemberListingAndRollback(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service, _, _ := newTenantServiceForTest(h, newOrganizationTestIDs(500))
	service.now = func() time.Time { return organizationTestNow }
	tenant, err := service.Create(t.Context(), newTenantForTest("acme", "Acme"))
	require.NoError(t, err)
	assert.Equal(t, TenantActive, tenant.Status)
	_, err = service.Create(t.Context(), newTenantForTest("acme", "Duplicate"))
	assert.ErrorIs(t, err, ErrTenantSlugConflict)
	second, err := service.Create(t.Context(), newTenantForTest("beta", "Beta"))
	require.NoError(t, err)
	items, err := service.List(t.Context(), false)
	require.NoError(t, err)
	require.Len(t, items, 2)

	h.seedMember(t, tenant.ID, 42, "Alice")
	memberTenants, err := service.ListByMember(t.Context(), 42)
	require.NoError(t, err)
	require.Len(t, memberTenants, 1)
	assert.Equal(t, tenant.ID, memberTenants[0].ID)
	_, err = service.ListByMember(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidTenant)

	name, status := "Acme Holdings", TenantSuspended
	tenant, err = service.Update(t.Context(), tenant.ID, UpdateTenant{Name: &name, Status: &status, Version: tenant.Version})
	require.NoError(t, err)
	assert.EqualValues(t, 2, tenant.Version)
	revision := h.revision(t, tenant.ID)
	tenant, err = service.Update(t.Context(), tenant.ID, UpdateTenant{Name: &name, Version: tenant.Version})
	require.NoError(t, err)
	assert.Equal(t, revision, h.revision(t, tenant.ID))
	_, err = service.Update(t.Context(), tenant.ID, UpdateTenant{Name: &name, Version: 1})
	assert.ErrorIs(t, err, ErrTenantVersionConflict)
	require.NoError(t, service.Delete(t.Context(), tenant.ID, tenant.Version))
	items, err = service.List(t.Context(), false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, second.ID, items[0].ID)
	deleted, err := service.Get(t.Context(), tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, TenantDeleted, deleted.Status)

	h.auditErr = assert.AnError
	_, err = service.Create(t.Context(), newTenantForTest("rollback", "Rollback"))
	assert.ErrorIs(t, err, assert.AnError)
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_tenants")).Where("slug = ?", "rollback").Count(&count).Error)
	assert.Zero(t, count)
}

func TestEnsureTenantIsIdempotent(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	require.NoError(t, EnsureTenant(t.Context(), h.runtime, 99))
	require.NoError(t, EnsureTenant(t.Context(), h.runtime, 99))
	var count int64
	require.NoError(t, h.db.Table(h.table("iam_tenants")).Where("id = ?", 99).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	assert.ErrorIs(t, EnsureTenant(t.Context(), h.runtime, 0), ErrInvalidTenant)
}

func TestTenantMutationFailuresRollbackAndDeletedRowsRemainQueryable(t *testing.T) {
	h := newOrganizationHarness(t, "")
	service, _, _ := newTenantServiceForTest(h, newOrganizationTestIDs(1600))
	service.now = func() time.Time { return organizationTestNow }
	tenant, err := service.Create(t.Context(), newTenantForTest("rollback", "Original"))
	require.NoError(t, err)

	h.auditErr = assert.AnError
	updatedName := "Updated"
	_, err = service.Update(t.Context(), tenant.ID, UpdateTenant{Name: &updatedName, Version: tenant.Version})
	assert.ErrorIs(t, err, assert.AnError)
	stored, err := service.Get(t.Context(), tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "Original", stored.Name)
	assert.Equal(t, tenant.Version, stored.Version)
	assert.EqualValues(t, 1, h.revision(t, tenant.ID))

	err = service.Delete(t.Context(), tenant.ID, tenant.Version)
	assert.ErrorIs(t, err, assert.AnError)
	stored, err = service.Get(t.Context(), tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, TenantActive, stored.Status)
	h.auditErr = nil
	require.NoError(t, service.Delete(t.Context(), tenant.ID, tenant.Version))

	items, err := service.List(t.Context(), true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, TenantDeleted, items[0].Status)
	_, err = service.Update(t.Context(), tenant.ID, UpdateTenant{Name: &updatedName, Version: tenant.Version + 1})
	assert.ErrorIs(t, err, ErrTenantVersionConflict)
	assert.ErrorIs(t, service.Delete(t.Context(), tenant.ID, tenant.Version+1), ErrTenantVersionConflict)

	_, err = service.Get(t.Context(), 0)
	assert.ErrorIs(t, err, ErrInvalidTenant)
	_, err = service.Get(t.Context(), 9999)
	assert.ErrorIs(t, err, ErrTenantNotFound)
	_, err = service.Update(t.Context(), 9999, UpdateTenant{Name: &updatedName, Version: 1})
	assert.ErrorIs(t, err, ErrTenantNotFound)
	assert.ErrorIs(t, service.Delete(t.Context(), 9999, 1), ErrTenantNotFound)
	_, err = getTenantRow(t.Context(), nil, 1)
	assert.ErrorIs(t, err, ErrInvalidTenant)
}
