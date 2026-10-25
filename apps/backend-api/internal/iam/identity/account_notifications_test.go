package identity

import (
	"7link.com/platform/internal/iam/identity/model"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestMachineAccountDisableIsAtomicAndInvalidatesTokens(t *testing.T) {
	for _, prefix := range []string{"plain_", "isolated_"} {
		t.Run(prefix, func(t *testing.T) {
			h := newServiceAccountHarness(t, prefix)
			account, err := h.service.CreateServiceAccount(t.Context(), 7, "machine-test", "Machine", "", nil)
			require.NoError(t, err)
			credential, err := h.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "runner", []string{"openid"}, nil)
			require.NoError(t, err)
			rollback := errors.New("domain rollback")
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				require.NoError(t, h.service.disableServiceAccountOn(t.Context(), tx, 7, account.ID))
				return rollback
			}), rollback)
			active, err := h.service.ValidateServiceAccount(t.Context(), h.db, 7, account.ID, identityTestNow)
			require.NoError(t, err)
			require.True(t, active)
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.service.disableServiceAccountOn(t.Context(), tx, 8, account.ID) }), ErrServiceAccountNotFound)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.service.disableServiceAccountOn(t.Context(), tx, 7, account.ID) }))
			active, err = h.service.ValidateServiceAccount(t.Context(), h.db, 7, account.ID, identityTestNow)
			require.NoError(t, err)
			require.False(t, active)
			var row model.ServiceAccount
			require.NoError(t, h.db.Table(h.table("iam_service_accounts")).Where("principal_id = ?", account.ID).Take(&row).Error)
			require.EqualValues(t, 2, row.TokenVersion)
			var c model.ServiceAccountCredential
			require.NoError(t, h.db.Table(h.table("iam_service_account_credentials")).Where("id = ?", credential.Credential.ID).Take(&c).Error)
			require.NotNil(t, c.RevokedAt)
			require.ErrorIs(t, h.service.disableServiceAccountOn(t.Context(), h.db, 7, account.ID), ErrServiceAccountInvalid)
		})
	}
}

func TestNotificationCandidatePagingFiltersDisabledPrincipals(t *testing.T) {
	h := newServiceAccountHarness(t, "notification_")
	account, err := h.service.Create(t.Context(), 7, "notify-reader", testPassword, "Reader", "")
	require.NoError(t, err)
	m := &Module{Service: h.service}
	ids, err := m.notificationCandidates(t.Context(), h.db, 0, 200)
	require.NoError(t, err)
	require.Contains(t, ids, account.ID)
	page, err := m.notificationCandidates(t.Context(), h.db, account.ID, 1)
	require.NoError(t, err)
	require.LessOrEqual(t, len(page), 1)
	for _, id := range page {
		require.Greater(t, id, account.ID)
	}
	require.NoError(t, h.db.Table(h.table("iam_principals")).Where("id = ?", account.ID).Update("status", "disabled").Error)
	ids, err = m.notificationCandidates(t.Context(), h.db, 0, 200)
	require.NoError(t, err)
	require.NotContains(t, ids, account.ID)
	_, err = m.notificationCandidates(t.Context(), h.db, 0, 201)
	require.Error(t, err)
	_, err = (*Module)(nil).notificationCandidates(t.Context(), nil, 0, 1)
	require.Error(t, err)
}
