package identity

import (
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAgentCredentialRotationPreservesAuthorityAndRollsBackWithCaller(t *testing.T) {
	h := newServiceAccountHarness(t, "rb_")
	account, err := h.service.CreateServiceAccount(t.Context(), 7, "agent-rotation", "Agent", "", nil)
	require.NoError(t, err)
	expiry := identityTestNow.Add(24 * time.Hour)
	old, err := h.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "current", []string{"read", "write"}, &expiry)
	require.NoError(t, err)
	other, err := h.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, "other", []string{"read"}, nil)
	require.NoError(t, err)
	before := h.serviceAccountRow(t, account.ID).TokenVersion
	m := &Module{Service: h.service}
	rollback := errors.New("enclosing outbox failure")
	var replacement ServiceAccountCredentialSecret
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		replacement, e = m.rotateAgentCredentialOn(t.Context(), tx, 7, account.ID, &old.Credential.ID)
		require.NoError(t, e)
		return rollback
	}), rollback)
	require.Nil(t, h.credentialRow(t, old.Credential.ID).RevokedAt)
	require.Nil(t, h.credentialRow(t, other.Credential.ID).RevokedAt)
	require.Equal(t, before, h.serviceAccountRow(t, account.ID).TokenVersion)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		replacement, e = m.rotateAgentCredentialOn(t.Context(), tx, 7, account.ID, &old.Credential.ID)
		return e
	}))
	require.NotEmpty(t, replacement.Secret)
	require.NotEqual(t, old.Secret, replacement.Secret)
	require.Equal(t, h.credentialRow(t, old.Credential.ID).Scopes, h.credentialRow(t, replacement.Credential.ID).Scopes)
	require.Equal(t, expiry, *replacement.Credential.ExpiresAt)
	require.NotNil(t, h.credentialRow(t, old.Credential.ID).RevokedAt)
	require.NotNil(t, h.credentialRow(t, other.Credential.ID).RevokedAt)
	require.Equal(t, before+1, h.serviceAccountRow(t, account.ID).TokenVersion)
	require.NotContains(t, h.credentialRow(t, replacement.Credential.ID).SecretHash, replacement.Secret)
}

func TestAgentCredentialRotationRejectsMissingOrAmbiguousScopeSource(t *testing.T) {
	h := newServiceAccountHarness(t, "rb_")
	account, err := h.service.CreateServiceAccount(t.Context(), 7, "agent-source", "Agent", "", nil)
	require.NoError(t, err)
	m := &Module{Service: h.service}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := m.rotateAgentCredentialOn(t.Context(), tx, 7, account.ID, nil)
		return e
	}), ErrCredentialNotFound)
	for _, name := range []string{"first", "second"} {
		_, err = h.service.CreateServiceAccountCredential(t.Context(), 7, account.ID, name, []string{"read"}, nil)
		require.NoError(t, err)
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := m.rotateAgentCredentialOn(t.Context(), tx, 7, account.ID, nil)
		return e
	}), ErrServiceAccountConflict)
	_, err = m.rotateAgentCredentialOn(t.Context(), h.db, 7, account.ID, nil)
	require.Error(t, err)
}
