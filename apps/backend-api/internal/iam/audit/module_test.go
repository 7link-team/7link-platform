package audit

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	auditi18n "7link.com/platform/internal/iam/audit/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditDependencyProvider struct {
	name    string
	provide func(*module.Services) error
}

func (provider *auditDependencyProvider) ModuleName() string { return provider.name }

func (provider *auditDependencyProvider) ProvideServices(services *module.Services) error {
	return provider.provide(services)
}

func TestAuditModuleConstructionAndReadiness(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("audit-test", "1"))
	assert.Panics(t, func() { NewModule(nil, Config{}) })
	instance := NewModule(runtime, Config{})
	assert.Equal(t, Name, instance.ModuleName())
	assert.Equal(t, []string{platformi18n.Name, guid.Name}, instance.ModuleDependencies())
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
}

func TestAuditModuleLifecycleProvidesStableAppender(t *testing.T) {
	h := newAuditHarness(t, "sl_")
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	instance := NewModule(h.runtime, Config{Config: module.Config{Enabled: true}})
	assert.ErrorIs(t, instance.CheckHealth(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, instance.RegisterHTTP(t.Context(), api), "module is unavailable")

	registry := module.NewRegistry()
	require.NoError(t, registry.Register(
		platformi18n.NewModule(nil, platformi18n.Base),
		respx.NewModule(nil),
		&auditDependencyProvider{name: guid.Name, provide: func(services *module.Services) error {
			if err := module.Provide[*authz.Registrar](services, auditRuntimeRegistrar()); err != nil {
				return err
			}
			return module.Provide[guid.Source](services, newAuditTestIDs(9000))
		}},
		instance,
	))
	require.NoError(t, registry.Activate())
	require.NoError(t, registry.Wire(nil))
	appender, err := module.Resolve[Appender](registry.Services())
	require.NoError(t, err)
	assert.ErrorIs(t, appender(t.Context(), h.db, DomainEvent{TenantID: 901, EventType: "before"}), ErrUnavailable)
	require.NoError(t, registry.Initialize(t.Context()))
	require.NoError(t, registry.Start(t.Context()))
	require.NoError(t, instance.CheckHealth(t.Context()))

	tx := h.db.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, appender(t.Context(), tx, DomainEvent{TenantID: 901, EventType: "after"}))
	require.NoError(t, tx.Commit().Error)
	assert.Equal(t, int64(1), h.count(t, "iam_audit_events", 901))
	_, api = humatest.New(t)
	require.NoError(t, instance.RegisterHTTP(t.Context(), api))
	assert.Len(t, auditOperationManifest(api.OpenAPI()), 21)
}

func TestAuditModuleActivationComesFromConfig(t *testing.T) {
	assert.False(t, (&Module{}).Enabled())
	assert.True(t, (&Module{config: Config{Config: module.Config{Enabled: true}}}).Enabled())
}

func TestAuditModuleReportsMissingAndInvalidStartupDependencies(t *testing.T) {
	instance := NewModule(
		appenv.NewRuntime(appenv.NewIdentity("audit-test", "1")),
		Config{},
	)
	services := module.NewServices()
	require.NoError(t, services.Seal())
	assert.ErrorContains(t, instance.InjectServices(services), "authorization registrar")
	provided := module.NewServices()
	require.NoError(t, instance.ProvideServices(provided))
	assert.ErrorContains(t, instance.ProvideServices(provided), "provide audit appender")

	var unavailable *Module
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	assert.ErrorContains(t, unavailable.Initialize(t.Context()), "module is required")
	assert.ErrorIs(t, (&Module{config: Config{Anchor: AnchorConfig{Enabled: true}}}).Start(t.Context()), ErrUnavailable)
	_, api := humatest.New(t)
	assert.ErrorContains(t, unavailable.RegisterHTTP(t.Context(), api), "module is unavailable")

	services = module.NewServices()
	require.NoError(t, module.Provide[*authz.Registrar](services, auditRuntimeRegistrar()))
	require.NoError(t, module.Provide[guid.Source](services, newAuditTestIDs(1)))
	require.NoError(t, services.Seal())
	require.NoError(t, instance.InjectServices(services))
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	assert.ErrorContains(t, instance.Initialize(t.Context()), "runtime database")

	h := newAuditHarness(t, "")
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	badAnchor := NewModule(h.runtime, Config{
		Anchor: AnchorConfig{Enabled: true},
	})
	badAnchor.registrar = auditRuntimeRegistrar()
	badAnchor.ids = newAuditTestIDs(1)
	require.NoError(t, badAnchor.Initialize(t.Context()))
	assert.ErrorContains(t, badAnchor.Start(t.Context()), "anchor configuration")

	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	badSigner := NewModule(h.runtime, Config{
		Anchor: AnchorConfig{
			Enabled: true, Endpoint: "127.0.0.1:9000", Bucket: "anchors",
			AccessKey: "access", SecretKey: "secret", RetentionDays: 30, SigningKey: "bad",
		},
	})
	badSigner.registrar = auditRuntimeRegistrar()
	badSigner.ids = newAuditTestIDs(1)
	require.NoError(t, badSigner.Initialize(t.Context()))
	assert.ErrorContains(t, badSigner.Start(t.Context()), "signing key")
}

func TestAuditModuleErasureLedgerStartupAndRecorderGates(t *testing.T) {
	assert.ErrorIs(t, (&Module{}).recordErasure(t.Context(), ErasureObligation{}), ErrErasureLedgerDisabled)

	newInstance := func(t *testing.T, config Config) *Module {
		t.Helper()
		h := newAuditHarness(t, "")
		require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
		instance := NewModule(h.runtime, config)
		instance.registrar = auditRuntimeRegistrar()
		instance.ids = newAuditTestIDs(1)
		require.NoError(t, instance.Initialize(t.Context()))
		return instance
	}

	fixture := newFakeS3()
	_, server := fakeAnchorStore(t, fixture, "unused")
	base := Config{
		Anchor: AnchorConfig{
			Endpoint: server.URL, Bucket: "erasure-module", AccessKey: "access",
			SecretKey: "secret", RetentionDays: 30,
		},
		ErasureLedger: ErasureLedgerConfig{Enabled: true, Timeout: 5 * time.Second},
	}

	invalidTimeout := base
	invalidTimeout.ErasureLedger.Timeout = 0
	assert.ErrorContains(t, newInstance(t, invalidTimeout).Start(t.Context()), "timeout")

	fixture.bucket = true
	fixture.objectLock = false
	assert.ErrorContains(t, newInstance(t, base).Start(t.Context()), "must support object locking")
}

func TestAuditI18nRegistrationAndKeyParity(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, auditi18n.Register())
	keys := []string{
		auditi18n.InvalidTime, auditi18n.EventNotFound, auditi18n.InvalidQuery,
		auditi18n.IntegrityFailed, auditi18n.Unavailable, auditi18n.AnchorNotEnabled,
		auditi18n.AnchorEmpty, auditi18n.AnchorAlreadyExists, auditi18n.AnchorUnavailable,
		auditi18n.RetentionInvalid, auditi18n.RootSigningDisabled,
		auditi18n.ArchiveDisabled, auditi18n.ArchiveEmpty, auditi18n.ArchiveNotFound,
		auditi18n.ArchiveBusy, auditi18n.ArchiveIntegrity, auditi18n.ArchiveUnavailable,
	}
	for _, locale := range []string{"en-US", "ms-MY", "zh-CN"} {
		ctx := platformi18n.WithLocale(t.Context(), locale)
		for _, key := range keys {
			message := platformi18n.TContext(ctx, key)
			assert.NotEmpty(t, message, locale+" "+key)
			assert.NotEqual(t, key, message, locale+" "+key)
		}
	}
}

func TestAuditModuleStartsAnchoredAndSignedService(t *testing.T) {
	h := newAuditHarness(t, "")
	fixture := newFakeS3()
	_, server := fakeAnchorStore(t, fixture, "unused")
	seed := make([]byte, 32)
	config := Config{Anchor: AnchorConfig{
		Enabled: true, Endpoint: server.URL, Bucket: "audit-module",
		AccessKey: "access", SecretKey: "secret", RetentionDays: 30,
		SigningKey: base64.StdEncoding.EncodeToString(seed),
	}}
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	instance := NewModule(h.runtime, config)
	instance.registrar = auditRuntimeRegistrar()
	instance.ids = newAuditTestIDs(13000)
	require.NoError(t, instance.Initialize(t.Context()))
	require.NoError(t, instance.Start(t.Context()))
	require.NotNil(t, instance.service.anchor)
	require.NotNil(t, instance.service.signer)
}

type auditTokenVerifier struct{}

func (auditTokenVerifier) Authenticate(context.Context, string, string) (*authnext.Claims, error) {
	return &authnext.Claims{PrincipalID: 1, Subject: "1"}, nil
}

type auditPermissionChecker struct{}

func (auditPermissionChecker) Check(context.Context, guid.ID, string, guid.ID) (bool, error) {
	return true, nil
}

func (auditPermissionChecker) CheckPlatform(context.Context, string, guid.ID) (bool, error) {
	return true, nil
}

type auditMembershipChecker struct{}

func (auditMembershipChecker) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}

func auditRuntimeRegistrar() *authz.Registrar {
	return authz.NewRegistrar(authz.DefaultRegistry(), auditTokenVerifier{}, auditPermissionChecker{}, auditMembershipChecker{})
}
