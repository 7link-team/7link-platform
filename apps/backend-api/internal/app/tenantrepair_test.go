package app

import (
	"context"
	"errors"
	"testing"
	"time"

	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// orphanTenantID is the tenant every case below repairs. A fixed value keeps
// the assertions readable and matches nothing the ID generator would produce.
const orphanTenantID guid.ID = 90210

// newTenantRepairConfig reuses the platform-administrator configuration so the
// repair is exercised against the same deployment shape. Only the request half
// differs, which is the point: the repair is not a second application.
func newTenantRepairConfig(t *testing.T, dsn string) TenantRepairConfig {
	t.Helper()
	admin := newPlatformAdminConfig(t, dsn)
	return TenantRepairConfig{
		AppConfig: admin.AppConfig,
		TenantRepairRequest: TenantRepairRequest{
			TenantSlug: "acme", OwnerLogin: admin.Login,
		},
	}
}

// seedOrphanTenant writes the tenant this command exists for: a row inserted
// before tenant creation and owner bootstrap became one transaction, holding no
// member and no role. No service can create one any more, so a direct insert is
// the only honest way to reproduce it.
func seedOrphanTenant(t *testing.T, config TenantRepairConfig, slug, status string) {
	t.Helper()
	db, err := (gormx.Datasource{
		Type: "sqlite", DSN: config.Database["primary"].Dsn, Writable: true,
	}).Open(t.Context())
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	now := time.Now().UTC()
	require.NoError(t, db.Exec(
		"INSERT INTO iam_tenants (id, slug, name, status, version, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, 1, ?, ?)",
		orphanTenantID, slug, "Acme", status, now, now,
	).Error)
}

// TestTenantOwnerRepairMakesAnUnreachableTenantAdministrable is the end-to-end
// contract: a tenant nobody inside the product can reach becomes one an
// operator can sign in to and administer.
func TestTenantOwnerRepairMakesAnUnreachableTenantAdministrable(t *testing.T) {
	dsn := newPlatformAdminDSN(t)
	config := newTenantRepairConfig(t, dsn)
	migratePlatformAdminDatabase(t, PlatformAdminConfig{AppConfig: config.AppConfig})
	owner, err := (&App{}).RunPlatformAdminBootstrap(
		t.Context(), newPlatformAdminConfig(t, dsn), bootstrapTestPassword)
	require.NoError(t, err)
	seedOrphanTenant(t, config, "acme", organization.TenantActive)

	report, err := (&App{}).RunTenantOwnerRepair(t.Context(), config)
	require.NoError(t, err)
	assert.Equal(t, TenantOwnerRepaired, report.Outcome)
	assert.Equal(t, orphanTenantID, report.TenantID)
	assert.Equal(t, "acme", report.TenantSlug)
	assert.Equal(t, owner.PrincipalID, report.OwnerPrincipalID)
	assert.Equal(t, "admin", report.OwnerLogin)
	assert.NotZero(t, report.AdministratorRoleID,
		"the report must name the role an operator can now inspect")
	assert.Positive(t, report.PermissionCount)
	assert.Positive(t, report.MenuCount)

	// Running it again is what an operator does when they are unsure the first
	// run took. The tenant now has a member, so the second run must refuse.
	_, err = (&App{}).RunTenantOwnerRepair(t.Context(), config)
	assert.ErrorIs(t, err, ErrTenantRepairConflict)
}

// TestTenantOwnerRepairReportsEachMistypedArgumentDistinctly keeps an operator
// correcting the argument they got wrong instead of investigating a permission
// problem that does not exist.
func TestTenantOwnerRepairReportsEachMistypedArgumentDistinctly(t *testing.T) {
	dsn := newPlatformAdminDSN(t)
	base := newTenantRepairConfig(t, dsn)
	migratePlatformAdminDatabase(t, PlatformAdminConfig{AppConfig: base.AppConfig})
	_, err := (&App{}).RunPlatformAdminBootstrap(
		t.Context(), newPlatformAdminConfig(t, dsn), bootstrapTestPassword)
	require.NoError(t, err)
	seedOrphanTenant(t, base, "acme", organization.TenantActive)

	t.Run("unknown tenant", func(t *testing.T) {
		config := base
		config.TenantSlug = "beta"
		_, err := (&App{}).RunTenantOwnerRepair(t.Context(), config)
		assert.ErrorIs(t, err, ErrTenantRepairNotFound)
	})

	t.Run("unknown login", func(t *testing.T) {
		config := base
		config.OwnerLogin = "nobody"
		_, err := (&App{}).RunTenantOwnerRepair(t.Context(), config)
		assert.ErrorIs(t, err, ErrTenantRepairNotFound)
	})
}

// TestTenantOwnerRepairValidatesBeforeTouchingTheDatabase proves an operator
// typo costs nothing: no connection, no lock, no half-open transaction.
func TestTenantOwnerRepairValidatesBeforeTouchingTheDatabase(t *testing.T) {
	base := newTenantRepairConfig(t, newPlatformAdminDSN(t))

	for name, mutate := range map[string]func(*TenantRepairConfig){
		"blank slug":  func(config *TenantRepairConfig) { config.TenantSlug = "   " },
		"blank login": func(config *TenantRepairConfig) { config.OwnerLogin = "   " },
	} {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			_, err := (&App{}).RunTenantOwnerRepair(t.Context(), config)
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrPlatformAdminSchemaNotReady,
				"the argument is rejected before any schema check")
		})
	}
}

// TestTenantOwnerRepairReportsAnUnmigratedSchema keeps the repair from blaming
// the tenant for a database that was never brought up to date.
func TestTenantOwnerRepairReportsAnUnmigratedSchema(t *testing.T) {
	_, err := (&App{}).RunTenantOwnerRepair(
		t.Context(), newTenantRepairConfig(t, newPlatformAdminDSN(t)))
	assert.ErrorIs(t, err, ErrPlatformAdminSchemaNotReady)
}

// TestTenantOwnerRepairReportsUnavailableWithoutIAM proves a deployment that
// never enabled IAM gets its own outcome rather than a resolution panic.
func TestTenantOwnerRepairReportsUnavailableWithoutIAM(t *testing.T) {
	config := newTenantRepairConfig(t, newPlatformAdminDSN(t))
	migratePlatformAdminDatabase(t, PlatformAdminConfig{AppConfig: config.AppConfig})
	config.Authn.Enabled = false

	_, err := (&App{}).RunTenantOwnerRepair(t.Context(), config)
	assert.ErrorIs(t, err, ErrPlatformAdminUnavailable)
}

// --- decision-level tests -------------------------------------------------

// stubTenantRepairStores drives the decision without a database, so each
// refusal below is the full set of inputs with exactly one property taken away.
type stubTenantRepairStores struct {
	tenant       organization.Tenant
	tenantFound  bool
	tenantErr    error
	owner        identity.Principal
	ownerFound   bool
	ownerErr     error
	unclaimed    bool
	unclaimedErr error
	result       core.TenantBootstrapResult
	bootstrapErr error
	revisionErr  error
	appendErr    error

	bootstrapped []core.TenantBootstrapInput
	bumped       []guid.ID
	appended     []iamaudit.DomainEvent
}

func (stub *stubTenantRepairStores) stores() tenantRepairStores {
	return tenantRepairStores{
		tenants: func(context.Context, *gorm.DB, string) (organization.Tenant, bool, error) {
			return stub.tenant, stub.tenantFound, stub.tenantErr
		},
		principals: identity.BootstrapPrincipalStore{
			Find: func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
				return stub.owner, stub.ownerFound, stub.ownerErr
			},
		},
		bootstrap: core.TenantBootstrapper{
			Bootstrap: func(
				_ context.Context, _ *gorm.DB, input core.TenantBootstrapInput, _ time.Time,
			) (core.TenantBootstrapResult, error) {
				stub.bootstrapped = append(stub.bootstrapped, input)
				return stub.result, stub.bootstrapErr
			},
			IsUnclaimed: func(context.Context, *gorm.DB, guid.ID) (bool, error) {
				return stub.unclaimed, stub.unclaimedErr
			},
			BumpRevision: func(_ context.Context, _ *gorm.DB, tenantID guid.ID, _ time.Time) error {
				stub.bumped = append(stub.bumped, tenantID)
				return stub.revisionErr
			},
		},
		appender: func(_ context.Context, _ *gorm.DB, event iamaudit.DomainEvent) error {
			stub.appended = append(stub.appended, event)
			return stub.appendErr
		},
	}
}

// repairableTenant is the only tenant shape a repair will act on.
func repairableTenant() organization.Tenant {
	return organization.Tenant{ID: orphanTenantID, Slug: "acme", Status: organization.TenantActive}
}

// eligibleOwner is the only principal shape a repair will install.
func eligibleOwner() identity.Principal {
	return identity.Principal{
		ID: 7500, LoginName: "admin", DisplayName: "Platform Operator",
		Email: "ops@example.test", Status: platformAdminActiveStatus,
	}
}

// fullyRepairable is every input a successful repair needs. Each refusal test
// below is this value with one property changed.
func fullyRepairable() *stubTenantRepairStores {
	return &stubTenantRepairStores{
		tenant: repairableTenant(), tenantFound: true,
		owner: eligibleOwner(), ownerFound: true, unclaimed: true,
		result: core.TenantBootstrapResult{AdministratorRoleID: 4242, PermissionCount: 9, MenuCount: 15},
	}
}

// TestRepairDecisionWritesTheSameStateOrdinaryCreationWould is what makes a
// repaired tenant worth having: it must be indistinguishable from one that was
// bootstrapped correctly, including the cache invalidation.
func TestRepairDecisionWritesTheSameStateOrdinaryCreationWould(t *testing.T) {
	stub := fullyRepairable()
	now := time.Now().UTC()

	report, err := stub.stores().decideAndWrite(t.Context(), nil, "acme", "admin", now)
	require.NoError(t, err)
	assert.Equal(t, TenantOwnerRepaired, report.Outcome)
	assert.EqualValues(t, 4242, report.AdministratorRoleID)
	assert.Equal(t, 9, report.PermissionCount)
	assert.Equal(t, 15, report.MenuCount)

	require.Len(t, stub.bootstrapped, 1, "a tenant is repaired exactly once")
	assert.Equal(t, orphanTenantID, stub.bootstrapped[0].TenantID)
	assert.EqualValues(t, 7500, stub.bootstrapped[0].OwnerID)
	assert.Equal(t, "Platform Operator", stub.bootstrapped[0].DisplayName,
		"the membership row carries the profile identity reports, not one the repair invented")
	assert.Equal(t, "ops@example.test", stub.bootstrapped[0].Email)
	assert.Equal(t, []guid.ID{orphanTenantID}, stub.bumped,
		"a grant nobody is told about leaves a cache answering that the owner may do nothing")
}

// TestRepairDecisionRecordsThatTheTenantWasRepaired keeps the one difference
// between a repair and an ordinary bootstrap discoverable. Both write the same
// tables, so the audit trail is the only place it survives.
func TestRepairDecisionRecordsThatTheTenantWasRepaired(t *testing.T) {
	stub := fullyRepairable()

	_, err := stub.stores().decideAndWrite(t.Context(), nil, "acme", "admin", time.Now().UTC())
	require.NoError(t, err)

	require.Len(t, stub.appended, 1)
	event := stub.appended[0]
	assert.Equal(t, "tenant_bootstrapped", event.EventType)
	assert.Equal(t, "tenant", event.TargetType)
	assert.Equal(t, orphanTenantID, event.TargetID)
	assert.Equal(t, orphanTenantID, event.TenantID)
	assert.EqualValues(t, 7500, event.PrincipalID)
	assert.Equal(t, true, event.Detail["repaired"],
		"an auditor asking why a tenant gained an owner long after creation has to find it here")
	assert.Equal(t, "7500", event.Detail["owner_principal_id"])
	assert.Equal(t, "4242", event.Detail["administrator_role_id"])
	assert.Equal(t, 9, event.Detail["permission_count"])
	assert.Equal(t, 15, event.Detail["menu_count"])
}

// TestRepairDecisionRefusesAnythingButAnUnclaimedTenantAndAnActiveOwner is the
// safety property the command exists under. It is a recovery path for tenants
// nobody administers, not a way to grant somebody a tenant out of band.
func TestRepairDecisionRefusesAnythingButAnUnclaimedTenantAndAnActiveOwner(t *testing.T) {
	for name, mutate := range map[string]func(*stubTenantRepairStores){
		"tenant already has a member or a role": func(stub *stubTenantRepairStores) {
			stub.unclaimed = false
		},
		"tenant is deleted": func(stub *stubTenantRepairStores) {
			stub.tenant.Status = organization.TenantDeleted
		},
		"owner is disabled": func(stub *stubTenantRepairStores) { stub.owner.Status = "disabled" },
		"owner is locked":   func(stub *stubTenantRepairStores) { stub.owner.Status = "locked" },
	} {
		t.Run(name, func(t *testing.T) {
			stub := fullyRepairable()
			mutate(stub)

			_, err := stub.stores().decideAndWrite(t.Context(), nil, "acme", "admin", time.Now().UTC())
			assert.ErrorIs(t, err, ErrTenantRepairConflict)
			assert.Empty(t, stub.bootstrapped, "a refusal must write nothing")
			assert.Empty(t, stub.appended, "a refusal must write no audit event")
		})
	}
}

// TestRepairDecisionSeparatesAnAbsentTargetFromAConflict keeps a mistyped
// argument from being reported as a state an operator has to go investigate.
func TestRepairDecisionSeparatesAnAbsentTargetFromAConflict(t *testing.T) {
	for name, mutate := range map[string]func(*stubTenantRepairStores){
		"no tenant has that slug":     func(stub *stubTenantRepairStores) { stub.tenantFound = false },
		"no principal has that login": func(stub *stubTenantRepairStores) { stub.ownerFound = false },
	} {
		t.Run(name, func(t *testing.T) {
			stub := fullyRepairable()
			mutate(stub)

			_, err := stub.stores().decideAndWrite(t.Context(), nil, "acme", "admin", time.Now().UTC())
			assert.ErrorIs(t, err, ErrTenantRepairNotFound)
			assert.NotErrorIs(t, err, ErrTenantRepairConflict, "an absent target is not a conflict")
			assert.Empty(t, stub.bootstrapped)
		})
	}
}

// TestRepairDecisionSurfacesEveryStoreFailureDistinctly keeps a storage fault
// from being reported as a refusal, which would send an operator looking for a
// privilege problem that does not exist.
func TestRepairDecisionSurfacesEveryStoreFailureDistinctly(t *testing.T) {
	fault := errors.New("store is down")
	for name, mutate := range map[string]func(*stubTenantRepairStores){
		"tenant lookup":   func(stub *stubTenantRepairStores) { stub.tenantErr = fault },
		"owner lookup":    func(stub *stubTenantRepairStores) { stub.ownerErr = fault },
		"unclaimed check": func(stub *stubTenantRepairStores) { stub.unclaimedErr = fault },
		"bootstrap":       func(stub *stubTenantRepairStores) { stub.bootstrapErr = fault },
		"revision bump":   func(stub *stubTenantRepairStores) { stub.revisionErr = fault },
		"audit append":    func(stub *stubTenantRepairStores) { stub.appendErr = fault },
	} {
		t.Run(name, func(t *testing.T) {
			stub := fullyRepairable()
			mutate(stub)

			_, err := stub.stores().decideAndWrite(t.Context(), nil, "acme", "admin", time.Now().UTC())
			require.ErrorIs(t, err, fault)
			assert.NotErrorIs(t, err, ErrTenantRepairConflict, "a fault is not a conflict")
			assert.NotErrorIs(t, err, ErrTenantRepairNotFound)
		})
	}
}
