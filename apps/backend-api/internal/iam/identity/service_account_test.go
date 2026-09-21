package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/passwordx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newServiceAccountHarness(t *testing.T, prefix string) *identityHarness {
	t.Helper()
	harness := newIdentityHarness(t, prefix)
	harness.seedTenant(t, 7, "active")
	harness.seedTenant(t, 8, "active")
	return harness
}

func TestValidateServiceAccountRequiresActiveOwnedIdentity(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run("prefix_"+prefix, func(t *testing.T) {
			h := newServiceAccountHarness(t, prefix)
			account, err := h.service.CreateServiceAccount(t.Context(), 7, "scim-agent", "SCIM agent", "", nil)
			require.NoError(t, err)
			ordinary, err := h.service.Create(t.Context(), 7, "ordinary", testPassword, "Ordinary", "")
			require.NoError(t, err)
			for _, id := range []guid.ID{ordinary.ID, 999999} {
				active, err := h.service.ValidateServiceAccount(t.Context(), h.db, 7, id, identityTestNow)
				require.NoError(t, err)
				assert.False(t, active)
			}
			active, err := h.service.ValidateServiceAccount(t.Context(), h.db, 8, account.ID, identityTestNow)
			require.NoError(t, err)
			assert.False(t, active)
			for _, state := range []struct {
				table, where, column string
				arg, value           any
			}{
				{"iam_tenants", "id = ?", "status", guid.ID(7), "suspended"},
				{"iam_tenants", "id = ?", "status", guid.ID(7), "deleted"},
				{"iam_service_accounts", "principal_id = ?", "status", account.ID, "disabled"},
				{"iam_service_accounts", "principal_id = ?", "status", account.ID, "deleted"},
				{"iam_service_accounts", "principal_id = ?", "deleted_at", account.ID, identityTestNow},
				{"iam_service_accounts", "principal_id = ?", "expires_at", account.ID, identityTestNow},
				{"iam_service_accounts", "principal_id = ?", "owner_tenant_id", account.ID, guid.ID(8)},
				{"iam_principals", "id = ?", "status", account.ID, "disabled"},
				{"iam_principals", "id = ?", "activation_required", account.ID, true},
				{"iam_tenant_members", "principal_id = ?", "status", account.ID, "disabled"},
			} {
				t.Run(state.table+"/"+state.column, func(t *testing.T) {
					rollback := errors.New("rollback test state")
					err := h.db.Transaction(func(tx *gorm.DB) error {
						require.NoError(t, tx.Table(h.table(state.table)).Where(state.where, state.arg).Update(state.column, state.value).Error)
						active, err := h.service.ValidateServiceAccount(t.Context(), tx, 7, account.ID, identityTestNow)
						require.NoError(t, err)
						assert.False(t, active)
						return rollback
					})
					require.ErrorIs(t, err, rollback)
					active, err := h.service.ValidateServiceAccount(t.Context(), h.db, 7, account.ID, identityTestNow)
					require.NoError(t, err)
					assert.True(t, active)
				})
			}
			for _, ids := range [][2]guid.ID{{0, account.ID}, {-1, account.ID}, {7, 0}, {7, -1}} {
				_, err := h.service.ValidateServiceAccount(t.Context(), h.db, ids[0], ids[1], identityTestNow)
				assert.ErrorIs(t, err, ErrServiceAccountInvalid)
			}
			_, err = h.service.ValidateServiceAccount(t.Context(), nil, 7, account.ID, identityTestNow)
			assert.ErrorIs(t, err, ErrServiceAccountInvalid)
			_, err = h.service.ValidateServiceAccount(t.Context(), h.db, 7, account.ID, time.Time{})
			assert.ErrorIs(t, err, ErrServiceAccountInvalid)
			_, err = (*Service)(nil).ValidateServiceAccount(t.Context(), h.db, 7, account.ID, identityTestNow)
			assert.ErrorIs(t, err, ErrUnavailable)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, err = h.service.ValidateServiceAccount(ctx, h.db, 7, account.ID, identityTestNow)
			assert.ErrorIs(t, err, context.Canceled)
		})
	}
}

func (h *identityHarness) serviceAccountRow(t *testing.T, id guid.ID) *model.ServiceAccount {
	t.Helper()
	var row model.ServiceAccount
	require.NoError(t, h.db.Table(h.table(row.TableName())).Where("principal_id = ?", id).Take(&row).Error)
	return &row
}

func (h *identityHarness) credentialRow(t *testing.T, id guid.ID) *model.ServiceAccountCredential {
	t.Helper()
	var row model.ServiceAccountCredential
	require.NoError(t, h.db.Table(h.table(row.TableName())).Where("id = ?", id).Take(&row).Error)
	return &row
}

func TestCreateServiceAccountWritesIdentityAccountAndMembership(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	expiresAt := identityTestNow.Add(24 * time.Hour)

	account, err := harness.service.CreateServiceAccount(t.Context(), 7, " Robot ", " Robot One ", " does work ", &expiresAt)
	require.NoError(t, err)

	assert.Equal(t, guid.ID(1001), account.ID)
	assert.Equal(t, "robot", account.LoginName)
	assert.Equal(t, "Robot One", account.DisplayName)
	assert.Equal(t, "does work", account.Description)
	assert.Equal(t, "active", account.Status)
	assert.Equal(t, int64(1), account.Version)
	require.NotNil(t, account.ExpiresAt)
	assert.True(t, expiresAt.Equal(*account.ExpiresAt))

	row := harness.serviceAccountRow(t, account.ID)
	assert.Equal(t, guid.ID(7), row.OwnerTenantID)
	assert.Equal(t, int64(1), row.TokenVersion)
	assert.Equal(t, "robot", harness.principal(t, account.ID).LoginName)
	assert.Equal(t, "active", harness.member(t, 7, account.ID).Status)
	assert.Equal(t, int64(1), harness.revision(t, 7))

	events := harness.events()
	require.Len(t, events, 1)
	assert.Equal(t, "service_account_created", events[0].EventType)
}

func TestCreateServiceAccountRejectsInvalidInputAndInactiveTenant(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	harness.seedTenant(t, 9, "suspended")
	past := identityTestNow.Add(-time.Hour)

	for _, testCase := range []struct {
		name        string
		tenantID    guid.ID
		loginName   string
		displayName string
		description string
		expiresAt   *time.Time
	}{
		{"zero tenant", 0, "robot", "Robot", "", nil},
		{"blank login", 7, "   ", "Robot", "", nil},
		{"long login", 7, strings.Repeat("r", 201), "Robot", "", nil},
		{"long display", 7, "robot", strings.Repeat("d", 129), "", nil},
		{"long description", 7, "robot", "Robot", strings.Repeat("x", 1001), nil},
		{"expired", 7, "robot", "Robot", "", &past},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := harness.service.CreateServiceAccount(
				t.Context(), testCase.tenantID, testCase.loginName,
				testCase.displayName, testCase.description, testCase.expiresAt,
			)
			assert.ErrorIs(t, err, ErrServiceAccountInvalid)
		})
	}

	_, err := harness.service.CreateServiceAccount(t.Context(), 9, "robot", "Robot", "", nil)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)
	_, err = harness.service.CreateServiceAccount(t.Context(), 404, "robot", "Robot", "", nil)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)
	assert.Zero(t, harness.principalCount(t))
}

func TestCreateServiceAccountRejectsDuplicateLogin(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	_, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)

	_, err = harness.service.CreateServiceAccount(t.Context(), 8, "robot", "Robot", "", nil)
	assert.ErrorIs(t, err, ErrLoginConflict)
	assert.Equal(t, int64(1), harness.principalCount(t))
}

func TestListServiceAccountsIsTenantScopedAndSearchable(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	for _, login := range []string{"alpha", "beta"} {
		_, err := harness.service.CreateServiceAccount(t.Context(), 7, login, login, "worker "+login, nil)
		require.NoError(t, err)
	}
	other, err := harness.service.CreateServiceAccount(t.Context(), 8, "gamma", "gamma", "", nil)
	require.NoError(t, err)

	items, total, err := harness.service.ListServiceAccounts(t.Context(), 7, "", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 2)
	assert.Equal(t, []string{"alpha", "beta"}, []string{items[0].LoginName, items[1].LoginName})

	items, total, err = harness.service.ListServiceAccounts(t.Context(), 7, "WORKER BETA", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "beta", items[0].LoginName)

	items, total, err = harness.service.ListServiceAccounts(t.Context(), 7, "", 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, items, 1)
	assert.Equal(t, "beta", items[0].LoginName)

	_, err = harness.service.GetServiceAccount(t.Context(), 7, other.ID)
	assert.ErrorIs(t, err, ErrServiceAccountNotFound)

	for _, invalid := range []struct{ limit, offset int }{{0, 0}, {201, 0}, {10, -1}} {
		_, _, err := harness.service.ListServiceAccounts(t.Context(), 7, "", invalid.limit, invalid.offset)
		assert.ErrorIs(t, err, ErrServiceAccountInvalid)
	}
	_, _, err = harness.service.ListServiceAccounts(t.Context(), 0, "", 10, 0)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)
}

func TestGetServiceAccountRejectsZeroIdentifiers(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "")
	_, err := harness.service.GetServiceAccount(t.Context(), 0, 10)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)
	_, err = harness.service.GetServiceAccount(t.Context(), 7, 0)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)
	_, err = harness.service.GetServiceAccount(t.Context(), 7, 404)
	assert.ErrorIs(t, err, ErrServiceAccountNotFound)
}

func TestReplaceServiceAccountAppliesOptimisticVersion(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "old", nil)
	require.NoError(t, err)

	_, err = harness.service.ReplaceServiceAccount(t.Context(), 7, account.ID, "Robot", "new", "active", nil, 99)
	assert.ErrorIs(t, err, ErrServiceAccountConflict)

	updated, err := harness.service.ReplaceServiceAccount(
		t.Context(), 7, account.ID, " Robot Two ", " new ", "active", nil, 1,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)
	assert.Equal(t, "new", updated.Description)
	assert.Equal(t, "Robot Two", updated.DisplayName)
	assert.Equal(t, "Robot Two", harness.principal(t, account.ID).DisplayName)
	assert.Equal(t, "Robot Two", harness.member(t, 7, account.ID).DisplayName)
	assert.Equal(t, int64(1), harness.serviceAccountRow(t, account.ID).TokenVersion,
		"an unchanged status and expiry must not invalidate issued tokens")
	assert.Equal(t, int64(1), harness.revision(t, 7))
	assert.Empty(t, harness.guard.protectedTenants())
}

func TestReplaceServiceAccountDisableProtectsAdministratorsAndTokens(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)

	updated, err := harness.service.ReplaceServiceAccount(
		t.Context(), 7, account.ID, "Robot", "", "disabled", nil, 1,
	)
	require.NoError(t, err)

	assert.Equal(t, "disabled", updated.Status)
	assert.Equal(t, []guid.ID{7}, harness.guard.protectedTenants())
	assert.Equal(t, 1, harness.guard.verifiedCount())
	assert.Equal(t, int64(2), harness.serviceAccountRow(t, account.ID).TokenVersion)
	assert.Equal(t, "disabled", harness.principal(t, account.ID).Status)
	assert.NotNil(t, harness.principal(t, account.ID).DisabledAt)
	assert.Equal(t, int64(2), harness.revision(t, 7))

	events := harness.events()
	require.Len(t, events, 2)
	assert.Equal(t, "service_account_updated", events[1].EventType)
	assert.Equal(t, true, events[1].Detail["status_changed"])
}

func TestReplaceServiceAccountRollsBackWhenGuardRejects(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	harness.guard.verifyErr = errors.New("last tenant administrator")

	_, err = harness.service.ReplaceServiceAccount(t.Context(), 7, account.ID, "Robot", "", "disabled", nil, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last tenant administrator")

	row := harness.serviceAccountRow(t, account.ID)
	assert.Equal(t, "active", row.Status)
	assert.Equal(t, int64(1), row.Version)
	assert.Equal(t, int64(1), row.TokenVersion)
	assert.Equal(t, "active", harness.principal(t, account.ID).Status)
}

func TestReplaceServiceAccountExpiryChangeInvalidatesTokens(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	expiresAt := identityTestNow.Add(48 * time.Hour)

	updated, err := harness.service.ReplaceServiceAccount(
		t.Context(), 7, account.ID, "Robot", "", "active", &expiresAt, 1,
	)
	require.NoError(t, err)
	require.NotNil(t, updated.ExpiresAt)
	assert.Equal(t, int64(2), harness.serviceAccountRow(t, account.ID).TokenVersion)

	// Replaying the same expiry leaves the token version untouched.
	same := expiresAt
	_, err = harness.service.ReplaceServiceAccount(t.Context(), 7, account.ID, "Robot", "", "active", &same, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(2), harness.serviceAccountRow(t, account.ID).TokenVersion)
}

func TestReplaceServiceAccountRejectsInvalidInputAndForeignTenant(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)

	for _, testCase := range []struct {
		name        string
		displayName string
		status      string
		version     int64
	}{
		{"blank display", "  ", "active", 1},
		{"unknown status", "Robot", "archived", 1},
		{"zero version", "Robot", "active", 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := harness.service.ReplaceServiceAccount(
				t.Context(), 7, account.ID, testCase.displayName, "", testCase.status, nil, testCase.version,
			)
			assert.ErrorIs(t, err, ErrServiceAccountInvalid)
		})
	}
	_, err = harness.service.ReplaceServiceAccount(t.Context(), 7, 0, "Robot", "", "active", nil, 1)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)

	_, err = harness.service.ReplaceServiceAccount(t.Context(), 8, account.ID, "Hijacked", "", "disabled", nil, 1)
	assert.ErrorIs(t, err, ErrServiceAccountNotFound)
	assert.Equal(t, "Robot", harness.principal(t, account.ID).DisplayName)
	assert.Equal(t, "active", harness.serviceAccountRow(t, account.ID).Status)
}

func TestDeleteServiceAccountSoftDeletesAndRevokesCredentials(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	secret, err := harness.service.CreateServiceAccountCredential(
		t.Context(), 7, account.ID, "ci", []string{"read"}, nil,
	)
	require.NoError(t, err)

	require.NoError(t, harness.service.DeleteServiceAccount(t.Context(), 7, account.ID, 1))

	row := harness.serviceAccountRow(t, account.ID)
	assert.Equal(t, "deleted", row.Status)
	assert.NotNil(t, row.DeletedAt)
	assert.Equal(t, int64(2), row.Version)
	assert.Equal(t, int64(2), row.TokenVersion)
	assert.Equal(t, "disabled", harness.principal(t, account.ID).Status)
	assert.Equal(t, "disabled", harness.member(t, 7, account.ID).Status)
	assert.NotNil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)
	assert.Equal(t, []guid.ID{7}, harness.guard.protectedTenants())

	_, err = harness.service.GetServiceAccount(t.Context(), 7, account.ID)
	assert.ErrorIs(t, err, ErrServiceAccountNotFound)
	assert.Equal(t, "service_account_deleted", harness.eventTypes()[len(harness.eventTypes())-1])
}

func TestDeleteServiceAccountValidatesVersionTenantAndGuard(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)

	assert.ErrorIs(t, harness.service.DeleteServiceAccount(t.Context(), 0, account.ID, 1), ErrServiceAccountInvalid)
	assert.ErrorIs(t, harness.service.DeleteServiceAccount(t.Context(), 7, 0, 1), ErrServiceAccountInvalid)
	assert.ErrorIs(t, harness.service.DeleteServiceAccount(t.Context(), 7, account.ID, 0), ErrServiceAccountInvalid)
	assert.ErrorIs(t, harness.service.DeleteServiceAccount(t.Context(), 7, account.ID, 5), ErrServiceAccountConflict)
	assert.ErrorIs(t, harness.service.DeleteServiceAccount(t.Context(), 8, account.ID, 1), ErrServiceAccountNotFound)
	assert.Equal(t, "active", harness.serviceAccountRow(t, account.ID).Status)

	harness.guard.verifyErr = errors.New("last tenant administrator")
	err = harness.service.DeleteServiceAccount(t.Context(), 7, account.ID, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last tenant administrator")
	assert.Equal(t, "active", harness.serviceAccountRow(t, account.ID).Status)
	assert.Equal(t, "active", harness.principal(t, account.ID).Status)
}

func TestCreateServiceAccountCredentialIssuesVerifiableSecret(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	expiresAt := identityTestNow.Add(time.Hour)

	secret, err := harness.service.CreateServiceAccountCredential(
		t.Context(), 7, account.ID, " ci ", []string{"write", "read", "read"}, &expiresAt,
	)
	require.NoError(t, err)

	assert.Equal(t, "ci", secret.Credential.Name)
	assert.Equal(t, []string{"read", "write"}, secret.Credential.Scopes)
	assert.Equal(t, account.ID, secret.Credential.ServiceAccountID)
	assert.NotEmpty(t, secret.Secret)
	row := harness.credentialRow(t, secret.Credential.ID)
	assert.NotContains(t, row.SecretHash, secret.Secret)
	ok, err := passwordx.Verify(row.SecretHash, secret.Secret)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "service_account_credential_created", harness.eventTypes()[1])
}

func TestCreateServiceAccountCredentialRejectsInvalidScopesAndExpiry(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	accountExpiry := identityTestNow.Add(time.Hour)
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", &accountExpiry)
	require.NoError(t, err)
	past := identityTestNow.Add(-time.Minute)
	beyondAccount := accountExpiry.Add(time.Minute)

	for _, testCase := range []struct {
		name      string
		tenantID  guid.ID
		id        guid.ID
		credName  string
		scopes    []string
		expiresAt *time.Time
	}{
		{"zero tenant", 0, account.ID, "ci", []string{"read"}, nil},
		{"zero account", 7, 0, "ci", []string{"read"}, nil},
		{"blank name", 7, account.ID, "  ", []string{"read"}, nil},
		{"long name", 7, account.ID, strings.Repeat("n", 129), []string{"read"}, nil},
		{"no scopes", 7, account.ID, "ci", nil, nil},
		{"blank scope", 7, account.ID, "ci", []string{" "}, nil},
		{"spaced scope", 7, account.ID, "ci", []string{"read write"}, nil},
		{"long scope", 7, account.ID, "ci", []string{strings.Repeat("s", 129)}, nil},
		{"expired credential", 7, account.ID, "ci", []string{"read"}, &past},
		{"outlives account", 7, account.ID, "ci", []string{"read"}, &beyondAccount},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := harness.service.CreateServiceAccountCredential(
				t.Context(), testCase.tenantID, testCase.id, testCase.credName, testCase.scopes, testCase.expiresAt,
			)
			assert.ErrorIs(t, err, ErrServiceAccountInvalid)
		})
	}

	tooMany := make([]string, 129)
	for index := range tooMany {
		tooMany[index] = "scope"
	}
	_, err = harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", tooMany, nil)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)
}

func TestCreateServiceAccountCredentialRejectsDisabledOrExpiredAccount(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	_, err = harness.service.ReplaceServiceAccount(t.Context(), 7, account.ID, "Robot", "", "disabled", nil, 1)
	require.NoError(t, err)

	_, err = harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", []string{"read"}, nil)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)

	expired := identityTestNow.Add(-time.Hour)
	require.NoError(t, harness.db.Table(harness.table(model.ServiceAccount{}.TableName())).
		Where("principal_id = ?", account.ID).
		Updates(map[string]any{"status": "active", "expires_at": expired}).Error)
	_, err = harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", []string{"read"}, nil)
	assert.ErrorIs(t, err, ErrServiceAccountInvalid)
}

func TestCreateServiceAccountCredentialEnforcesActiveLimit(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	for index := range maxServiceAccountCredentials {
		_, err := harness.service.CreateServiceAccountCredential(
			t.Context(), 7, account.ID, "ci", []string{"read"}, nil,
		)
		require.NoError(t, err, "credential %d must be accepted", index)
	}

	_, err = harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", []string{"read"}, nil)
	assert.ErrorIs(t, err, ErrCredentialLimit)

	// Revoked and expired credentials free a slot again.
	credentials, err := harness.service.ListServiceAccountCredentials(t.Context(), 7, account.ID)
	require.NoError(t, err)
	require.NoError(t, harness.service.RevokeServiceAccountCredential(
		t.Context(), 7, account.ID, credentials[0].ID,
	))
	_, err = harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", []string{"read"}, nil)
	require.NoError(t, err)
}

func TestListServiceAccountCredentialsIsTenantScoped(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	for _, name := range []string{"first", "second"} {
		_, err := harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, name, []string{"read"}, nil)
		require.NoError(t, err)
	}

	credentials, err := harness.service.ListServiceAccountCredentials(t.Context(), 7, account.ID)
	require.NoError(t, err)
	require.Len(t, credentials, 2)
	assert.Empty(t, credentials[0].RevokedAt)

	_, err = harness.service.ListServiceAccountCredentials(t.Context(), 8, account.ID)
	assert.ErrorIs(t, err, ErrServiceAccountNotFound)
}

func TestRevokeServiceAccountCredentialIsIdempotentAndScoped(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	secret, err := harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", []string{"read"}, nil)
	require.NoError(t, err)

	assert.ErrorIs(t, harness.service.RevokeServiceAccountCredential(t.Context(), 0, account.ID, secret.Credential.ID), ErrServiceAccountInvalid)
	assert.ErrorIs(t, harness.service.RevokeServiceAccountCredential(t.Context(), 7, account.ID, 0), ErrServiceAccountInvalid)
	assert.ErrorIs(t, harness.service.RevokeServiceAccountCredential(t.Context(), 8, account.ID, secret.Credential.ID), ErrServiceAccountNotFound)
	assert.ErrorIs(t, harness.service.RevokeServiceAccountCredential(t.Context(), 7, account.ID, 4040), ErrCredentialNotFound)
	assert.Nil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)

	require.NoError(t, harness.service.RevokeServiceAccountCredential(t.Context(), 7, account.ID, secret.Credential.ID))
	assert.NotNil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)
	assert.Equal(t, int64(2), harness.serviceAccountRow(t, account.ID).TokenVersion)
	assert.Equal(t, "service_account_credential_revoked", harness.eventTypes()[2])

	// A second revoke changes nothing: no token-version bump and no new event.
	require.NoError(t, harness.service.RevokeServiceAccountCredential(t.Context(), 7, account.ID, secret.Credential.ID))
	assert.Equal(t, int64(2), harness.serviceAccountRow(t, account.ID).TokenVersion)
	assert.Len(t, harness.events(), 3)
}

func TestNormalizeServiceAccountScopesSortsAndDeduplicates(t *testing.T) {
	t.Parallel()

	scopes, ok := normalizeServiceAccountScopes([]string{" write ", "read", "write"})
	require.True(t, ok)
	assert.Equal(t, []string{"read", "write"}, scopes)

	_, ok = normalizeServiceAccountScopes(nil)
	assert.False(t, ok)
	_, ok = normalizeServiceAccountScopes([]string{"with\tspace"})
	assert.False(t, ok)
}

func TestSameOptionalTimeComparesInstants(t *testing.T) {
	t.Parallel()

	first := identityTestNow
	second := identityTestNow.In(time.FixedZone("UTC+8", 8*60*60))
	assert.True(t, sameOptionalTime(nil, nil))
	assert.False(t, sameOptionalTime(&first, nil))
	assert.False(t, sameOptionalTime(nil, &first))
	assert.True(t, sameOptionalTime(&first, &second))
	assert.Nil(t, identityOptionalTime(nil))
}

func TestRandomServiceAccountSecretIsUnique(t *testing.T) {
	t.Parallel()

	first, err := randomServiceAccountSecret()
	require.NoError(t, err)
	second, err := randomServiceAccountSecret()
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.GreaterOrEqual(t, len(first), 43)
}

func TestServiceAccountWorkflowsRollBackOnAuditFailure(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	secret, err := harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", []string{"read"}, nil)
	require.NoError(t, err)
	harness.auditErr = errors.New("audit sink down")

	err = harness.service.DeleteServiceAccount(t.Context(), 7, account.ID, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit sink down")
	assert.Equal(t, "active", harness.serviceAccountRow(t, account.ID).Status)
	assert.Nil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)

	err = harness.service.RevokeServiceAccountCredential(t.Context(), 7, account.ID, secret.Credential.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit sink down")
	assert.Nil(t, harness.credentialRow(t, secret.Credential.ID).RevokedAt)
	assert.Equal(t, int64(1), harness.serviceAccountRow(t, account.ID).TokenVersion)

	_, err = harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "second", []string{"read"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit sink down")
	credentials, err := harness.service.ListServiceAccountCredentials(t.Context(), 7, account.ID)
	require.NoError(t, err)
	assert.Len(t, credentials, 1)
}

func TestServiceAccountCreationSurfacesIDAndMembershipFailures(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	harness.repository.ids = failingIDSource{}
	_, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	assert.EqualError(t, err, "id source down")

	harness.repository.ids = newIdentityTestIDs(8000)
	require.NoError(t, harness.db.Exec("DROP TABLE "+harness.table("iam_tenant_members")).Error)
	_, err = harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert tenant membership")
	assert.Zero(t, harness.principalCount(t))
}

func TestCreateServiceAccountCredentialSurfacesIDFailure(t *testing.T) {
	t.Parallel()

	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	harness.repository.ids = failingIDSource{}

	_, err = harness.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "ci", []string{"read"}, nil)
	assert.EqualError(t, err, "id source down")
}

// TestServiceAccountValidationSQLIsFrozen pins the statement behind
// ValidateServiceAccount. Its query plan is one of the four fingerprints in
// internal/app/testdata/iam_performance_baseline.json (path scim_bulk), and
// that comparison needs an external MySQL or PostgreSQL instance, which
// `make ci` does not provide. Freezing the text here makes a drift fail
// `go test ./...` instead of surfacing only in the external run -- which also
// closes the door on widening the capability's return type, because selecting
// columns instead of count(*) would change this string.
func TestServiceAccountValidationSQLIsFrozen(t *testing.T) {
	const frozen = `SELECT count(*) FROM sl_iam_service_accounts AS service_account ` +
		`JOIN sl_iam_principals AS principal ON principal.id = service_account.principal_id ` +
		`JOIN sl_iam_tenant_members AS member ON member.principal_id = principal.id ` +
		`AND member.tenant_id = service_account.owner_tenant_id ` +
		`JOIN sl_iam_tenants AS tenant ON tenant.id = service_account.owner_tenant_id ` +
		`WHERE (service_account.owner_tenant_id = $1 AND service_account.principal_id = $2) ` +
		`AND (service_account.status = 'active' AND service_account.deleted_at IS NULL ` +
		`AND (service_account.expires_at IS NULL OR service_account.expires_at > $3)) ` +
		`AND (principal.status = 'active' AND principal.activation_required = $4 ` +
		`AND member.status = 'active' AND tenant.status = 'active')`

	harness := newIdentityHarness(t, "sl_")
	connection, err := harness.db.DB()
	require.NoError(t, err)
	// The real PostgreSQL dialect with DryRun: the text is what a deployment
	// sends, not what the SQLite fixture happens to accept.
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{
		DryRun: true, SkipDefaultTransaction: true, DisableAutomaticPing: true,
		NowFunc: func() time.Time { return identityTestNow },
		Logger:  logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	var recorded []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("freeze_service_account", func(tx *gorm.DB) {
		recorded = append(recorded, tx.Statement.SQL.String())
	}))

	_, _ = harness.service.ValidateServiceAccount(t.Context(), db, 10, 2, identityTestNow)
	assert.Equal(t, []string{frozen}, recorded)
}

// TestServiceAccountOperationsFailClosedAfterDatabaseShutdown proves that a
// storage outage is reported by every service-account entry point instead of
// being flattened into an empty list, a missing account, or a silent success
// that a caller would read as "there is nothing here".
func TestServiceAccountOperationsFailClosedAfterDatabaseShutdown(t *testing.T) {
	harness := newServiceAccountHarness(t, "sl_")
	account, err := harness.service.CreateServiceAccount(t.Context(), 7, "robot", "Robot", "", nil)
	require.NoError(t, err)
	secret, err := harness.service.CreateServiceAccountCredential(
		t.Context(), 7, account.ID, "ci", []string{"read"}, nil,
	)
	require.NoError(t, err)
	connection, err := harness.db.DB()
	require.NoError(t, err)
	require.NoError(t, connection.Close())

	ctx := t.Context()
	for _, check := range []struct {
		name string
		call func() error
	}{
		{"validate", func() error {
			_, err := harness.service.ValidateServiceAccount(ctx, harness.db, 7, account.ID, identityTestNow)
			return err
		}},
		{"create", func() error {
			_, err := harness.service.CreateServiceAccount(ctx, 7, "second", "Second", "", nil)
			return err
		}},
		{"list", func() error {
			_, _, err := harness.service.ListServiceAccounts(ctx, 7, "", 10, 0)
			return err
		}},
		{"get", func() error { _, err := harness.service.GetServiceAccount(ctx, 7, account.ID); return err }},
		{"replace", func() error {
			_, err := harness.service.ReplaceServiceAccount(ctx, 7, account.ID, "Robot", "Robot", "active", nil, 1)
			return err
		}},
		{"delete", func() error { return harness.service.DeleteServiceAccount(ctx, 7, account.ID, 1) }},
		{"create credential", func() error {
			_, err := harness.service.CreateServiceAccountCredential(ctx, 7, account.ID, "second", []string{"read"}, nil)
			return err
		}},
		{"list credentials", func() error {
			_, err := harness.service.ListServiceAccountCredentials(ctx, 7, account.ID)
			return err
		}},
		{"revoke credential", func() error {
			return harness.service.RevokeServiceAccountCredential(ctx, 7, account.ID, secret.Credential.ID)
		}},
		{"resolve owner", func() error {
			_, _, err := harness.service.resolveServiceAccountOwner(ctx, account.ID)
			return err
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			require.Error(t, check.call(), "a closed database must not read as an answer")
		})
	}

	// The read paths must not report a usable account either.
	valid, err := harness.service.ValidateServiceAccount(ctx, harness.db, 7, account.ID, identityTestNow)
	require.Error(t, err)
	assert.False(t, valid, "an unreachable database never validates a credential")
	accounts, total, err := harness.service.ListServiceAccounts(ctx, 7, "", 10, 0)
	require.Error(t, err)
	assert.Empty(t, accounts)
	assert.Zero(t, total)
	_, found, err := harness.service.resolveServiceAccountOwner(ctx, account.ID)
	require.Error(t, err)
	assert.False(t, found, "an unreachable database is not an absent owner")
}
