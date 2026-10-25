package authn

import (
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestAcceptanceRecordProofIsSessionTargetBoundSingleUseAndTransactional(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	RegisterAcceptanceReauthentication(api)
	RegisterAcceptanceReauthentication(api)
	require.True(t, validReauthenticationPurpose("acceptance_record"))
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	proof, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationAcceptanceRecord, "package:7", "password", "")
	require.NoError(t, err)
	consume := func(target, c string, rollback bool) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if err := s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, c, proof.Proof, ReauthenticationAcceptanceRecord, target); err != nil {
				return err
			}
			if rollback {
				return errors.New("receipt failure")
			}
			return nil
		})
	}
	require.Error(t, consume("package:7", cookie, false))
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, proof.Proof, "correct horse battery staple", "", "", nil))
	require.Error(t, consume("package:8", cookie, false))
	require.Error(t, consume("package:7", "", false))
	require.Error(t, consume("package:7", cookie, true))
	require.NoError(t, consume("package:7", cookie, false))
	require.Error(t, consume("package:7", cookie, false))
}

func TestDeliverySendProofRequiresVerifiedSessionTargetAndSingleTransactionalConsumption(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	RegisterAcceptanceReauthentication(api)
	require.True(t, validReauthenticationPurpose("delivery_send"))
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	proof, err := s.BeginReauthentication(t.Context(), cookie, "delivery_send", "package:7", "password", "")
	require.NoError(t, err)
	consume := func(target, session string, rollback bool) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if err := s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, session, proof.Proof, "delivery_send", target); err != nil {
				return err
			}
			if rollback {
				return errors.New("outbox failed")
			}
			return nil
		})
	}
	require.Error(t, consume("package:7", cookie, false))
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, proof.Proof, "correct horse battery staple", "", "", nil))
	require.Error(t, consume("package:8", cookie, false))
	require.Error(t, consume("package:7", "", false))
	require.Error(t, consume("package:7", cookie, true))
	require.NoError(t, consume("package:7", cookie, false))
	require.Error(t, consume("package:7", cookie, false))
}
