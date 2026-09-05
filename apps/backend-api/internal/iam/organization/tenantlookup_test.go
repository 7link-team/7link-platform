package organization

import (
	"strings"
	"testing"
	"time"

	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTenantLookupHarness creates one tenant and hands back the capability under
// test bound to the same database. Every case below differs only in the slug it
// asks for.
func newTenantLookupHarness(t *testing.T) (*organizationHarness, TenantLookup, Tenant) {
	t.Helper()
	h := newOrganizationHarness(t, "sl_")
	service, _, _ := newTenantServiceForTest(h, newOrganizationTestIDs(3100))
	service.now = func() time.Time { return organizationTestNow }
	tenant, err := service.Create(t.Context(), newTenantForTest("acme", "Acme"))
	require.NoError(t, err)

	module := &Module{runtime: h.runtime, ids: newOrganizationTestIDs(3200), appender: h.appender}
	return h, module.tenantLookup(), tenant
}

// TestTenantLookupResolvesTheSlugAnOperatorTyped is the reason the capability
// exists. A tenant with no member has no reachable route, so the slug is the
// only handle a repair command can be given.
func TestTenantLookupResolvesTheSlugAnOperatorTyped(t *testing.T) {
	h, lookup, tenant := newTenantLookupHarness(t)

	for name, slug := range map[string]string{
		"exact":              "acme",
		"typed with spaces":  "  acme  ",
		"typed in uppercase": "ACME",
	} {
		t.Run(name, func(t *testing.T) {
			found, ok, err := lookup(t.Context(), h.db, slug)
			require.NoError(t, err)
			require.True(t, ok)
			assert.Equal(t, tenant.ID, found.ID)
			assert.Equal(t, "acme", found.Slug,
				"the caller reports the slug back to an operator, so it must be the stored one")
			assert.Equal(t, TenantActive, found.Status,
				"the caller refuses a deleted tenant, which it can only do if the status is carried")
		})
	}
}

// TestTenantLookupSeparatesAnAbsentTenantFromARejectedSlug keeps the caller
// able to tell an operator which of the two mistakes they made: a slug that
// names nothing, or one that could never name anything.
func TestTenantLookupSeparatesAnAbsentTenantFromARejectedSlug(t *testing.T) {
	h, lookup, _ := newTenantLookupHarness(t)

	found, ok, err := lookup(t.Context(), h.db, "beta")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, found.ID)

	for name, slug := range map[string]string{
		"empty":         "   ",
		"has a space":   "acme west",
		"too short":     "ab",
		"too long":      strings.Repeat("a", 64),
		"has a newline": "acme\nwest",
	} {
		t.Run(name, func(t *testing.T) {
			_, ok, err := lookup(t.Context(), h.db, slug)
			assert.ErrorIs(t, err, ErrInvalidTenant)
			assert.False(t, ok)
		})
	}
}

// TestTenantLookupRefusesToRunOutsideACallerTransaction pins the capability to
// its contract. Its whole purpose is to let a repair decide and write in one
// commit; resolving the tenant on some other connection would silently give
// that up.
func TestTenantLookupRefusesToRunOutsideACallerTransaction(t *testing.T) {
	_, lookup, _ := newTenantLookupHarness(t)

	_, ok, err := lookup(t.Context(), nil, "acme")
	require.Error(t, err)
	assert.False(t, ok)
}

// TestTenantLookupReportsAHalfWiredModuleRatherThanPanicking covers the gap the
// capability is published across: it is provided before injection runs, so a
// deployment that never completed wiring must get an error inside the caller's
// transaction rather than a panic from the repository constructor.
func TestTenantLookupReportsAHalfWiredModuleRatherThanPanicking(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")

	for name, module := range map[string]*Module{
		"no module":   nil,
		"no runtime":  {ids: newOrganizationTestIDs(1), appender: h.appender},
		"no ids":      {runtime: h.runtime, appender: h.appender},
		"no appender": {runtime: h.runtime, ids: newOrganizationTestIDs(1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, ok, err := module.tenantLookup()(t.Context(), h.db, "acme")
			assert.ErrorIs(t, err, ErrUnavailable)
			assert.False(t, ok)
		})
	}
}

// TestTenantLookupIsPublishedForOtherModules is what makes the capability
// reachable at all. Providing it is the only way a command outside
// organization may ask which tenant a slug means.
func TestTenantLookupIsPublishedForOtherModules(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	services := module.NewServices()
	require.NoError(t, (&Module{runtime: h.runtime}).ProvideServices(services))
	require.NoError(t, services.Seal())

	resolved, err := module.Resolve[TenantLookup](services)
	require.NoError(t, err)
	assert.NotNil(t, resolved)
}
