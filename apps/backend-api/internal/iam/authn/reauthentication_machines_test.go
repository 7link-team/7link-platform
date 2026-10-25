package authn

import (
	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReplayRegisteredSecurityCapabilitiesFailClosedBeforeServiceAndUseCurrentSession(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	m := NewModule(h.runtime, authnModuleConfig(localAuthnConfig()))
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	browser, err := module.Resolve[authnext.BrowserSessionSecurity](services)
	require.NoError(t, err)
	federated, err := module.Resolve[FederatedReauthentication](services)
	require.NoError(t, err)
	consumer, err := module.Resolve[ReauthenticationConsumer](services)
	require.NoError(t, err)
	_, err = browser.Authenticate(t.Context(), cookie)
	require.ErrorIs(t, err, authnext.ErrUnavailable)
	require.ErrorIs(t, browser.ValidateCSRF(http.MethodPost, "", cookie), authnext.ErrUnavailable)
	_, err = federated.Inspect(t.Context(), cookie, "proof")
	require.ErrorIs(t, err, authnext.ErrUnavailable)
	_, err = federated.ValidateSessionOn(t.Context(), h.db, 1, cookie)
	require.ErrorIs(t, err, authnext.ErrUnavailable)
	_, err = federated.LocalMethodsOn(t.Context(), h.db, 1)
	require.ErrorIs(t, err, authnext.ErrUnavailable)
	require.ErrorIs(t, federated.VerifyOn(t.Context(), h.db, 1, cookie, "hash", "link", strings.Repeat("a", 64)), authnext.ErrUnavailable)
	require.ErrorIs(t, federated.ConsumeIndependentOn(t.Context(), h.db, 1, cookie, "proof", ReauthenticationSystemEventReplay, "delivery.admin"), authnext.ErrUnavailable)
	require.ErrorIs(t, consumer(t.Context(), h.db, 1, cookie, "proof", ReauthenticationSystemEventReplay, "delivery.admin"), authnext.ErrUnavailable)
	_, err = m.ProjectProfile(t.Context(), nil, "")
	require.ErrorIs(t, err, authnext.ErrUnavailable)
	m.Service = s
	claims, err := browser.Authenticate(t.Context(), cookie)
	require.NoError(t, err)
	require.Positive(t, claims.PrincipalID)
	require.NoError(t, browser.ValidateCSRF(http.MethodGet, "", cookie))
	_, err = m.ProjectProfile(t.Context(), claims, "")
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := federated.ValidateSessionOn(t.Context(), tx, claims.PrincipalID, cookie)
		return err
	}))
	count, err := federated.LocalMethodsOn(t.Context(), h.db, claims.PrincipalID)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	proof, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationSystemEventReplay, "delivery.admin", "federation", "")
	require.NoError(t, err)
	binding, err := federated.Inspect(t.Context(), cookie, proof.Proof)
	require.NoError(t, err)
	require.Equal(t, claims.PrincipalID, binding.PrincipalID)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return federated.VerifyOn(t.Context(), tx, claims.PrincipalID, cookie, binding.ChallengeHash, "trusted-link", strings.Repeat("a", 64))
	}))
	trust, err := module.Resolve[FederatedProofTrustConnector](services)
	require.NoError(t, err)
	require.NoError(t, trust.Bind(func(context.Context, *gorm.DB, guid.ID, string, string) error { return nil }))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return consumer(t.Context(), tx, claims.PrincipalID, cookie, proof.Proof, ReauthenticationSystemEventReplay, "delivery.admin")
	}))
	recovery, err := module.Resolve[RecoveryEligibilityConnector](services)
	require.NoError(t, err)
	require.NoError(t, recovery.Bind(func(context.Context, *gorm.DB, guid.ID) error { return nil }))
	require.NotNil(t, s.recoveryEligibility)
	view, err := module.Resolve[AccountViewAuthorizerConnector](services)
	require.NoError(t, err)
	require.NoError(t, view.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) error { return nil }))
	require.NotNil(t, s.accountViewAuthorizer)
}

func TestReplayFederatedProofRechecksTrustAndRollsBackConsumption(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	p, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationSystemEventReplay, "delivery.admin", "federation", "")
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return s.verifyFederatedReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, tokenHash(p.Proof), "trusted-link", strings.Repeat("a", 64))
	}))
	consume := func(independent bool) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if independent {
				return s.ConsumeIndependentReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, p.Proof, ReauthenticationSystemEventReplay, "delivery.admin")
			}
			return s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, p.Proof, ReauthenticationSystemEventReplay, "delivery.admin")
		})
	}
	require.ErrorIs(t, consume(true), authnext.ErrInvalidCredentials, "upstream identity cannot prove independent trust repair")
	require.ErrorIs(t, consume(false), authnext.ErrUnavailable, "trust checker is required for upstream proofs")
	s.federatedProofTrust = func(_ context.Context, _ *gorm.DB, id guid.ID, link, fingerprint string) error {
		require.Equal(t, claims.PrincipalID, id)
		require.Equal(t, "trusted-link", link)
		require.Equal(t, strings.Repeat("a", 64), fingerprint)
		return errors.New("upstream trust revoked")
	}
	require.ErrorContains(t, consume(false), "upstream trust revoked")
	var row model.ReauthenticationChallenge
	require.NoError(t, h.db.Take(&row, "id_hash = ?", tokenHash(p.Proof)).Error)
	require.Nil(t, row.ConsumedAt)
	original := row.SessionData
	s.federatedProofTrust = func(context.Context, *gorm.DB, guid.ID, string, string) error { return nil }
	for _, plain := range []string{"invalid JSON", `{"link_id":"","fingerprint":""}`} {
		cipher, err := s.encryptAuthnData("federated-proof-source", row.IDHash, []byte(plain))
		require.NoError(t, err)
		require.NoError(t, h.db.Model(&model.ReauthenticationChallenge{}).Where("id_hash = ?", row.IDHash).Update("session_data", cipher).Error)
		require.ErrorIs(t, consume(false), authnext.ErrInvalidCredentials)
	}
	require.NoError(t, h.db.Model(&model.ReauthenticationChallenge{}).Where("id_hash = ?", row.IDHash).Update("session_data", "corrupt ciphertext").Error)
	require.ErrorIs(t, consume(false), authnext.ErrInvalidCredentials)
	require.NoError(t, h.db.Model(&model.ReauthenticationChallenge{}).Where("id_hash = ?", row.IDHash).Update("session_data", original).Error)
	h.auditErr = errors.New("audit offline")
	require.Error(t, consume(false))
	h.auditErr = nil
	require.NoError(t, consume(false))
	require.ErrorIs(t, consume(false), authnext.ErrInvalidCredentials)
}

func TestReplayIndependentProofRequiresLocalMethodAndCurrentSession(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	require.ErrorIs(t, (*Service)(nil).ConsumeIndependentReauthenticationOn(t.Context(), nil, claims.PrincipalID, cookie, "", "", ""), authnext.ErrUnavailable)
	p, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationSystemEventReplay, "delivery.admin", "password", "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, p.Proof, "correct horse battery staple", "", "", nil))
	consume := func(c string, purpose string) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			return s.ConsumeIndependentReauthenticationOn(t.Context(), tx, claims.PrincipalID, c, p.Proof, purpose, "delivery.admin")
		})
	}
	require.ErrorIs(t, consume("", ReauthenticationSystemEventReplay), authnext.ErrInvalidCredentials)
	require.ErrorIs(t, consume(cookie, "unknown_purpose"), authnext.ErrInvalidCredentials)
	require.NoError(t, consume(cookie, ReauthenticationSystemEventReplay))
	require.ErrorIs(t, consume(cookie, ReauthenticationSystemEventReplay), authnext.ErrInvalidCredentials)
}

func TestMachineRevokeProofBindingSingleUseAndRollback(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	p, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationMachineRevoke, "7", "password", "")
	require.NoError(t, err)
	consume := func(purpose, target, c string, rollback bool) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if e := s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, c, p.Proof, purpose, target); e != nil {
				return e
			}
			if rollback {
				return errors.New("domain rollback")
			}
			return nil
		})
	}
	require.Error(t, consume(ReauthenticationMachineRevoke, "7", cookie, false))
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, p.Proof, "correct horse battery staple", "", "", nil))
	require.Error(t, consume(ReauthenticationPrincipalClose, "7", cookie, false))
	require.Error(t, consume(ReauthenticationMachineRevoke, "8", cookie, false))
	require.Error(t, consume(ReauthenticationMachineRevoke, "7", "", false))
	require.Error(t, consume(ReauthenticationMachineRevoke, "7", cookie, true))
	require.NoError(t, consume(ReauthenticationMachineRevoke, "7", cookie, false))
	require.Error(t, consume(ReauthenticationMachineRevoke, "7", cookie, false))
	p, err = s.BeginReauthentication(t.Context(), cookie, ReauthenticationMachineRevoke, "7", "password", "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, p.Proof, "correct horse battery staple", "", "", nil))
	require.NoError(t, h.db.Model(&model.ReauthenticationChallenge{}).Where("id_hash = ?", tokenHash(p.Proof)).Update("expires_at", s.now().Add(-time.Second)).Error)
	require.Error(t, consume(ReauthenticationMachineRevoke, "7", cookie, false))
}

func TestMachineRevokeProofHTTPPurpose(t *testing.T) {
	s, _, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	out := api.Post("/principals/"+httpPrincipalID(t, s, cookie)+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", strings.NewReader(`{"purpose":"machine_revoke","target":"7","method":"password"}`))
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
}

func TestSystemEventReplayProofIsSubscriberBoundAndTransactional(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	p, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationSystemEventReplay, "delivery.admin", "password", "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, p.Proof, "correct horse battery staple", "", "", nil))
	consume := func(target string, rollback bool) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			if e := s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, p.Proof, ReauthenticationSystemEventReplay, target); e != nil {
				return e
			}
			if rollback {
				return errors.New("replay transaction rollback")
			}
			return nil
		})
	}
	require.Error(t, consume("another.subscriber", false))
	require.Error(t, consume("delivery.admin", true))
	require.NoError(t, consume("delivery.admin", false))
	require.Error(t, consume("delivery.admin", false))
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	out := api.Post("/principals/"+httpPrincipalID(t, s, cookie)+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", strings.NewReader(`{"purpose":"system_event_replay","target":"delivery.admin","method":"password"}`))
	require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
}
