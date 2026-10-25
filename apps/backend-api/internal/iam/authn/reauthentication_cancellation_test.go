package authn

import (
	"errors"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectCancellationStepUpRetainsSessionTargetAndSingleUse(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	RegisterCancellationReauthentication(api)
	RegisterCancellationReauthentication(api)
	out := api.Post("/principals/"+httpPrincipalID(t, s, cookie)+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", strings.NewReader(`{"purpose":"project_cancel","target":"project:7","method":"password"}`))
	require.Equal(t, 201, out.Code)
	claims, e := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, e)
	proof, e := s.BeginReauthentication(t.Context(), cookie, ReauthenticationProjectCancel, "project:7", "password", "")
	require.NoError(t, e)
	consume := func(target, c string, rollback bool) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if e := s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, c, proof.Proof, ReauthenticationProjectCancel, target); e != nil {
				return e
			}
			if rollback {
				return errors.New("cancel failed")
			}
			return nil
		})
	}
	require.Error(t, consume("project:7", cookie, false))
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, proof.Proof, "correct horse battery staple", "", "", nil))
	require.Error(t, consume("project:8", cookie, false))
	require.Error(t, consume("project:7", "", false))
	require.Error(t, consume("project:7", cookie, true))
	require.NoError(t, consume("project:7", cookie, false))
	require.Error(t, consume("project:7", cookie, false))
}
