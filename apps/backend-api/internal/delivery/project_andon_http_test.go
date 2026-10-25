package delivery

import (
	"context"
	"strings"
	"testing"

	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectAndonHTTPReplayReauthorizesCurrentSafetyAuthority(t *testing.T) {
	m, _, ctx, _ := workHTTPHarness(t)
	m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, guid.ID(11), actor)
		require.Equal(t, "project_read", permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	allowed := true
	m.andonAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: allowed, AuthoritySource: "ROLE:owner"}, nil
	}
	in := &projectAndonInput{EntityID: "99", Key: "project-hold-once", Body: projectAndonBody{Version: 1, Reason: "Investigate project safety anomaly"}}
	first, err := m.projectAndonHTTP(ctx, in, false)
	require.NoError(t, err)
	replay, err := m.projectAndonHTTP(ctx, in, false)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	allowed = false
	_, err = m.projectAndonHTTP(ctx, in, false)
	assertWorkStatus(t, err, 403)
}

func TestProjectAndonHTTPGuardAndRequestBinding(t *testing.T) {
	m, _, ctx, _ := workHTTPHarness(t)
	registry, err := authz.NewRegistry(authz.Action{Resource: "andon", Verb: "pull", Scope: authz.ActionScopeTenant, DataScoped: true}, authz.Action{Resource: "andon", Verb: "clear", Scope: authz.ActionScopeTenant, DataScoped: true})
	require.NoError(t, err)
	guard := resultHTTPGuard{ctx: ctx}
	m.registrar = authz.NewRegistrar(registry, guard, guard, guard)
	m.entityOwner = func(context.Context, guid.ID) (authz.ResourceOwner, bool, error) {
		return authz.ResourceOwner{ScopeType: "tenant", TenantID: 1, EntityID: 99}, true, nil
	}
	_, api := humatest.New(t)
	m.registerProjectAndonHTTP(api)
	for _, path := range []string{projectAndonPath, projectAndonPath + "/clear"} {
		require.NotEmpty(t, api.OpenAPI().Paths[path].Post.Security)
	}
	response := api.Post("/entities/99/project/andon", "Idempotency-Key: bind", strings.NewReader(`{"version":0,"reason":"Investigate source anomaly"}`))
	require.Equal(t, 422, response.Code, response.Body.String())
}
