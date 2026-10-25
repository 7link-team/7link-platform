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

func TestModeProofsRemainSessionPurposeTargetBoundAndSingleUse(t *testing.T) {
	for _, purpose := range []string{ReauthenticationTeamModeChange, ReauthenticationProjectTakeover} {
		t.Run(purpose, func(t *testing.T) {
			service, h, cookie := authenticatedLocalService(t)
			claims, err := service.Authenticate(t.Context(), "", cookie)
			require.NoError(t, err)
			proof, err := service.BeginReauthentication(t.Context(), cookie, purpose, "project:99", "password", "")
			require.NoError(t, err)
			consume := func(usePurpose, target, useCookie string) error {
				return h.db.Transaction(func(tx *gorm.DB) error {
					return service.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, useCookie, proof.Proof, usePurpose, target)
				})
			}
			require.Error(t, consume(purpose, "project:99", cookie))
			require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, proof.Proof, "correct horse battery staple", "", "", nil))
			other := ReauthenticationTeamModeChange
			if purpose == other {
				other = ReauthenticationProjectTakeover
			}
			require.Error(t, consume(other, "project:99", cookie))
			require.Error(t, consume(purpose, "project:100", cookie))
			require.Error(t, consume(purpose, "project:99", ""))
			require.NoError(t, consume(purpose, "project:99", cookie))
			require.Error(t, consume(purpose, "project:99", cookie))
		})
	}
}

func TestModeChallengeHTTPAdvertisesOnlyCanonicalPurposes(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, service)
	RegisterModesReauthentication(api)
	RegisterModesReauthentication(api)
	for _, purpose := range []string{ReauthenticationTeamModeChange, ReauthenticationProjectTakeover, "unknown-mode-purpose"} {
		body, err := json.Marshal(map[string]string{"purpose": purpose, "target": "project:99", "method": "password"})
		require.NoError(t, err)
		response := api.Post("/principals/"+httpPrincipalID(t, service, cookie)+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", strings.NewReader(string(body)))
		expected := http.StatusCreated
		if purpose == "unknown-mode-purpose" {
			expected = http.StatusUnprocessableEntity
		}
		require.Equal(t, expected, response.Code, response.Body.String())
	}
}
