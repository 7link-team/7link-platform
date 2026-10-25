package authn

import (
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestIntakeProofBindsFreshHumanSessionTargetAndRollback(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	RegisterIntakeReauthentication(api)
	RegisterIntakeReauthentication(api)
	response := api.Post("/principals/"+httpPrincipalID(t, s, cookie)+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", strings.NewReader(`{"purpose":"intake_convert_privileged","target":"intake:50","method":"password"}`))
	require.Equal(t, 201, response.Code)
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	proof, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationIntakeConvert, "intake:50", "password", "")
	require.NoError(t, err)
	consume := func(target, session string, rollback bool) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if err := s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, session, proof.Proof, ReauthenticationIntakeConvert, target); err != nil {
				return err
			}
			if rollback {
				return errors.New("project creation failed")
			}
			return nil
		})
	}
	require.Error(t, consume("intake:50", cookie, false))
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, proof.Proof, "correct horse battery staple", "", "", nil))
	require.Error(t, consume("intake:51", cookie, false))
	require.Error(t, consume("intake:50", "", false))
	require.Error(t, consume("intake:50", cookie, true))
	require.NoError(t, consume("intake:50", cookie, false))
	require.Error(t, consume("intake:50", cookie, false))
}
