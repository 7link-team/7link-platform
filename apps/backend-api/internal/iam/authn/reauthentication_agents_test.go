package authn

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentReauthenticationBindingAndLifetime(t *testing.T) {
	for _, purpose := range []string{ReauthenticationAgentFullReset, ReauthenticationAgentToolCredentialWrite} {
		t.Run(purpose, func(t *testing.T) {
			service, harness, cookie := authenticatedLocalService(t)
			claims, err := service.Authenticate(t.Context(), "", cookie)
			require.NoError(t, err)
			target := "tenant:1:agent:7"
			if purpose == ReauthenticationAgentToolCredentialWrite {
				target = "tenant:1:tool:team-docs"
			}
			options, err := service.BeginReauthentication(t.Context(), cookie, purpose, target, "password", "")
			require.NoError(t, err)
			consume := func(proof, usePurpose, useTarget, useCookie string, rollback bool) error {
				return harness.db.Transaction(func(tx *gorm.DB) error {
					if err := service.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, useCookie, proof, usePurpose, useTarget); err != nil {
						return err
					}
					if rollback {
						return errors.New("domain unavailable")
					}
					return nil
				})
			}
			require.Error(t, consume("", purpose, target, cookie, false))
			require.Error(t, consume(options.Proof, purpose, target, cookie, false), "unverified")
			require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
			require.Error(t, consume(options.Proof, ReauthenticationPersonalTokenCreate, target, cookie, false))
			require.Error(t, consume(options.Proof, purpose, target+"-other", cookie, false))
			require.Error(t, consume(options.Proof, purpose, target, "", false))
			require.Error(t, consume(options.Proof, purpose, target, cookie, true))
			require.NoError(t, consume(options.Proof, purpose, target, cookie, false), "rollback preserves fresh proof")
			require.Error(t, consume(options.Proof, purpose, target, cookie, false), "single use")
			expired, err := service.BeginReauthentication(t.Context(), cookie, purpose, target, "password", "")
			require.NoError(t, err)
			require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, expired.Proof, "correct horse battery staple", "", "", nil))
			require.NoError(t, harness.db.Model(&model.ReauthenticationChallenge{}).Where("id_hash = ?", tokenHash(expired.Proof)).Update("expires_at", service.now().Add(-time.Second)).Error)
			require.Error(t, consume(expired.Proof, purpose, target, cookie, false))
		})
	}
}

func TestAgentReauthenticationHTTPAcceptsOnlyRegisteredPurposes(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, service)
	RegisterAgentsReauthentication(api)
	RegisterAgentsReauthentication(api)
	principal := httpPrincipalID(t, service, cookie)
	for _, purpose := range []string{ReauthenticationAgentFullReset, ReauthenticationAgentToolCredentialWrite, "not_registered"} {
		body, err := json.Marshal(map[string]string{"purpose": purpose, "target": "tenant:1:agent:7", "method": "password"})
		require.NoError(t, err)
		response := api.Post("/principals/"+principal+"/reauthentication/challenges", "Cookie: "+cookie,
			"Origin: https://app.example.test", "Content-Type: application/json", bytes.NewReader(body))
		if purpose == "not_registered" {
			require.Equal(t, http.StatusUnprocessableEntity, response.Code)
		} else {
			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		}
	}
}
