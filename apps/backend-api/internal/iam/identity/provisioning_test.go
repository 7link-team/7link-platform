package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func provisionedInput(login string) ProvisionedPrincipalInput {
	normalized := normalizeLogin(login)
	return ProvisionedPrincipalInput{
		LoginName: login, DisplayName: strings.ToUpper(normalized),
		Email: normalized + "@example.test", PasswordHash: "hash", Active: true,
	}
}

func TestCreateProvisionedToWritesThroughCallerTransaction(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")

	var principal Principal
	require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
		created, err := harness.service.CreateProvisionedTo(t.Context(), tx, 7, provisionedInput("  Scim.User  "))
		principal = created
		return err
	}))

	assert.Equal(t, "scim.user", principal.LoginName)
	assert.Equal(t, "SCIM.USER", principal.DisplayName)
	assert.Equal(t, "scim.user@example.test", principal.Email)
	assert.True(t, principal.ActivationRequired)
	assert.Equal(t, "active", principal.Status)

	row := harness.principal(t, principal.ID)
	assert.Equal(t, "scim.user", row.LoginName)
	assert.False(t, row.SelfRegistered, "public registration never re-credentials a directory principal")
	assert.Equal(t, int64(1), harness.credentialVersion(t, principal.ID))
	member := harness.member(t, 7, principal.ID)
	assert.Equal(t, "active", member.Status)
	assert.Nil(t, member.DisabledAt)
	assert.Equal(t, int64(1), harness.revision(t, 7))
	// The provisioning module owns the SCIM audit trail.
	assert.Empty(t, harness.events())
}

func TestCreateProvisionedToDisabledMemberIsAdmittedButInactive(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	input := provisionedInput("scim.user")
	input.Active = false

	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, input)
	require.NoError(t, err)

	member := harness.member(t, 7, principal.ID)
	assert.Equal(t, "disabled", member.Status)
	assert.NotNil(t, member.DisabledAt)
	assert.Equal(t, "active", harness.principal(t, principal.ID).Status,
		"a tenant admission decision must not disable the global identity")
}

func TestCreateProvisionedToRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	for _, testCase := range []struct {
		name     string
		tenantID guid.ID
		mutate   func(*ProvisionedPrincipalInput)
	}{
		{"zero tenant", 0, func(*ProvisionedPrincipalInput) {}},
		{"blank login", 7, func(input *ProvisionedPrincipalInput) { input.LoginName = "  " }},
		{"long login", 7, func(input *ProvisionedPrincipalInput) { input.LoginName = strings.Repeat("l", 201) }},
		{"long display", 7, func(input *ProvisionedPrincipalInput) { input.DisplayName = strings.Repeat("d", 129) }},
		{"long email", 7, func(input *ProvisionedPrincipalInput) { input.Email = strings.Repeat("e", 321) }},
		{"invalid email", 7, func(input *ProvisionedPrincipalInput) { input.Email = "not-an-email" }},
		{"missing hash", 7, func(input *ProvisionedPrincipalInput) { input.PasswordHash = "" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			input := provisionedInput("scim.user")
			testCase.mutate(&input)
			_, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, testCase.tenantID, input)
			assert.ErrorIs(t, err, ErrInvalid)
		})
	}
	_, err := harness.service.CreateProvisionedTo(t.Context(), nil, 7, provisionedInput("scim.user"))
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Zero(t, harness.principalCount(t))
}

func TestCreateProvisionedToDefaultsDisplayNameAndAllowsBlankEmail(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	input := provisionedInput("scim.user")
	input.DisplayName, input.Email = "   ", ""

	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, input)
	require.NoError(t, err)
	assert.Equal(t, "scim.user", principal.DisplayName)
	assert.Empty(t, principal.Email)
}

func TestCreateProvisionedToRollsBackWithCallerTransaction(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "")
	failure := errors.New("scim batch aborted")
	err := harness.db.Transaction(func(tx *gorm.DB) error {
		if _, err := harness.service.CreateProvisionedTo(t.Context(), tx, 7, provisionedInput("scim.user")); err != nil {
			return err
		}
		return failure
	})
	assert.ErrorIs(t, err, failure)
	assert.Zero(t, harness.principalCount(t))
	assert.Zero(t, harness.revision(t, 7))
}

func TestReplaceProvisionedToUpdatesLocalProfileAndPreservesGlobalSecurityState(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).
		Where("id = ?", principal.ID).Update("email_verified", true).Error)
	harness.seedSession(t, 11, principal.ID)
	harness.seedRefreshToken(t, "hash", principal.ID)
	harness.seedRecoveryToken(t, 21, principal.ID)
	harness.seedVerificationToken(t, 31, principal.ID)

	input := provisionedInput("scim.user")
	input.Email, input.DisplayName = "local@example.test", "Local Name"
	updated, err := harness.service.ReplaceProvisionedTo(t.Context(), harness.db, 7, principal.ID, input)
	require.NoError(t, err)

	assert.Equal(t, "scim.user", updated.LoginName)
	assert.Equal(t, "local@example.test", updated.Email)
	assert.False(t, updated.EmailVerified)
	assert.Equal(t, int64(1), harness.credentialVersion(t, principal.ID))
	assert.EqualValues(t, 1, harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
	assert.EqualValues(t, 1, harness.nullTimeCount(t, "iam_refresh_tokens", "revoked_at", principal.ID))
	assert.EqualValues(t, 1, harness.nullTimeCount(t, "iam_password_recovery_tokens", "consumed_at", principal.ID))
	assert.EqualValues(t, 1, harness.nullTimeCount(t, "iam_email_verification_tokens", "consumed_at", principal.ID))
	assert.Equal(t, "local@example.test", harness.member(t, 7, principal.ID).Email)
	global, err := harness.service.Get(t.Context(), 7, principal.ID)
	require.NoError(t, err)
	assert.Equal(t, "scim.user@example.test", global.Email)
	assert.Equal(t, "SCIM.USER", global.DisplayName)
	assert.True(t, global.EmailVerified)
	assert.Equal(t, int64(2), harness.revision(t, 7))
	assert.Empty(t, harness.guard.protectedTenants(), "an active membership needs no administrator guard")
}

func TestReplaceProvisionedToKeepsCredentialWhenOnlyDisplayNameChanges(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	harness.seedSession(t, 11, principal.ID)

	input := provisionedInput("scim.user")
	input.DisplayName = "Renamed Only"
	updated, err := harness.service.ReplaceProvisionedTo(t.Context(), harness.db, 7, principal.ID, input)
	require.NoError(t, err)

	assert.Equal(t, "Renamed Only", updated.DisplayName)
	assert.Equal(t, int64(1), harness.credentialVersion(t, principal.ID))
	assert.Equal(t, int64(1), harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
	assert.Equal(t, int64(1), harness.revision(t, 7), "an unchanged email and membership must not bump the revision")
}

func TestProvisionedImmutableLoginAndTenantProjection(t *testing.T) {
	h := newIdentityHarness(t, "projection_")
	input := provisionedInput("scim.user")
	p, err := h.service.CreateProvisionedTo(t.Context(), h.db, 7, input)
	require.NoError(t, err)
	input.LoginName, input.DisplayName = "renamed", "Must not persist"
	_, err = h.service.ReplaceProvisionedTo(t.Context(), h.db, 7, p.ID, input)
	assert.ErrorIs(t, err, ErrImmutableProvisionedLogin)
	assert.Equal(t, "SCIM.USER", h.member(t, 7, p.ID).DisplayName)
	input.LoginName, input.DisplayName, input.Active = "scim.user", "Local", false
	var revoked []guid.ID
	h.service.revokeTenantTokens = func(_ context.Context, tx *gorm.DB, tenant, id guid.ID) error {
		require.NotNil(t, tx)
		assert.Equal(t, p.ID, id)
		revoked = append(revoked, tenant)
		return nil
	}
	h.service.revokeTenantOAuth = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) error {
		return h.service.revokeTenantTokens(ctx, tx, tenant, id)
	}
	_, err = h.service.ReplaceProvisionedTo(t.Context(), h.db, 7, p.ID, input)
	require.NoError(t, err)
	assert.Equal(t, []guid.ID{7, 7}, revoked)
	projection, err := h.service.GetProvisioned(t.Context(), 7, p.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", projection.Status)
	assert.Equal(t, "disabled", projection.MembershipStatus)
	assert.Equal(t, "Local", projection.DisplayName)
}

func TestReplaceProvisionedToDeactivationTakesAdministratorGuard(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	harness.seedSession(t, 11, principal.ID)

	input := provisionedInput("scim.user")
	input.Active = false
	_, err = harness.service.ReplaceProvisionedTo(t.Context(), harness.db, 7, principal.ID, input)
	require.NoError(t, err)

	member := harness.member(t, 7, principal.ID)
	assert.Equal(t, "disabled", member.Status)
	assert.NotNil(t, member.DisabledAt)
	assert.Equal(t, []guid.ID{7}, harness.guard.protectedTenants())
	assert.Equal(t, 1, harness.guard.verifiedCount())
	assert.EqualValues(t, 1, harness.nullTimeCount(t, "iam_sessions", "revoked_at", principal.ID))
	assert.Equal(t, int64(2), harness.revision(t, 7))
	assert.Equal(t, "active", harness.principal(t, principal.ID).Status)
}

func TestReplaceProvisionedToRollsBackWhenGuardRejects(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	var principalID guid.ID
	require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
		principal, err := harness.service.CreateProvisionedTo(t.Context(), tx, 7, provisionedInput("scim.user"))
		principalID = principal.ID
		return err
	}))
	harness.guard.verifyErr = errors.New("last tenant administrator")

	input := provisionedInput("scim.user")
	input.Active = false
	err := harness.db.Transaction(func(tx *gorm.DB) error {
		_, err := harness.service.ReplaceProvisionedTo(t.Context(), tx, 7, principalID, input)
		return err
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last tenant administrator")
	assert.Equal(t, "active", harness.member(t, 7, principalID).Status)
}

func TestReplaceProvisionedToRefusesToReactivateDisabledIdentity(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	input := provisionedInput("scim.user")
	input.Active = false
	_, err = harness.service.ReplaceProvisionedTo(t.Context(), harness.db, 7, principal.ID, input)
	require.NoError(t, err)
	require.NoError(t, harness.db.Table(harness.table("iam_principals")).
		Where("id = ?", principal.ID).Update("status", "disabled").Error)

	_, err = harness.service.ReplaceProvisionedTo(
		t.Context(), harness.db, 7, principal.ID, provisionedInput("scim.user"),
	)
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestReplaceProvisionedToIsTenantScoped(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)

	_, err = harness.service.ReplaceProvisionedTo(
		t.Context(), harness.db, 8, principal.ID, provisionedInput("hijacked"),
	)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "scim.user", harness.principal(t, principal.ID).LoginName)

	_, err = harness.service.ReplaceProvisionedTo(t.Context(), harness.db, 7, 0, provisionedInput("scim.user"))
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = harness.service.ReplaceProvisionedTo(t.Context(), nil, 7, principal.ID, provisionedInput("scim.user"))
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestReplaceProvisionedToRejectsMissingMembership(t *testing.T) {
	t.Parallel()

	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreatePendingPrincipal(
		t.Context(), harness.db, "pending@example.test", "hash", "Pending", identityTestNow, newIdentityTestIDs(700),
	)
	require.NoError(t, err)
	require.NoError(t, harness.repository.insertTenantMember(t.Context(), tenantMemberRecord{
		TenantID: 7, PrincipalID: principal, DisplayName: "Pending", Email: "pending@example.test",
		Status: "active", CreatedAt: identityTestNow, UpdatedAt: identityTestNow,
	}))
	require.NoError(t, harness.db.Table(harness.table("iam_tenant_members")).
		Where("tenant_id = ? AND principal_id = ?", 7, principal).Delete(nil).Error)

	_, err = harness.service.ReplaceProvisionedTo(
		t.Context(), harness.db, 7, principal, provisionedInput("pending"),
	)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestNormalizeProvisionedInputTrimsAndValidates(t *testing.T) {
	t.Parallel()

	tenantID, input, err := normalizeProvisionedInput(7, ProvisionedPrincipalInput{
		LoginName: " SCIM.User ", DisplayName: " ", Email: " User@Example.test ", PasswordHash: "hash",
	}, true)
	require.NoError(t, err)
	assert.Equal(t, guid.ID(7), tenantID)
	assert.Equal(t, "scim.user", input.LoginName)
	assert.Equal(t, "scim.user", input.DisplayName)
	assert.Equal(t, "user@example.test", input.Email)

	_, _, err = normalizeProvisionedInput(7, ProvisionedPrincipalInput{LoginName: "scim.user"}, true)
	assert.ErrorIs(t, err, ErrInvalid)
	_, _, err = normalizeProvisionedInput(7, ProvisionedPrincipalInput{LoginName: "scim.user"}, false)
	assert.NoError(t, err, "replacement carries no password hash")
}

// TestReplaceProvisionedToDeactivationFailsWholeWhenTenantAccessCannotBeRevoked
// pins that a membership is only disabled together with revoking the tenant's
// tokens and OAuth grants: a disabled member who keeps a live token would
// still act inside the tenant.
func TestReplaceProvisionedToDeactivationFailsWholeWhenTenantAccessCannotBeRevoked(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	input := provisionedInput("scim.user")
	input.Active = false
	deactivate := func() error {
		return harness.db.Transaction(func(tx *gorm.DB) error {
			_, err := harness.service.ReplaceProvisionedTo(t.Context(), tx, 7, principal.ID, input)
			return err
		})
	}

	harness.service.revokeTenantTokens, harness.service.revokeTenantOAuth = nil, nil
	require.ErrorIs(t, deactivate(), ErrUnavailable)
	assert.Equal(t, "active", harness.member(t, 7, principal.ID).Status)

	tokenFailure := errors.New("token store unavailable")
	oauthCalled := false
	harness.service.revokeTenantTokens = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return tokenFailure }
	harness.service.revokeTenantOAuth = func(context.Context, *gorm.DB, guid.ID, guid.ID) error {
		oauthCalled = true
		return nil
	}
	require.ErrorIs(t, deactivate(), tokenFailure)
	assert.False(t, oauthCalled, "OAuth grants are not revoked after the token revocation failed")
	assert.Equal(t, "active", harness.member(t, 7, principal.ID).Status)
	assert.Equal(t, int64(1), harness.revision(t, 7), "the failed deactivation bumped no policy revision")
}

func TestGetProvisionedIsTenantScoped(t *testing.T) {
	harness := newIdentityHarness(t, "sl_")
	principal, err := harness.service.CreateProvisionedTo(t.Context(), harness.db, 7, provisionedInput("scim.user"))
	require.NoError(t, err)
	projection, err := harness.service.GetProvisioned(t.Context(), 7, principal.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", projection.MembershipStatus)

	_, err = harness.service.GetProvisioned(t.Context(), 7, 424242)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = harness.service.GetProvisioned(t.Context(), 8, principal.ID)
	require.ErrorIs(t, err, ErrNotFound, "another tenant cannot read this tenant's provisioned member")
}
