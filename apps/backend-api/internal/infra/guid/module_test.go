package guid

import (
	"testing"

	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/pkg/appenv"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestModuleUsesWUIDModuleAndTracksLeaseState(t *testing.T) {
	t.Setenv(wuid.EnvKey, "17")
	rt := appenv.NewRuntime(appenv.NewIdentity("", ""))
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(t.Context()))
	workerIDModule := wuid.NewModule(rt)
	guidModule := NewModule(rt)
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
	guidModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("", "")))
	services := module.NewServices()
	require.NoError(t, services.Seal())
	err := guidModule.InjectServices(services)
	assert.ErrorIs(t, err, module.ErrServiceMissing)
}

func TestModuleContractAndRegistrationBeforeStart(t *testing.T) {
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0"))
	m := NewModule(rt)
	assert.Same(t, rt, m.runtime)
	assert.Equal(t, Name, m.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, wuid.Name}, m.ModuleDependencies())
	assert.ErrorContains(t, m.Start(t.Context()), "was not injected")
	_, err := m.Next()
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.ErrorIs(t, m.CheckHealth(t.Context()), ErrUnavailable)

	_, api := humatest.New(t)
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	grpcServer := grpc.NewServer()
	m.RegisterGRPC(grpcServer)
	assert.Contains(t, grpcServer.GetServiceInfo(), "platform.guid.v1.GuidService")
}
