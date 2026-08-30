package guid

import (
	"testing"

	"7link.com/platform/internal/infra/wuid"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleUsesWUIDModuleAndTracksLeaseState(t *testing.T) {
	t.Setenv(wuid.EnvKey, "17")
	require.NoError(t, platformi18n.NewModule(platformi18n.Base).Initialize(t.Context()))
	workerIDModule := wuid.NewModule(module.Runtime{})
	guidModule := NewModule()
	services := module.NewServices()
	require.NoError(t, workerIDModule.ProvideServices(services))
	require.NoError(t, guidModule.ProvideServices(services))
	require.NoError(t, services.Seal())
	require.NoError(t, guidModule.InjectServices(services))
	require.NoError(t, guidModule.Initialize(t.Context()))
	require.NoError(t, workerIDModule.Start(t.Context()))
	require.NoError(t, guidModule.Start(t.Context()))
	t.Cleanup(func() {
		_ = guidModule.Stop(t.Context())
		_ = workerIDModule.Stop(t.Context())
	})

	assert.NoError(t, guidModule.CheckHealth(t.Context()))
	id, err := guidModule.Next()
	require.NoError(t, err)
	assert.Positive(t, id)

	require.NoError(t, workerIDModule.Stop(t.Context()))
	assert.ErrorIs(t, guidModule.CheckHealth(t.Context()), ErrUnavailable)
	_, err = guidModule.Next()
	assert.Error(t, err)
}

func TestModuleRequiresWUIDService(t *testing.T) {
	guidModule := NewModule()
	services := module.NewServices()
	require.NoError(t, services.Seal())
	err := guidModule.InjectServices(services)
	assert.ErrorIs(t, err, module.ErrServiceMissing)
}
