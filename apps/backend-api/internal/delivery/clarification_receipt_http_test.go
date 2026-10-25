package delivery

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestClarificationReceiptHTTPReplayReauthorizesRequester(t *testing.T) {
	m, _, ctx, _ := workHTTPHarness(t)
	allowed := true
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: allowed}, nil
	}
	requester := guid.ID(11)
	m.clarificationReplySource = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, lock bool) (ClarificationReplyFact, bool, error) {
		require.True(t, lock)
		return ClarificationReplyFact{ProjectID: 99, RequesterID: requester, ReplyVersion: 1}, true, nil
	}
	in := &clarificationHandledInput{EntityID: "99", ClarificationID: "81", Key: "reply-handled-once"}
	in.Body.ReplyVersion = 1
	first, err := m.handleClarificationHTTP(ctx, in)
	require.NoError(t, err)
	replay, err := m.handleClarificationHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	requester = 12
	_, err = m.handleClarificationHTTP(ctx, in)
	assertWorkStatus(t, err, 403)
	requester, allowed = 11, false
	_, err = m.handleClarificationHTTP(ctx, in)
	require.Error(t, err)
}

func TestClarificationReceiptHTTPBindingAndContract(t *testing.T) {
	m, _, ctx, _ := workHTTPHarness(t)
	registry, err := authz.NewRegistry(authz.Action{Resource: "intake", Verb: "read", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, err)
	guard := resultHTTPGuard{ctx: ctx}
	m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	m.entityOwner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 99}, true, nil
	}
	_, api := humatest.New(t)
	m.registerClarificationReceiptHTTP(api)
	op := api.OpenAPI().Paths[clarificationHandledPath].Post
	require.Equal(t, "intake_read", op.Extensions[authz.GuardExtensionKey])
	for _, body := range []string{`{"reply_version":0}`, `{"reply_version":"one"}`} {
		response := api.Post("/entities/99/project/clarifications/81/handled", "Idempotency-Key: bind", strings.NewReader(body))
		require.Equal(t, 422, response.Code, response.Body.String())
	}
	response := api.Post("/entities/99/project/clarifications/81/handled", strings.NewReader(`{"reply_version":1}`))
	require.Equal(t, 422, response.Code, response.Body.String())
}
