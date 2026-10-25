package core

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
)

func TestOperationLogsAdapterMapsReaderFailuresWithoutLeakingDetails(t *testing.T) {
	for _, in := range []struct {
		name   string
		err    error
		status int
	}{{"invalid", audit.ErrInvalidCorrelation, 422}, {"storage", errors.New("private database details"), 503}} {
		t.Run(in.name, func(t *testing.T) {
			instance := NewOperationLogs(true)
			require.Equal(t, "operation-logs", instance.ModuleName())
			a := instance.(*operationLogsAdapter)
			s := module.NewServices()
			access := &operationLogAccess{allowed: true}
			registry := authz.MustRegistry(authz.Action{Resource: "system_audit_event", Verb: "view", Scope: authz.ActionScopeSystem})
			registrar := authz.NewRegistrar(registry, access, access, access)
			calls := 0
			require.NoError(t, module.Provide[audit.CorrelatedOperations](s, func(context.Context, string, int, int) ([]audit.OperationSummary, int64, error) {
				calls++
				return nil, 0, in.err
			}))
			require.NoError(t, module.Provide[*authz.Registrar](s, registrar))
			require.NoError(t, s.Seal())
			require.NoError(t, a.InjectServices(s))
			_, api := humatest.New(t, huma.DefaultConfig("test", "1"))
			require.NoError(t, a.RegisterHTTP(t.Context(), api))
			response := api.Get("/system/operation-logs?correlation_id=trace-1", "Authorization: Bearer token")
			require.Equal(t, in.status, response.Code)
			require.NotContains(t, response.Body.String(), "private database details")
			require.Equal(t, 1, calls)
		})
	}
	_, api := humatest.New(t, huma.DefaultConfig("test", "1"))
	require.ErrorIs(t, (&operationLogsAdapter{}).RegisterHTTP(t.Context(), api), audit.ErrDatabaseSinkUnavailable)
}

type operationLogAccess struct{ allowed bool }

func (*operationLogAccess) Authenticate(context.Context, string, string) (*authn.Claims, error) {
	return &authn.Claims{PrincipalID: 10, Subject: "10"}, nil
}
func (*operationLogAccess) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (a *operationLogAccess) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return a.allowed, nil
}
func (a *operationLogAccess) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return a.allowed, nil
}

func TestOperationLogsAdapterRegistersCurrentSystemGuardAndBoundedFilter(t *testing.T) {
	a := &operationLogsAdapter{enabled: true}
	require.True(t, a.Enabled())
	require.Equal(t, []string{audit.Name, Name}, a.ModuleDependencies())
	registry := authz.MustRegistry(authz.Action{Resource: "system_audit_event", Verb: "view", Scope: authz.ActionScopeSystem})
	access := &operationLogAccess{allowed: true}
	a.registrar = authz.NewRegistrar(registry, access, access, access)
	calls := 0
	a.read = func(_ context.Context, correlation string, offset, limit int) ([]audit.OperationSummary, int64, error) {
		calls++
		require.Equal(t, "trace-1", correlation)
		require.Equal(t, 2, offset)
		require.Equal(t, 10, limit)
		return []audit.OperationSummary{}, 0, nil
	}
	_, api := humatest.New(t, huma.DefaultConfig("test", "1"))
	require.NoError(t, a.RegisterHTTP(t.Context(), api))
	op := api.OpenAPI().Paths["/system/operation-logs"].Get
	require.True(t, authz.IsPlatformOperation(op))
	require.Equal(t, "system_audit_event_view", op.Extensions[authz.GuardExtensionKey])
	require.Equal(t, 200, api.Get("/system/operation-logs?correlation_id=trace-1&offset=2&limit=10", "Authorization: Bearer token").Code)
	require.Equal(t, 422, api.Get("/system/operation-logs?correlation_id=%25", "Authorization: Bearer token").Code)
	require.Equal(t, 422, api.Get("/system/operation-logs", "Authorization: Bearer token").Code)
	require.Equal(t, 422, api.Get("/system/operation-logs?correlation_id=trace-1&limit=201", "Authorization: Bearer token").Code)
	require.Equal(t, 1, calls)
	access.allowed = false
	require.Equal(t, 403, api.Get("/system/operation-logs?correlation_id=trace-1", "Authorization: Bearer token").Code)
	require.Equal(t, 1, calls)
	s := module.NewServices()
	require.NoError(t, s.Seal())
	require.Error(t, (&operationLogsAdapter{}).InjectServices(s))
}
