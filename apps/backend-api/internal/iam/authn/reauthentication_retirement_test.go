package authn

import (
	"bytes"
	"context"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"testing"
	"time"
)

func TestForcedRetirementReauthenticationRequiresFreshPurposeTargetSessionAndOneUse(t *testing.T) {
	service, h, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, service)
	response := api.Post("/principals/"+claims.PrincipalID.String()+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", bytes.NewBufferString(`{"purpose":"agent_forced_retire","target":"tenant:7:agent:202","method":"password"}`))
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	issue := func() ReauthenticationOptions {
		options, e := service.BeginReauthentication(t.Context(), cookie, ReauthenticationAgentForcedRetire, "tenant:7:agent:202", "password", "")
		require.NoError(t, e)
		require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
		return options
	}
	proof := issue()
	consume := func(proof, session, purpose, target string) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			return service.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, session, proof, purpose, target)
		})
	}
	require.Error(t, consume(proof.Proof, cookie, ReauthenticationAgentFullReset, "tenant:7:agent:202"))
	require.Error(t, consume(proof.Proof, cookie, ReauthenticationAgentForcedRetire, "tenant:8:agent:202"))
	require.Error(t, consume(proof.Proof, "", ReauthenticationAgentForcedRetire, "tenant:7:agent:202"))
	require.NoError(t, consume(proof.Proof, cookie, ReauthenticationAgentForcedRetire, "tenant:7:agent:202"))
	require.Error(t, consume(proof.Proof, cookie, ReauthenticationAgentForcedRetire, "tenant:7:agent:202"))
	stale := issue()
	service.now = func() time.Time { return stale.ExpiresAt.Add(time.Millisecond) }
	require.Error(t, consume(stale.Proof, cookie, ReauthenticationAgentForcedRetire, "tenant:7:agent:202"))
}

func TestKnowledgeEmbeddingReauthenticationIsTargetSessionAndSingleUseBound(t *testing.T) {
	service, h, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, service)
	response := api.Post("/principals/"+httpPrincipalID(t, service, cookie)+"/reauthentication/challenges", "Cookie: "+cookie, "Origin: https://app.example.test", "Content-Type: application/json", bytes.NewBufferString(`{"purpose":"knowledge_embedding_write","target":"tenant:1:embedding-config","method":"password"}`))
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationKnowledgeEmbeddingWrite, "tenant:1:embedding-config", "password", "")
	require.NoError(t, err)
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
	consume := func(target, session string) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			return service.ConsumeReauthenticationOn(context.Background(), tx, claims.PrincipalID, session, options.Proof, ReauthenticationKnowledgeEmbeddingWrite, target)
		})
	}
	require.Error(t, consume("tenant:2:embedding-config", cookie))
	require.Error(t, consume("tenant:1:embedding-config", ""))
	require.NoError(t, consume("tenant:1:embedding-config", cookie))
	require.Error(t, consume("tenant:1:embedding-config", cookie))
}
