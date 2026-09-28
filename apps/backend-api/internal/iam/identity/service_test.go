package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testPassword = "correct-horse-battery"

func TestCreateRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	cases := []struct {
		name        string
		tenantID    guid.ID
		loginName   string
		password    string
		displayName string
		email       string
	}{
		{"zero tenant", 0, "alice", testPassword, "Alice", "alice@example.test"},
		{"blank login", 7, "   ", testPassword, "Alice", "alice@example.test"},
		{"long login", 7, strings.Repeat("a", 201), testPassword, "Alice", "alice@example.test"},
		{"short password", 7, "alice", "short", "Alice", "alice@example.test"},
		{"long password", 7, "alice", strings.Repeat("p", 1025), "Alice", "alice@example.test"},
		{"long display name", 7, "alice", testPassword, strings.Repeat("d", 129), "alice@example.test"},
		{"long email", 7, "alice", testPassword, "Alice", strings.Repeat("e", 321)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := harness.service.Create(
				t.Context(), testCase.tenantID, testCase.loginName,
				testCase.password, testCase.displayName, testCase.email,
			)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
	assert.Zero(t, harness.principalCount(t))
	assert.Empty(t, harness.events())
}

func TestCreateWritesPrincipalCredentialMembershipAndRevision(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "  Alice  ", testPassword, " Alice Smith ", "Alice@Example.test")
	require.NoError(t, err)

	assert.Equal(t, guid.ID(1001), principal.ID)
	assert.Equal(t, "alice", principal.LoginName)
	assert.Equal(t, "alice@example.test", principal.Email)
	assert.Equal(t, "Alice Smith", principal.DisplayName)
	assert.Equal(t, "active", principal.Status)
	assert.False(t, principal.EmailVerified)
	assert.False(t, principal.ActivationRequired)
	assert.Equal(t, identityTestNow.Truncate(time.Millisecond), principal.CreatedAt)

	row := harness.principal(t, principal.ID)
	assert.Equal(t, "alice", row.LoginName)
	assert.Nil(t, row.DisabledAt)
	assert.Equal(t, int64(1), harness.credentialVersion(t, principal.ID))
	member := harness.member(t, 7, principal.ID)
	assert.Equal(t, "active", member.Status)
	assert.Equal(t, "Alice Smith", member.DisplayName)
	assert.Equal(t, "alice@example.test", member.Email)
	assert.Equal(t, int64(1), harness.revision(t, 7))

	events := harness.events()
	require.Len(t, events, 1)
	assert.Equal(t, "principal_created", events[0].EventType)
	assert.Equal(t, guid.ID(7), events[0].TenantID)
	assert.Equal(t, true, events[0].Detail["membership_created"])
	assert.Equal(t, "active", events[0].Detail["status"])
}

func TestCreateDefaultsDisplayNameToLogin(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	principal, err := harness.service.Create(t.Context(), 7, "Bob", testPassword, "   ", "")
	require.NoError(t, err)
	assert.Equal(t, "bob", principal.DisplayName)
	assert.Empty(t, principal.Email)
}

func TestCreateRejectsDuplicateLoginAndRollsBack(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	_, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)

	_, err = harness.service.Create(t.Context(), 8, "alice", testPassword, "Alice Again", "other@example.test")
	assert.ErrorIs(t, err, ErrLoginConflict)
	assert.Equal(t, int64(1), harness.principalCount(t))
	assert.Zero(t, harness.memberCount(t, 8, 1002))
	assert.Zero(t, harness.revision(t, 8))
	assert.Len(t, harness.events(), 1)
}

func TestCreateAbortsWhenIDSourceFails(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	harness.repository.ids = failingIDSource{}
	_, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "")
	assert.EqualError(t, err, "id source down")
	assert.Zero(t, harness.principalCount(t))
}

func TestCreateRollsBackWhenAuditFails(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	harness.auditErr = errors.New("audit sink down")
	_, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create principal")
	assert.Zero(t, harness.principalCount(t))
	assert.Zero(t, harness.memberCount(t, 7, 1001))
	assert.Zero(t, harness.revision(t, 7))
}

func TestCreateInvitedPrincipalValidatesAndWritesThroughCallerTransaction(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")

	_, err := harness.service.CreateInvitedPrincipal(
		t.Context(), nil, 7, "alice", testPassword, "Alice", "alice@example.test",
	)
	assert.ErrorIs(t, err, ErrInvalid)

	invalidEmails := []string{"", "not-an-email", "Alice <alice@example.test>"}
	for _, email := range invalidEmails {
		_, err := harness.service.CreateInvitedPrincipal(
			t.Context(), harness.db, 7, "alice", testPassword, "Alice", email,
		)
		assert.ErrorIs(t, err, ErrInvalid, "email %q must be rejected", email)
	}

	var invited guid.ID
	require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
		id, err := harness.service.CreateInvitedPrincipal(
			t.Context(), tx, 7, "Alice", testPassword, "", "Alice@Example.test",
		)
		invited = id
		return err
	}))

	row := harness.principal(t, invited)
	assert.Equal(t, "alice", row.LoginName)
	assert.Equal(t, "alice", row.DisplayName)
	assert.True(t, row.EmailVerified)
	assert.Equal(t, int64(1), harness.credentialVersion(t, invited))
	assert.Equal(t, "active", harness.member(t, 7, invited).Status)
	// The invitation flow leaves the policy revision to the organization module
	// that owns the invitation row.
	assert.Zero(t, harness.revision(t, 7))
	assert.Empty(t, harness.events())
}

func TestCreateInvitedPrincipalRollsBackWithCallerTransaction(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	failure := errors.New("invitation rejected")
	err := harness.db.Transaction(func(tx *gorm.DB) error {
		if _, err := harness.service.CreateInvitedPrincipal(
			t.Context(), tx, 7, "alice", testPassword, "Alice", "alice@example.test",
		); err != nil {
			return err
		}
		return failure
	})
	assert.ErrorIs(t, err, failure)
	assert.Zero(t, harness.principalCount(t))
}

func TestCreatePendingPrincipalGrantsNoMembership(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	ids := newIdentityTestIDs(5000)

	_, err := harness.service.CreatePendingPrincipal(
		t.Context(), nil, "alice@example.test", "hash", "Alice", identityTestNow, ids,
	)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "alice@example.test", "", "Alice", identityTestNow, ids,
	)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "not-an-email", "hash", "Alice", identityTestNow, ids,
	)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "alice@example.test", "hash", "Alice", identityTestNow, nil,
	)
	assert.ErrorIs(t, err, ErrInvalid)

	id, err := harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, " Alice@Example.test ", "hash", "  ", identityTestNow, ids,
	)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(5001), id)

	row := harness.principal(t, id)
	assert.Equal(t, "alice@example.test", row.LoginName)
	assert.Equal(t, "alice@example.test", row.DisplayName)
	assert.True(t, row.ActivationRequired)
	assert.False(t, row.EmailVerified)
	assert.Equal(t, int64(1), harness.credentialVersion(t, id))
	assert.Zero(t, harness.memberCount(t, 7, id))
}

func TestEnsureExternalPrincipalCreatesButNeverReusesIdentity(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")

	id, created, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "Ext@Example.test", "External User", identityTestNow,
	)
	require.NoError(t, err)
	assert.True(t, created)
	row := harness.principal(t, id)
	assert.Equal(t, "ext@example.test", row.LoginName)
	assert.True(t, row.EmailVerified)
	assert.Equal(t, "active", harness.member(t, 7, id).Status)
	assert.Equal(t, int64(1), harness.revision(t, 7))

	again, created, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "ext@example.test", "External User", identityTestNow,
	)
	require.ErrorIs(t, err, ErrLoginConflict)
	assert.False(t, created)
	assert.Zero(t, again)
	assert.Equal(t, int64(1), harness.revision(t, 7), "existing membership must not bump the revision")

	other, created, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 8, "ext@example.test", "External User", identityTestNow,
	)
	require.ErrorIs(t, err, ErrLoginConflict)
	assert.False(t, created)
	assert.Zero(t, other)
	assert.Zero(t, harness.memberCount(t, 8, id))
	assert.Zero(t, harness.revision(t, 8))
	assert.Equal(t, int64(1), harness.principalCount(t))
}

func TestEnsureExternalPrincipalRejectsInvalidAndInactive(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	for _, testCase := range []struct {
		name     string
		tenantID guid.ID
		email    string
		display  string
	}{
		{"zero tenant", 0, "ext@example.test", "External"},
		{"invalid email", 7, "not-an-email", "External"},
		{"long display", 7, "ext@example.test", strings.Repeat("d", 129)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := harness.service.EnsureExternalPrincipal(
				t.Context(), harness.db, testCase.tenantID, testCase.email, testCase.display, identityTestNow,
			)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
	_, _, err := harness.service.EnsureExternalPrincipal(
		t.Context(), nil, 7, "ext@example.test", "External", identityTestNow,
	)
	assert.ErrorIs(t, err, ErrInvalid)

	id, _, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "ext@example.test", "External", identityTestNow,
	)
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).
		Where("id = ?", id).Update("status", "disabled").Error)

	_, _, err = harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 9, "ext@example.test", "External", identityTestNow,
	)
	assert.ErrorIs(t, err, ErrLoginConflict)
	assert.Zero(t, harness.memberCount(t, 9, id))
}

func TestEnsureExternalPrincipalRejectsDisabledMembership(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	id, _, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "ext@example.test", "External", identityTestNow,
	)
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_tenant_members")).
		Where("tenant_id = ? AND principal_id = ?", 7, id).Update("status", "disabled").Error)

	_, _, err = harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "ext@example.test", "External", identityTestNow,
	)
	assert.ErrorIs(t, err, ErrLoginConflict)
	assert.Equal(t, "disabled", harness.member(t, 7, id).Status)
}

func TestPrincipalByLoginOrEmailRejectsAmbiguousEmail(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	for index, login := range []string{"first", "second"} {
		_, err := harness.service.Create(
			t.Context(), guid.ID(7+index), login, testPassword, login, "shared@example.test",
		)
		require.NoError(t, err)
	}
	_, _, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "shared@example.test", "Shared", identityTestNow,
	)
	assert.ErrorIs(t, err, ErrLoginConflict)
}

func TestListAppliesTenantScopeSearchAndPaging(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	for _, login := range []string{"alice", "bob", "carol"} {
		_, err := harness.service.Create(t.Context(), 7, login, testPassword, login, login+"@example.test")
		require.NoError(t, err)
	}
	_, err := harness.service.Create(t.Context(), 8, "dave", testPassword, "Dave", "dave@example.test")
	require.NoError(t, err)

	items, total, err := harness.service.List(t.Context(), 7, "", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, items, 3)
	assert.Equal(t, []string{"alice", "bob", "carol"}, []string{
		items[0].LoginName, items[1].LoginName, items[2].LoginName,
	})

	items, total, err = harness.service.List(t.Context(), 7, "BOB", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "bob", items[0].LoginName)

	items, total, err = harness.service.List(t.Context(), 7, "", 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, items, 1)
	assert.Equal(t, "bob", items[0].LoginName)

	items, total, err = harness.service.List(t.Context(), 9, "", 10, 0)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, items)

	for _, invalid := range [][3]int{{0, 0, 0}, {0, 201, 0}, {0, 10, -1}} {
		_, _, err := harness.service.List(t.Context(), 7, "", invalid[1], invalid[2])
		assert.ErrorIs(t, err, ErrInvalid)
	}
	_, _, err = harness.service.List(t.Context(), 0, "", 10, 0)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestListAndGetExcludeServiceAccounts(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	harness.seedTenant(t, 7, "active")
	_, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)

	items, total, err := harness.service.List(t.Context(), 7, "", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "alice", items[0].LoginName)

	_, err = harness.service.Get(t.Context(), 7, account.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGetIsTenantScoped(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)

	found, err := harness.service.Get(t.Context(), 7, principal.ID)
	require.NoError(t, err)
	assert.Equal(t, principal.ID, found.ID)

	_, err = harness.service.Get(t.Context(), 8, principal.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = harness.service.Get(t.Context(), 0, principal.ID)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = harness.service.Get(t.Context(), 7, 0)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestUpdateRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	blank, long := "   ", strings.Repeat("d", 129)
	longEmail := strings.Repeat("e", 321)
	display := "Alice"
	for _, testCase := range []struct {
		name        string
		tenantID    guid.ID
		id          guid.ID
		displayName *string
		email       *string
	}{
		{"zero tenant", 0, 10, &display, nil},
		{"zero id", 7, 0, &display, nil},
		{"no field", 7, 10, nil, nil},
		{"blank display", 7, 10, &blank, nil},
		{"long display", 7, 10, &long, nil},
		{"long email", 7, 10, nil, &longEmail},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := harness.service.Update(
				t.Context(), testCase.tenantID, testCase.id, testCase.displayName, testCase.email,
			)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestUpdateEmailResetsVerificationAndRevokesSecurityState(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).
		Where("id = ?", principal.ID).Update("email_verified", true).Error)
	harness.seedSession(t, 11, principal.ID)
	harness.seedRefreshToken(t, "hash", principal.ID)
	harness.seedRecoveryToken(t, 21, principal.ID)
	harness.seedVerificationToken(t, 31, principal.ID)

	newEmail := "Alice.New@Example.test"
	updated, err := harness.service.Update(selfContext(t.Context(), principal.ID), 7, principal.ID, nil, &newEmail)
	require.NoError(t, err)

	assert.Equal(t, "alice.new@example.test", updated.Email)
	assert.False(t, updated.EmailVerified)
	assert.Equal(t, int64(2), harness.credentialVersion(t, principal.ID))
	assert.Zero(t, harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
	assert.Zero(t, harness.nullTimeCount(t, "iam_refresh_tokens", "revoked_at", principal.ID))
	assert.Zero(t, harness.nullTimeCount(t, "iam_password_recovery_tokens", "consumed_at", principal.ID))
	assert.Zero(t, harness.nullTimeCount(t, "iam_email_verification_tokens", "consumed_at", principal.ID))
	assert.Equal(t, "alice@example.test", harness.member(t, 7, principal.ID).Email)

	events := harness.events()
	require.Len(t, events, 2)
	assert.Equal(t, "principal_updated", events[1].EventType)
	assert.Equal(t, false, events[1].Detail["display_name_changed"])
	assert.Equal(t, true, events[1].Detail["email_changed"])
}

func TestUpdateDisplayNameKeepsCredentialAndSessions(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	harness.seedSession(t, 11, principal.ID)

	display := "  Alice Smith  "
	updated, err := harness.service.Update(selfContext(t.Context(), principal.ID), 7, principal.ID, &display, nil)
	require.NoError(t, err)

	assert.Equal(t, "Alice Smith", updated.DisplayName)
	assert.Equal(t, int64(1), harness.credentialVersion(t, principal.ID))
	assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
	assert.Equal(t, "Alice", harness.member(t, 7, principal.ID).DisplayName)

	events := harness.events()
	require.Len(t, events, 2)
	assert.Equal(t, true, events[1].Detail["display_name_changed"])
	assert.Equal(t, false, events[1].Detail["email_changed"])
}

func TestUpdateCannotReachAnotherTenantsPrincipal(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)

	display := "Renamed By Other Tenant"
	_, err = harness.service.Update(selfContext(t.Context(), principal.ID), 8, principal.ID, &display, nil)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "Alice", harness.principal(t, principal.ID).DisplayName)
}

func TestSetStatusDisableProtectsLastAdministratorPerActiveTenant(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	// A membership that is no longer active neither blocks the tenant route nor
	// needs its administrators protected.
	require.NoError(t, harness.repository.insertTenantMember(t.Context(), tenantMemberRecord{
		TenantID: 8, PrincipalID: principal.ID, DisplayName: "Alice", Email: "alice@example.test",
		Status: "disabled", CreatedAt: identityTestNow, UpdatedAt: identityTestNow,
	}))
	harness.seedSession(t, 11, principal.ID)
	harness.seedRefreshToken(t, "hash", principal.ID)

	updated, err := harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.NoError(t, err)

	assert.Equal(t, "disabled", updated.Status)
	assert.Equal(t, []guid.ID{7}, harness.guard.protectedTenants())
	assert.Equal(t, 1, harness.guard.verifiedCount())
	assert.NotNil(t, harness.principal(t, principal.ID).DisabledAt)
	assert.Zero(t, harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
	assert.Zero(t, harness.nullTimeCount(t, "iam_refresh_tokens", "revoked_at", principal.ID))

	events := harness.events()
	require.Len(t, events, 2)
	assert.Equal(t, "principal_disabled", events[1].EventType)
	assert.Equal(t, true, events[1].Detail["changed"])
	assert.Equal(t, "disabled", events[1].Detail["status"])
}

// A principal is global, so the tenant suspension route would lock it out of
// every other organization and of any platform authority it holds. Those
// accounts are left to their other owners; the tenant removes its membership.
func TestSetStatusRefusesSuspendingAPrincipalSharedBeyondTheTenant(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	harness.seedSession(t, 11, principal.ID)
	unchanged := func() {
		t.Helper()
		assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
		assert.Nil(t, harness.principal(t, principal.ID).DisabledAt)
		assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
		assert.Len(t, harness.events(), 1, "a refused suspension records no event")
	}

	// Control: the platform route already refuses an account that holds platform authority.
	harness.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error {
		return core.ErrPrincipalHasPlatformAccess
	}
	_, err = harness.service.SetPlatformStatus(t.Context(), principal.ID, "disabled")
	require.ErrorIs(t, err, core.ErrPrincipalHasPlatformAccess)
	protected := harness.guard.protectedTenants()
	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.ErrorIs(t, err, ErrPrincipalSharedBeyondTenant)
	require.ErrorIs(t, err, core.ErrPrincipalHasPlatformAccess)
	unchanged()

	guardFailure := errors.New("platform grants unreadable")
	harness.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return guardFailure }
	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.ErrorIs(t, err, guardFailure)
	assert.NotErrorIs(t, err, ErrPrincipalSharedBeyondTenant)
	harness.service.platformAccessGuard = nil
	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.ErrorIs(t, err, ErrUnavailable, "without the platform guard the tenant route fails closed")
	unchanged()

	harness.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	require.NoError(t, harness.repository.insertTenantMember(t.Context(), tenantMemberRecord{
		TenantID: 9, PrincipalID: principal.ID, DisplayName: "Alice", Email: "alice@example.test",
		Status: "active", CreatedAt: identityTestNow, UpdatedAt: identityTestNow,
	}))
	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.ErrorIs(t, err, ErrPrincipalSharedBeyondTenant, "an active member of another organization root")
	_, err = harness.service.SetStatus(t.Context(), 9, principal.ID, "disabled")
	require.ErrorIs(t, err, ErrPrincipalSharedBeyondTenant, "the other root is refused symmetrically")
	unchanged()
	assert.Equal(t, protected, harness.guard.protectedTenants(), "refusal precedes the administrator guard")
}

func TestSetStatusRollsBackWhenGuardRejects(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	harness.seedSession(t, 11, principal.ID)
	harness.guard.verifyErr = errors.New("last tenant administrator")

	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last tenant administrator")

	assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
	assert.Nil(t, harness.principal(t, principal.ID).DisabledAt)
	assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
	assert.Len(t, harness.events(), 1)
}

func TestSetStatusPropagatesGuardAcquisitionFailure(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	harness.guard.protectErr = errors.New("revision lock unavailable")

	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revision lock unavailable")
	assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
}

func TestSetStatusRestoreSkipsGuardAndClearsDisabledAt(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.NoError(t, err)

	restored, err := harness.service.SetStatus(t.Context(), 7, principal.ID, "active")
	require.NoError(t, err)
	assert.Equal(t, "active", restored.Status)
	assert.Nil(t, harness.principal(t, principal.ID).DisabledAt)
	assert.Len(t, harness.guard.protectedTenants(), 1, "restore must not take the administrator guard")

	events := harness.events()
	require.Len(t, events, 3)
	assert.Equal(t, "principal_restored", events[2].EventType)
	assert.Equal(t, true, events[2].Detail["changed"])
}

func TestSetStatusRejectsInvalidStatusAndForeignTenant(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)

	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "locked")
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = harness.service.SetStatus(t.Context(), 0, principal.ID, "disabled")
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = harness.service.SetStatus(t.Context(), 7, 0, "disabled")
	assert.ErrorIs(t, err, ErrInvalid)

	_, err = harness.service.SetStatus(t.Context(), 8, principal.ID, "disabled")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
	assert.Empty(t, harness.guard.protectedTenants())
}

func TestSetStatusIdempotentDisableSkipsGuard(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.NoError(t, err)

	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.NoError(t, err)
	assert.Len(t, harness.guard.protectedTenants(), 1, "an already disabled principal needs no guard")

	events := harness.events()
	require.Len(t, events, 3)
	assert.Equal(t, false, events[2].Detail["changed"])
}

func TestCreateStoresVerifiablePasswordHash(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)

	var hash string
	require.NoError(t, harness.db.Table(harness.table("iam_credentials")).Select("password_hash").
		Where("principal_id = ?", principal.ID).Scan(&hash).Error)
	assert.NotContains(t, hash, testPassword)
	ok, err := passwordx.Verify(hash, testPassword)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestIdentityTimeNormalizesToUTCMilliseconds(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("UTC+8", 8*60*60)
	value := time.Date(2026, 9, 3, 20, 0, 0, 123_456_789, zone)
	normalized := identityTime(value)
	assert.Equal(t, time.UTC, normalized.Location())
	assert.Equal(t, 123_000_000, normalized.Nanosecond())
	assert.Equal(t, "alice", normalizeLogin("  ALICE  "))
}

func TestCreateInvitedPrincipalSurfacesWriteFailures(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	_, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)

	_, err = harness.service.CreateInvitedPrincipal(
		t.Context(), harness.db, 7, "alice", testPassword, "Alice", "other@example.test",
	)
	assert.ErrorIs(t, err, ErrLoginConflict)

	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_credentials")).Error)
	_, err = harness.service.CreateInvitedPrincipal(
		t.Context(), harness.db, 7, "bob", testPassword, "Bob", "bob@example.test",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert invited credential")

	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_tenant_members")).Error)
	_, err = harness.service.CreateInvitedPrincipal(
		t.Context(), harness.db, 7, "carol", testPassword, "Carol", "carol@example.test",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert invited credential")
}

func TestCreatePendingPrincipalSurfacesWriteFailures(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	ids := newIdentityTestIDs(6000)
	_, err := harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "alice@example.test", "hash", "Alice", identityTestNow, ids,
	)
	require.NoError(t, err)

	_, err = harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "alice@example.test", "hash", "Alice", identityTestNow, ids,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert pending principal")

	_, err = harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "bob@example.test", "hash", "Bob", identityTestNow, failingIDSource{},
	)
	assert.EqualError(t, err, "id source down")

	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_credentials")).Error)
	_, err = harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "carol@example.test", "hash", "Carol", identityTestNow, ids,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert pending credential")
}

func TestEnsureExternalPrincipalRejectsServiceAccountLoginCollision(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	harness.seedTenant(t, 7, "active")
	// A service account owns the login this external identity would claim. The
	// lookup ignores service accounts, so the insert must refuse the collision.
	_, err := harness.service.CreateServiceAccount(t.Context(), 7, "ext@example.test", "Robot", "", nil)
	require.NoError(t, err)

	_, _, err = harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "ext@example.test", "External", identityTestNow,
	)
	assert.ErrorIs(t, err, ErrLoginConflict)
}

func TestEnsureExternalPrincipalSurfacesIDAndWriteFailures(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	harness.repository.ids = failingIDSource{}
	_, _, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "ext@example.test", "External", identityTestNow,
	)
	assert.EqualError(t, err, "id source down")

	harness.repository.ids = newIdentityTestIDs(7000)
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_tenant_members")).Error)
	_, _, err = harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "ext@example.test", "External", identityTestNow,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert external membership")
}

func TestCreateSurfacesMembershipFailure(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_tenant_members")).Error)

	_, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert tenant membership")
	assert.Zero(t, harness.principalCount(t))
}

func TestUpdateSurfacesMissingCredentialRow(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_credentials")).
		Where("principal_id = ?", principal.ID).Delete(nil).Error)

	email := "changed@example.test"
	_, err = harness.service.Update(selfContext(t.Context(), principal.ID), 7, principal.ID, nil, &email)
	assert.ErrorIs(t, err, errPrincipalCredentialNotFound)
	assert.Equal(t, "alice@example.test", harness.principal(t, principal.ID).Email)
}

func TestSetStatusSurfacesMembershipQueryFailure(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_sessions")).Error)

	_, err = harness.service.SetStatus(t.Context(), 7, principal.ID, "disabled")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "revoke principal sessions")
	assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
}

// A provider's verified email is not proof of ownership of a local account.
func TestEnsureExternalPrincipalRejectsEmailUnderADifferentLogin(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	local, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)

	id, created, err := harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 8, "Alice@Example.test", "Alice External", identityTestNow,
	)
	require.ErrorIs(t, err, ErrLoginConflict)
	assert.False(t, created, "an address already held by an account must not create a second one")
	assert.Zero(t, id)
	assert.Equal(t, "alice", harness.principal(t, local.ID).LoginName)
	assert.Zero(t, harness.memberCount(t, 8, local.ID))
	assert.Equal(t, int64(1), harness.principalCount(t))

	// An address shared by two accounts is ambiguous, so neither is picked.
	second, err := harness.service.Create(t.Context(), 7, "alice2", testPassword, "Alice Two", "alice@example.test")
	require.NoError(t, err)
	_, _, err = harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 8, "alice@example.test", "Alice External", identityTestNow,
	)
	assert.ErrorIs(t, err, ErrLoginConflict)
	assert.Zero(t, harness.memberCount(t, 8, second.ID))
}

// TestEnsureExternalPrincipalRefusesAServiceAccountLoginName proves an external
// login cannot take over a name a service account already owns: the human
// lookup skips service accounts, so the refusal has to come from the insert.
func TestEnsureExternalPrincipalRefusesAServiceAccountLoginName(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	harness.seedTenant(t, 7, "active")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot@example.test", "Robot", "", nil)
	require.NoError(t, err)

	_, _, err = harness.service.EnsureExternalPrincipal(
		t.Context(), harness.db, 7, "robot@example.test", "Robot Person", identityTestNow,
	)
	assert.ErrorIs(t, err, ErrLoginConflict)
	assert.Equal(t, int64(1), harness.principalCount(t))
	assert.Equal(t, "robot@example.test", harness.principal(t, account.ID).LoginName)
}

// TestPrincipalOperationsFailClosedAfterDatabaseShutdown proves a storage
// outage reaches the caller from every principal entry point instead of being
// reported as an empty page or an absent account.
func TestPrincipalOperationsFailClosedAfterDatabaseShutdown(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	harness.seedTenant(t, 7, "active")
	principal, err := harness.service.Create(t.Context(), 7, "alice", testPassword, "Alice", "alice@example.test")
	require.NoError(t, err)
	connection, err := harness.db.DB()
	require.NoError(t, err)
	require.NoError(t, connection.Close())

	ctx, display := t.Context(), "Alice Smith"
	for _, check := range []struct {
		name string
		call func() error
	}{
		{"create", func() error {
			_, err := harness.service.Create(ctx, 7, "bob", testPassword, "Bob", "")
			return err
		}},
		{"create invited", func() error {
			_, err := harness.service.CreateInvitedPrincipal(ctx, harness.db, 7, "carol", testPassword, "Carol", "")
			return err
		}},
		{"create pending", func() error {
			_, err := harness.service.CreatePendingPrincipal(
				ctx, harness.db, "dave@example.test", "hash", "Dave", identityTestNow, harness.repository.ids,
			)
			return err
		}},
		{"ensure external", func() error {
			_, _, err := harness.service.EnsureExternalPrincipal(
				ctx, harness.db, 7, "erin@example.test", "Erin", identityTestNow,
			)
			return err
		}},
		{"get", func() error { _, err := harness.service.Get(ctx, 7, principal.ID); return err }},
		{"update", func() error {
			_, err := harness.service.Update(selfContext(ctx, principal.ID), 7, principal.ID, &display, nil)
			return err
		}},
		{"set status", func() error {
			_, err := harness.service.SetStatus(ctx, 7, principal.ID, "disabled")
			return err
		}},
		{"set platform status", func() error {
			_, err := harness.service.SetPlatformStatus(ctx, principal.ID, "disabled")
			return err
		}},
		{"create provisioned", func() error {
			_, err := harness.service.CreateProvisionedTo(ctx, harness.db, 7, provisionedInput("scim.user"))
			return err
		}},
		{"replace provisioned", func() error {
			_, err := harness.service.ReplaceProvisionedTo(ctx, harness.db, 7, principal.ID, provisionedInput("scim.user"))
			return err
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			require.Error(t, check.call(), "a closed database must not read as an answer")
		})
	}

	rows, total, err := harness.service.List(ctx, 7, "", 10, 0)
	require.Error(t, err)
	assert.Empty(t, rows, "an unreachable database is not an empty tenant")
	assert.Zero(t, total)
}
