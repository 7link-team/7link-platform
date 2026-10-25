package authn

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGovernanceProofBindingAndSingleUse(t *testing.T) {
	for _, purpose := range []string{ReauthenticationDelegationGrant, ReauthenticationSpawnGrantManage} {
		t.Run(purpose, func(t *testing.T) {
			s, h, cookie := authenticatedLocalService(t)
			claims, err := s.Authenticate(t.Context(), "", cookie)
			require.NoError(t, err)
			const target = "project:99:grantee:3"
			proof, err := s.BeginReauthentication(t.Context(), cookie, purpose, target, "password", "")
			require.NoError(t, err)
			consume := func(usePurpose, useTarget, useCookie string) error {
				return h.db.Transaction(func(tx *gorm.DB) error {
					return s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, useCookie, proof.Proof, usePurpose, useTarget)
				})
			}
			require.Error(t, consume(purpose, target, cookie), "fresh verification required")
			require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, proof.Proof, "correct horse battery staple", "", "", nil))
			require.Error(t, consume(ReauthenticationScopeChange, target, cookie))
			require.Error(t, consume(purpose, target+"0", cookie))
			require.Error(t, consume(purpose, target, ""))
			require.NoError(t, consume(purpose, target, cookie))
			require.Error(t, consume(purpose, target, cookie), "single use")
		})
	}
}

func TestGovernanceChallengeHTTPUsesCanonicalValidation(t *testing.T) {
	s, _, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	RegisterGovernanceReauthentication(api)
	RegisterGovernanceReauthentication(api)
	for _, purpose := range []string{ReauthenticationDelegationGrant, ReauthenticationSpawnGrantManage, "unregistered-purpose"} {
		body, err := json.Marshal(map[string]string{"purpose": purpose, "target": "agent:33", "method": "password"})
		require.NoError(t, err)
		w := api.Post("/principals/"+httpPrincipalID(t, s, cookie)+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", strings.NewReader(string(body)))
		if purpose == "unregistered-purpose" {
			require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
		} else {
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		}
	}
}
