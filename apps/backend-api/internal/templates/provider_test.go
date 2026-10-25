package templates

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/internal/templates/query"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func providerFixture(t *testing.T, m *Module) ProviderAdapter {
	t.Helper()
	sealer := secrets.NewModule(secrets.Config{ActiveKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))})
	require.NoError(t, sealer.Initialize(t.Context()))
	require.NoError(t, sealer.RegisterPurpose(providerSecretPurpose))
	m.providerSealer = sealer
	a := ProviderAdapter{
		Definition:           ProviderAdapterView{Key: "fixture", Kind: "VIDEO_GEN", LicenseNotice: "Fixture licence", RegionNotice: "Fixture region", PublicConfigFields: []string{"endpoint"}, SecretRequired: true}, //nolint:misspell // Preserve the published domain or serialized contract spelling.
		SecretEnvironmentKey: "ADAPTER_CREDENTIAL",
		ValidateConfig:       func(json.RawMessage) error { return nil },
		Health: func(context.Context, ProviderConnection) (ProviderHealth, error) {
			return ProviderHealth{Available: true}, nil
		},
		Generate: func(context.Context, ProviderConnection, string, json.RawMessage) (string, error) {
			return "actual-job", nil
		},
		Poll: func(context.Context, ProviderConnection, string) (ProviderPoll, error) {
			return ProviderPoll{State: "COMPLETED", Outputs: []ProviderOutput{{FileRef: "files:actual-output", SHA256: strings.Repeat("a", 64), Model: "fixture-model", ModelVersion: "v1", Seed: "42"}}}, nil
		},
		Cancel: func(context.Context, ProviderConnection, string) error { return nil },
	}
	s := module.NewServices()
	require.NoError(t, m.provideProviders(s))
	require.NoError(t, s.Seal())
	connector, err := module.Resolve[ProviderAdapterConnector](s)
	require.NoError(t, err)
	require.NoError(t, connector.Register(a))
	return a
}
func createProviderFixture(t *testing.T, m *Module, db *gorm.DB, ctx context.Context) ProviderView {
	t.Helper()
	var out ProviderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.createProviderOn(ctx, tx, ProviderDraft{Kind: "VIDEO_GEN", AdapterKey: "fixture", DisplayName: "Fixture", Config: map[string]json.RawMessage{"endpoint": json.RawMessage(`"https://example.invalid"`)}, Secret: "fixture-secret-private", AcceptLicenseNotice: true})
		return err
	}))
	return out
}
func TestProviderRegistrySealingPublicConfigAndLicence(t *testing.T) {
	m, db, ctx := templateHarness(t)
	a := providerFixture(t, m)
	view := createProviderFixture(t, m, db, ctx)
	require.Equal(t, "UNAVAILABLE", view.State)
	require.Nil(t, view.HealthCheckedAt)
	require.True(t, view.HasSecret)
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fixture-secret-private")
	q := query.BindProviders(db, m.names)
	row, err := q.WithContext(ctx).Where(q.ID.Eq(view.ID)).First()
	require.NoError(t, err)
	require.NotContains(t, row.SealedSecret, "fixture-secret-private")
	conn, cleanup, err := m.providerConnection(row)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"ADAPTER_CREDENTIAL": "fixture-secret-private"}, conn.Environment)
	cleanup()
	require.Empty(t, conn.Environment)
	foreign := *row
	foreign.TenantID = 2
	_, _, err = m.providerConnection(&foreign)
	require.ErrorIs(t, err, ErrUnavailable)
	for _, config := range []map[string]json.RawMessage{{"secret": json.RawMessage(`"private"`)}, {"endpoint": json.RawMessage(`{"nested":[{"api_key":"private"}]}`)}, {"undeclared": json.RawMessage(`1`)}} {
		_, err = providerConfig(a, config)
		require.ErrorIs(t, err, ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.createProviderOn(ctx, tx, ProviderDraft{Kind: "VIDEO_GEN", AdapterKey: "fixture", DisplayName: "No licence", Secret: "private"}) //nolint:misspell // Preserve the published domain or serialized contract spelling.
		return err
	}), ErrProviderLicense)
	a.Definition.LicenseNotice = "Changed terms"
	m.providerAdapters[a.Definition.Key] = a
	require.Equal(t, "UNAVAILABLE", m.providerView(row).State)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.patchProviderOn(ctx, tx, view.ID, 1, ProviderPatch{}); return err }), ErrProviderLicense)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.patchProviderOn(ctx, tx, view.ID, 1, ProviderPatch{AcceptLicenseNotice: true})
		return err
	}))
	row, err = q.WithContext(ctx).Where(q.ID.Eq(view.ID)).First()
	require.NoError(t, err)
	require.Equal(t, providerLicenseHash(a), row.LicenseSHA256)
	delete(m.providerAdapters, "fixture")
	require.Equal(t, "UNAVAILABLE", m.providerView(row).State)
}
func TestProviderRegistryRejectsMissingOperationsAndSealsAtInitialization(t *testing.T) {
	m, _, _ := templateHarness(t)
	a := providerFixture(t, m)
	s := module.NewServices()
	require.NoError(t, m.provideProviders(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[ProviderAdapterConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Register(a), ErrConflict)
	a.Definition.Key = "other"
	a.Poll = nil
	require.ErrorIs(t, c.Register(a), ErrInvalid)
	a.Poll = func(context.Context, ProviderConnection, string) (ProviderPoll, error) {
		return ProviderPoll{}, errors.New("offline")
	}
	a.Definition.PublicConfigFields = []string{"api_key"}
	require.ErrorIs(t, c.Register(a), ErrInvalid)
	a.Definition.PublicConfigFields = nil
	m.calibrationSealed = true
	require.ErrorIs(t, c.Register(a), ErrInvalid)
	e, err := module.Resolve[ProviderExecutionConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, e.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }), ErrInvalid)
}

func TestProviderSealingExplicitlyUnavailableWithoutSecrets(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	// Registry lifecycle errors must not be swallowed as an optional capability.
	require.ErrorIs(t, m.injectProviders(s), module.ErrServicesProviding)
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectProviders(s))
	sealed, err := m.sealProviderSecret(1, 2, "credential")
	require.Empty(t, sealed)
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorContains(t, err, "provider credential sealing requires the secrets module")
}

func TestProviderMutationsRejectInvalidPatchesAndRollbackWrites(t *testing.T) {
	m, db, ctx := templateHarness(t)
	adapter := providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	empty, invalid := "", "UNKNOWN"
	badConfig := map[string]json.RawMessage{"secret": json.RawMessage(`"private"`)}
	for _, patch := range []ProviderPatch{{DisplayName: &empty}, {Secret: &empty}, {State: &invalid}, {Config: &badConfig}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.patchProviderOn(ctx, tx, provider.ID, 1, patch); return e }), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.patchProviderOn(ctx, tx, provider.ID, 2, ProviderPatch{}); return e }), ErrConflict)
	for _, draft := range []ProviderDraft{{AdapterKey: "missing"}, {AdapterKey: "fixture", Kind: "VIDEO_GEN", DisplayName: ""}, {AdapterKey: "fixture", Kind: "VIDEO_GEN", DisplayName: "No secret", AcceptLicenseNotice: true}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createProviderOn(ctx, tx, draft); return e }))
	}
	injected := errors.New("provider update unavailable")
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("provider-update-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Providers.String() {
			tx.AddError(injected)
		}
	}))
	name := "Changed"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchProviderOn(ctx, tx, provider.ID, 1, ProviderPatch{DisplayName: &name})
		return e
	}), injected)
	require.NoError(t, db.Callback().Update().Remove("provider-update-failure"))
	audit := m.audit
	m.audit = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchProviderOn(ctx, tx, provider.ID, 1, ProviderPatch{DisplayName: &name})
		return e
	}), ErrUnavailable)
	m.audit = audit
	got, e := m.providerGetHTTP(ctx, &providerGetInput{ID: provider.ID.String()})
	require.NoError(t, e)
	require.Equal(t, provider, got.Body.Data)
	q := query.BindProviders(db, m.names)
	row, e := q.WithContext(ctx).Where(q.ID.Eq(provider.ID)).First()
	require.NoError(t, e)
	sealer := m.providerSealer
	m.providerSealer = nil
	_, cleanup, e := m.providerConnection(row)
	cleanup()
	require.ErrorIs(t, e, ErrProviderSecretsUnavailable)
	secret := "rotated"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchProviderOn(ctx, tx, provider.ID, 1, ProviderPatch{Secret: &secret})
		return e
	}), ErrProviderSecretsUnavailable)
	m.providerSealer = sealer
	adapter.SecretEnvironmentKey = ""
	m.providerAdapters["fixture"] = adapter
	_, cleanup, e = m.providerConnection(row)
	cleanup()
	require.ErrorIs(t, e, ErrUnavailable)
	row.SealedSecret = ""
	_, cleanup, e = m.providerConnection(row)
	cleanup()
	require.ErrorIs(t, e, ErrUnavailable)
	delete(m.providerAdapters, "fixture")
	_, cleanup, e = m.providerConnection(row)
	cleanup()
	require.ErrorIs(t, e, ErrProviderAdapter)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.patchProviderOn(ctx, tx, provider.ID, 1, ProviderPatch{}); return e }), ErrProviderAdapter)
	adapter.ValidateConfig = func(json.RawMessage) error { return injected }
	_, e = providerConfig(adapter, nil)
	require.ErrorIs(t, e, ErrInvalid)
}

func TestProviderCapabilityBindingRejectsDuplicatesAndSealedChanges(t *testing.T) {
	m, _, _ := templateHarness(t)
	s := module.NewServices()
	require.NoError(t, m.provideProviders(s))
	require.NoError(t, s.Seal())
	execution, e := module.Resolve[ProviderExecutionConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, execution.Bind(nil), ErrInvalid)
	require.NoError(t, execution.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }))
	require.ErrorIs(t, execution.Bind(m.providerExecution), ErrInvalid)
	video, e := module.Resolve[VideoSourceConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, video.Bind(nil), ErrInvalid)
	require.NoError(t, video.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (VideoExecution, error) {
		return VideoExecution{}, ErrUnavailable
	}))
	require.ErrorIs(t, video.Bind(m.videoSource), ErrInvalid)
	rubric, e := module.Resolve[RubricProofConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, rubric.Bind(nil), ErrInvalid)
	require.NoError(t, rubric.Bind(func(context.Context, *gorm.DB, RubricProofRequest) (RubricProofFact, error) {
		return RubricProofFact{}, ErrUnavailable
	}))
	require.ErrorIs(t, rubric.Bind(m.rubricProof), ErrInvalid)
}
