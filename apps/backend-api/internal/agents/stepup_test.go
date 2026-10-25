package agents

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// IAM's provider tests exercise real proof issuance/verification/expiry. This
// transport test checks that the domain adapter forwards the exact binding and
// browser cookie in the command transaction, with authorization still enforced.
func TestStepUpHTTPBindsActorCookiePurposeAndSpecificTarget(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "credential", true: "full_reset"}[reset], func(t *testing.T) {
			m, db, handler, _ := agentHTTPHarness(t)
			m.credentialProof = nil
			m.config.EncryptionKey = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
			status := 201
			purpose, target := iamauthn.ReauthenticationAgentToolCredentialWrite, "tenant:1:tool:protected-tool"
			path := "/tenants/1/agent-tools"
			body := map[string]any{"entry_key": "protected-tool", "kind": "MCP_SERVER", "title": "Protected", "transport": "STREAMABLE_HTTP", "endpoint": "https://tools.example.test", "credential": "secret", "content_hash": "", "skill_body": ""}
			if reset {
				status = 202
				purpose, target = iamauthn.ReauthenticationAgentFullReset, "tenant:1:agent:7"
				path = "/tenants/1/agents/7/resets"
				body = map[string]any{"kind": "FULL", "reason": "Clear stale runtime workspace", "confirm_name": "Test agent"}
				require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error { return nil }}))
			}
			services := module.NewServices()
			calls := 0
			require.NoError(t, module.Provide[authn.ReauthenticationConsumer](services, authn.ReauthenticationConsumer(func(ctx context.Context, tx *gorm.DB, actor guid.ID, cookie, proof, gotPurpose, gotTarget string) error {
				calls++
				require.EqualValues(t, 2, actor)
				require.NotSame(t, db, tx)
				require.Equal(t, purpose, gotPurpose)
				require.Equal(t, target, gotTarget)
				if proof != "fresh-bound-proof" || cookie != "session=browser-session" {
					return authn.ErrInvalidCredentials
				}
				return nil
			})))
			require.NoError(t, services.Seal())
			require.NoError(t, m.injectStepUp(services))
			request := func(key, proof, cookie string) *httptest.ResponseRecorder {
				body["reauth_proof"] = proof
				encoded, err := json.Marshal(body)
				require.NoError(t, err)
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded))).WithContext(agentContext(t, 2))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", key)
				req.Header.Set("Cookie", cookie)
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, req)
				return recorder
			}
			require.Equal(t, 403, request("missing", "", "session=browser-session").Code)
			require.Equal(t, 403, request("invalid", "wrong-proof", "session=browser-session").Code)
			require.Equal(t, 403, request("no-cookie", "fresh-bound-proof", "").Code)
			response := request("allowed", "fresh-bound-proof", "session=browser-session")
			require.Equal(t, status, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "secret")
			previous := calls
			response = request("allowed", "", "session=browser-session")
			require.Equal(t, status, response.Code, response.Body.String())
			require.Equal(t, "true", response.Header().Get("Idempotent-Replayed"))
			require.Equal(t, previous, calls, "replay performs no new sensitive write")
		})
	}
}

func TestStepUpBindingRejectsMissingAndDuplicateConsumers(t *testing.T) {
	m, _, services := agentHarness(t)
	require.ErrorIs(t, m.injectStepUp(services), ErrInvalid)
	services = module.NewServices()
	require.Error(t, module.Provide[authn.ReauthenticationConsumer](services, nil))
	require.NoError(t, services.Seal())
	require.Error(t, m.injectStepUp(services))
}
