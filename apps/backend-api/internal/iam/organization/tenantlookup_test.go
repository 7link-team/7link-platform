package organization

import (
	"context"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"

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
	service, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(3100))
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

func TestTenantEnumeratorPagesOnlyLiveTenantRoots(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	for _, row := range []struct {
		id           guid.ID
		kind, status string
	}{
		{10010, ScopeTenant, TenantActive}, {10020, ScopeTenant, TenantSuspended},
		{10030, ScopePlatform, TenantActive}, {10040, ScopeSystem, TenantActive},
		{10050, ScopeTenant, TenantDeleted}, {10060, ScopeTenant, TenantActive},
	} {
		require.NoError(t, h.db.Exec("INSERT INTO sl_iam_tenants (id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES (?,?,?,?,?,1,?,?)", row.id, row.kind, "tenant-"+row.id.String(), row.id.String(), row.status, organizationTestNow, organizationTestNow).Error)
	}
	m := &Module{runtime: h.runtime, ids: newOrganizationTestIDs(50000), appender: h.appender}
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	enumerate, err := module.Resolve[TenantEnumerator](services)
	require.NoError(t, err)
	ids, err := enumerate(t.Context(), 10000, 1)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{10010}, ids)
	ids, err = enumerate(t.Context(), ids[0], 2)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{10020, 10060}, ids)
	ids, err = enumerate(t.Context(), 10060, 100)
	require.NoError(t, err)
	require.Empty(t, ids)
	for _, bounds := range []struct {
		after guid.ID
		limit int
	}{{-1, 1}, {0, 0}, {0, 101}} {
		_, err = enumerate(t.Context(), bounds.after, bounds.limit)
		require.Error(t, err)
	}
	require.NoError(t, h.db.Exec("DROP TABLE sl_iam_tenants").Error)
	_, err = enumerate(t.Context(), 10000, 1)
	require.Error(t, err)
}

func TestTenantEnumeratorRequiresWiredModule(t *testing.T) {
	for _, owner := range []*Module{nil, {}} {
		_, err := owner.tenantEnumerator()(t.Context(), 0, 100)
		require.ErrorIs(t, err, ErrUnavailable)
	}
}

func TestActiveTenantEnumeratorUsesBoundedPagesAndExcludesInactiveRoots(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < 505; i++ {
			id := guid.ID(20000 + i)
			kind, status := ScopeTenant, TenantActive
			switch i {
			case 501:
				kind = ScopePlatform
			case 502:
				kind = ScopeSystem
			case 503:
				status = TenantDeleted
			case 504:
				status = TenantSuspended
			}
			if err := tx.Exec("INSERT INTO sl_iam_tenants (id,scope_kind,slug,name,status,version,created_at,updated_at) VALUES (?,?,?,?,?,1,?,?)", id, kind, "tenant-"+id.String(), id.String(), status, organizationTestNow, organizationTestNow).Error; err != nil {
				return err
			}
		}
		return nil
	}))
	owner := &Module{runtime: h.runtime, ids: newOrganizationTestIDs(50000), appender: h.appender}
	services := module.NewServices()
	require.NoError(t, owner.ProvideServices(services))
	require.NoError(t, services.Seal())
	enumerate, err := module.Resolve[ActiveTenantEnumerator](services)
	require.NoError(t, err)
	first, err := enumerate(t.Context(), 19999, 500)
	require.NoError(t, err)
	require.Len(t, first, 500)
	for i, id := range first {
		require.Equal(t, guid.ID(20000+i), id)
	}
	second, err := enumerate(t.Context(), first[499], 500)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{20500}, second)
	last, err := enumerate(t.Context(), second[0], 500)
	require.NoError(t, err)
	require.Empty(t, last)
	require.NoError(t, h.db.Exec("UPDATE sl_iam_tenants SET status = ? WHERE id = ?", TenantSuspended, 20500).Error)
	last, err = enumerate(t.Context(), first[499], 500)
	require.NoError(t, err)
	require.Empty(t, last)
	// Governance still intentionally includes suspended roots.
	governance, err := owner.tenantEnumerator()(t.Context(), first[499], 100)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{20500, 20504}, governance)
	for _, bounds := range []struct {
		after guid.ID
		limit int
	}{{-1, 1}, {0, 0}, {0, 501}} {
		_, err := enumerate(t.Context(), bounds.after, bounds.limit)
		require.Error(t, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = enumerate(canceled, 0, 500)
	require.ErrorIs(t, err, context.Canceled)
	for _, unwired := range []*Module{nil, {}} {
		_, err = unwired.activeTenantEnumerator()(t.Context(), 0, 500)
		require.ErrorIs(t, err, ErrUnavailable)
	}
}
