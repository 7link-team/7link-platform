package organization

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestJobTenantBridgePreservesActiveReaderContract(t *testing.T) {
	services := module.NewServices()
	require.ErrorIs(t, WireJobTenants(services), module.ErrServicesProviding)
	require.NoError(t, services.Seal())
	require.NoError(t, WireJobTenants(services))
	services = module.NewServices()
	require.NoError(t, module.Provide[jobs.TenantFanoutConnector](services, jobs.TenantFanoutConnector{}))
	require.NoError(t, services.Seal())
	require.ErrorIs(t, WireJobTenants(services), jobs.ErrUnavailable)
	services = module.NewServices()
	var captured func(context.Context, guid.ID, int) ([]guid.ID, error)
	require.NoError(t, module.Provide[jobs.TenantFanoutConnector](services, jobs.TenantFanoutConnector{Bind: func(reader func(context.Context, guid.ID, int) ([]guid.ID, error)) error {
		captured = reader
		return nil
	}}))
	sentinel := errors.New("active reader failed")
	require.NoError(t, module.Provide[ActiveTenantEnumerator](services, func(_ context.Context, after guid.ID, limit int) ([]guid.ID, error) {
		require.Equal(t, guid.ID(500), after)
		require.Equal(t, 500, limit)
		return nil, sentinel
	}))
	require.NoError(t, services.Seal())
	require.NoError(t, WireJobTenants(services))
	require.NotNil(t, captured)
	_, err := captured(t.Context(), 500, 500)
	require.ErrorIs(t, err, sentinel)
}
