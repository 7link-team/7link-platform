package events

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModuleLifecycleAndCapabilities(t *testing.T) {
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"))
	m := NewModule(rt)
	require.Equal(t, Name, m.ModuleName())
	require.Equal(t, []string{guid.Name}, m.ModuleDependencies())
	require.ErrorIs(t, m.Initialize(t.Context()), ErrUnavailable)
	missing := module.NewServices()
	require.NoError(t, missing.Seal())
	require.Error(t, m.InjectServices(missing))
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	require.NoError(t, module.Provide[guid.Source](services, &testIDs{}))
	require.NoError(t, services.Seal())
	require.NoError(t, m.InjectServices(services))
	publisher, err := module.Resolve[Publisher](services)
	require.NoError(t, err)
	require.Same(t, m, publisher)
	catalog, err := module.Resolve[Catalog](services)
	require.NoError(t, err)
	require.Same(t, m, catalog)
	require.NoError(t, m.Initialize(t.Context()))
	require.ErrorIs(t, m.AddTypes("delivery"), ErrCatalogSealed)
	duplicate := module.NewServices()
	require.NoError(t, module.Provide[Catalog](duplicate, m))
	require.Error(t, NewModule(rt).ProvideServices(duplicate))
}
func TestCatalogValidatesAtomically(t *testing.T) {
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"))
	valid := TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: RealtimeSignal}
	for _, change := range []func(*TypeSpec){
		func(s *TypeSpec) { s.Name = "bad" }, func(s *TypeSpec) { s.SchemaVersion = 0 }, func(s *TypeSpec) { s.SchemaVersion = 32768 }, func(s *TypeSpec) { s.SubjectKind = "Bad" }, func(s *TypeSpec) { s.Realtime = "UNKNOWN" },
	} {
		m := NewModule(rt)
		bad := valid
		change(&bad)
		require.ErrorIs(t, m.AddTypes("delivery", valid, bad), ErrInvalidEvent)
		require.Empty(t, m.types)
	}
	m := NewModule(rt)
	require.ErrorIs(t, m.AddTypes("bad-owner", valid), ErrInvalidEvent)
	require.Error(t, m.AddTypes("delivery", valid, valid))
	require.Empty(t, m.types)
	require.NoError(t, m.AddTypes("delivery", valid))
	require.Error(t, m.AddTypes("delivery", valid))
	for _, mode := range []RealtimeMode{RealtimeNone, RealtimeFull} {
		m := NewModule(rt)
		spec := valid
		spec.Realtime = mode
		require.NoError(t, m.AddTypes("delivery", spec))
	}
}
