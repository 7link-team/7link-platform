package authn

import (
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestScopeChangeReauthenticationIsBoundSingleUseAndTransactional(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationScopeChange, "requirement:123", "password", "")
	require.NoError(t, err)
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
	consume := func(target string, rollback bool) error {
		return harness.db.Transaction(func(tx *gorm.DB) error {
			if err := service.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, options.Proof, ReauthenticationScopeChange, target); err != nil {
				return err
			}
			if rollback {
				return errors.New("domain refused")
			}
			return nil
		})
	}
	require.Error(t, consume("requirement:999", false))
	require.Error(t, consume("requirement:123", true))
	require.NoError(t, consume("requirement:123", false))
	require.Error(t, consume("requirement:123", false))
}
