package organization

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// organizationTestOwnerID is the principal every tenant test hands its new
// tenant. Owning a tenant is now part of creating one, so the tests need a
// concrete owner rather than an implied absent one.
const organizationTestOwnerID guid.ID = 7001

// tenantBootstrapStub stands in for the capability core publishes.
// Organization must never write core's membership, role, or menu tables
// itself, so its own tests assert what the bootstrap was asked to do rather
// than reaching into those tables.
type tenantBootstrapStub struct {
	inputs    []core.TenantBootstrapInput
	result    core.TenantBootstrapResult
	err       error
	sawNilTx  bool
	unclaimed bool
}

func newTenantBootstrapStub() *tenantBootstrapStub {
	return &tenantBootstrapStub{
		result:    core.TenantBootstrapResult{AdministratorRoleID: 9001, PermissionCount: 1, MenuCount: 15},
		unclaimed: true,
	}
}

func (s *tenantBootstrapStub) capability() core.TenantBootstrapper {
	return core.TenantBootstrapper{
		Bootstrap: func(
			_ context.Context, db *gorm.DB, input core.TenantBootstrapInput, _ time.Time,
		) (core.TenantBootstrapResult, error) {
			if db == nil {
				s.sawNilTx = true
			}
			s.inputs = append(s.inputs, input)
			if s.err != nil {
				return core.TenantBootstrapResult{}, s.err
			}
			return s.result, nil
		},
		IsUnclaimed: func(context.Context, *gorm.DB, guid.ID) (bool, error) {
			return s.unclaimed, nil
		},
	}
}

// tenantOwnerStub is the identity half of the same boundary: organization asks
// who the owner is, it does not read principals itself.
type tenantOwnerStub struct {
	principals map[guid.ID]identity.Principal
	err        error
}

func newTenantOwnerStub() *tenantOwnerStub {
	return &tenantOwnerStub{principals: map[guid.ID]identity.Principal{
		organizationTestOwnerID: {
			ID: organizationTestOwnerID, LoginName: "owner",
			DisplayName: "Tenant Owner", Email: "owner@example.test", Status: "active",
		},
	}}
}

func (s *tenantOwnerStub) capability() identity.TenantOwnerLookup {
	return func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		if s.err != nil {
			return identity.Principal{}, false, s.err
		}
		principal, found := s.principals[id]
		return principal, found, nil
	}
}

func newTenantServiceForTest(
	h *organizationHarness, ids guid.Source,
) (*TenantService, *tenantBootstrapStub, *tenantOwnerStub) {
	bootstrap, owners := newTenantBootstrapStub(), newTenantOwnerStub()
	service := NewTenantService(h.runtime, h.appender, ids, bootstrap.capability(), owners.capability())
	return service, bootstrap, owners
}

func newTenantForTest(slug, name string) CreateTenant {
	return CreateTenant{Slug: slug, Name: name, OwnerPrincipalID: organizationTestOwnerID}
}

func TestCreateTenantBootstrapsItsFirstAdministrator(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service, bootstrap, _ := newTenantServiceForTest(h, newOrganizationTestIDs(2400))
	service.now = func() time.Time { return organizationTestNow }

	tenant, err := service.Create(t.Context(), newTenantForTest("acme", "Acme"))
	require.NoError(t, err)

	require.Len(t, bootstrap.inputs, 1, "a tenant is bootstrapped exactly once")
	assert.False(t, bootstrap.sawNilTx, "the bootstrap must run on the caller's transaction")
	assert.Equal(t, tenant.ID, bootstrap.inputs[0].TenantID)
	assert.Equal(t, organizationTestOwnerID, bootstrap.inputs[0].OwnerID)
	assert.Equal(t, "Tenant Owner", bootstrap.inputs[0].DisplayName,
		"the membership row carries the profile identity reported, not one organization invented")
	assert.Equal(t, "owner@example.test", bootstrap.inputs[0].Email)

	h.auditMu.Lock()
	events := append([]audit.DomainEvent(nil), h.audits...)
	h.auditMu.Unlock()
	var created, bootstrapped int
	for _, event := range events {
		switch event.EventType {
		case "tenant_created":
			created++
		case "tenant_bootstrapped":
			bootstrapped++
			assert.Equal(t, 15, event.Detail["menu_count"])
			assert.Equal(t, 1, event.Detail["permission_count"])
		}
	}
	assert.Equal(t, 1, created)
	assert.Equal(t, 1, bootstrapped, "the counts are reported, not asserted from memory")
}

func TestCreateTenantRefusesAnOwnerItCannotHandTheTenantTo(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service, bootstrap, owners := newTenantServiceForTest(h, newOrganizationTestIDs(2500))
	service.now = func() time.Time { return organizationTestNow }

	_, err := service.Create(t.Context(), CreateTenant{Slug: "noowner", Name: "No Owner"})
	assert.ErrorIs(t, err, ErrInvalidTenant, "an owner is required, not defaulted")

	_, err = service.Create(t.Context(), CreateTenant{
		Slug: "missing", Name: "Missing", OwnerPrincipalID: 8888,
	})
	assert.ErrorIs(t, err, ErrTenantOwnerNotFound)

	disabled := owners.principals[organizationTestOwnerID]
	disabled.Status = "disabled"
	owners.principals[organizationTestOwnerID] = disabled
	_, err = service.Create(t.Context(), newTenantForTest("disabled", "Disabled"))
	assert.ErrorIs(t, err, ErrTenantOwnerInactive)

	assert.Empty(t, bootstrap.inputs, "nothing is bootstrapped for a rejected owner")
	remaining, err := service.List(t.Context(), true)
	require.NoError(t, err)
	assert.Empty(t, remaining, "a refused owner rolls the tenant back with it")
}

func TestCreateTenantRollsBackWhenTheBootstrapFails(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service, bootstrap, _ := newTenantServiceForTest(h, newOrganizationTestIDs(2600))
	service.now = func() time.Time { return organizationTestNow }
	bootstrap.err = errors.New("menu insert failed")

	_, err := service.Create(t.Context(), newTenantForTest("halfway", "Halfway"))
	require.Error(t, err)

	remaining, err := service.List(t.Context(), true)
	require.NoError(t, err)
	assert.Empty(t, remaining, "no tenant may survive a failed bootstrap")
}

func TestTenantServiceRefusesToStartWithoutItsBootstrapCapabilities(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	ids := newOrganizationTestIDs(2700)
	owners := newTenantOwnerStub().capability()
	assert.Panics(t, func() {
		NewTenantService(h.runtime, h.appender, ids, core.TenantBootstrapper{}, owners)
	}, "a service that cannot bootstrap could only create unreachable tenants")
	assert.Panics(t, func() {
		NewTenantService(h.runtime, h.appender, ids, newTenantBootstrapStub().capability(), nil)
	})
}

// TestCreateTenantSurfacesAnOwnerLookupFailure keeps a broken identity module
// from reading as "this owner does not exist", which would let the tenant be
// refused for the wrong reason and reported to the caller as a 422.
func TestCreateTenantSurfacesAnOwnerLookupFailure(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service, bootstrap, owners := newTenantServiceForTest(h, newOrganizationTestIDs(7100))
	owners.err = assert.AnError

	_, err := service.Create(t.Context(), newTenantForTest("broken", "Broken"))
	require.ErrorIs(t, err, assert.AnError)
	assert.NotErrorIs(t, err, ErrTenantOwnerNotFound, "a failed lookup is not an absent owner")

	assert.Empty(t, bootstrap.inputs)
	remaining, err := service.List(t.Context(), true)
	require.NoError(t, err)
	assert.Empty(t, remaining, "the tenant rolls back with the lookup that failed")
}

// TestTenantErrorMapsOwnerRefusalsToUnprocessable keeps the two refusals a
// caller can actually fix from being reported as a server fault.
func TestTenantErrorMapsOwnerRefusalsToUnprocessable(t *testing.T) {
	for _, err := range []error{ErrTenantOwnerNotFound, ErrTenantOwnerInactive} {
		var status huma.StatusError
		require.ErrorAs(t, tenantError(fmt.Errorf("create tenant: %w", err)), &status)
		assert.Equal(t, http.StatusUnprocessableEntity, status.GetStatus(),
			"an owner the caller can correct is the caller's to correct")
	}
}
