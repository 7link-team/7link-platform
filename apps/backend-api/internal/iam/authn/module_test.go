package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type authnDependencyProvider struct {
	name    string
	provide func(*module.Services) error
}

func (m *authnDependencyProvider) ModuleName() string { return m.name }

func (m *authnDependencyProvider) ProvideServices(services *module.Services) error {
	return m.provide(services)
}

func TestAuthnModuleIdentityDependenciesAndReadiness(t *testing.T) {
	instance := NewModule(nil, localAuthnConfig())
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name, audit.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), authnext.ErrUnavailable)

	_, api := humatest.New(t)
	assert.Error(t, instance.RegisterHTTP(t.Context(), api))
	assert.Error(t, instance.Start(t.Context()))
	assert.NoError(t, instance.Stop(t.Context()))
}

func TestAuthnModuleResolvesDependenciesAndRunsLifecycle(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	notifications := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(notifications.Close)
	config := localAuthnConfig()
	config.Recovery = authnext.RecoveryConfig{
		Enabled: true, TokenTTL: 15 * time.Minute, Cooldown: time.Hour,
		ResetURL: "https://app.example.test/recover",
	}
	config.Notification = authnext.NotificationConfig{
		URL: notifications.URL, PollInterval: time.Minute, RequestTimeout: time.Second, MaxAttempts: 1,
	}
	instance := NewModule(harness.runtime, config)
	registry := module.NewRegistry()
	require.NoError(t, registry.Register(
		platformi18n.NewModule(nil, platformi18n.Base),
		respx.NewModule(nil),
		&authnDependencyProvider{name: guid.Name, provide: func(services *module.Services) error {
			return module.Provide[guid.Source](services, newRepositoryTestIDs(5000))
		}},
		&authnDependencyProvider{name: audit.Name, provide: func(services *module.Services) error {
			return module.Provide[audit.Appender](services, harness.repository.audit)
		}},
		instance,
	))
	require.NoError(t, registry.Activate())
	active, exists := registry.ByName(Name)
	require.True(t, exists)
	assert.Same(t, instance, active)
	require.NoError(t, registry.Wire(nil))

	web, err := module.Resolve[WebService](registry.Services())
	require.NoError(t, err)
	assert.Same(t, instance, web)
	invitations, err := module.Resolve[InvitationCredentials](registry.Services())
	require.NoError(t, err)
	assert.Same(t, instance, invitations)
	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))
	require.NoError(t, registry.Start(t.Context()))
	assert.NotNil(t, instance.notificationCancel)

	_, api := humatest.New(t)
	require.NoError(t, registry.RegisterHTTP(t.Context(), api))
	assert.Len(t, authnOperationManifest(api.OpenAPI()), 30)
	require.NoError(t, registry.Stop(context.Background()))
	assert.Nil(t, instance.notificationCancel)
	require.NoError(t, registry.Deinitialize(context.Background()))
}

func TestAuthnModuleReportsMissingWiring(t *testing.T) {
	services := module.NewServices()
	require.NoError(t, services.Seal())
	instance := NewModule(nil, localAuthnConfig())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve authn id generator")

	services = module.NewServices()
	require.NoError(t, module.Provide[guid.Source](services, newRepositoryTestIDs(1)))
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "resolve authn audit appender")

	providers := module.NewServices()
	require.NoError(t, instance.ProvideServices(providers))
	assert.ErrorContains(t, instance.ProvideServices(providers), "provide authn web service")
}
