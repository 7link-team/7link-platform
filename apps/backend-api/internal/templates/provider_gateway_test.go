package templates

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProviderGenerationFreezesProvenanceAndReauthorizesReplay(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	adapter := providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	health, err := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "fixture-health"})
	require.NoError(t, err)
	require.Equal(t, "ENABLED", health.Body.Data.State)
	instance, err := m.instanceOn(ctx, db, 99)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.bindProvidersOn(ctx, tx, 99, instance.Version, map[string]guid.ID{"VIDEO_GEN": provider.ID})
		return err
	}))
	denied := errors.New("execution authority revoked")
	allow := true
	m.providerExecution = func(_ context.Context, _ *gorm.DB, tenant, project, execution guid.ID, _ string) error {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, guid.ID(123), execution)
		if !allow {
			return denied
		}
		return nil
	}
	in := ProviderGenerationRequest{ProjectID: 99, ExecutionID: 123, Kind: "VIDEO_GEN", Input: json.RawMessage(`{"prompt":"fixture", "duration":3}`)}
	request := func() (out ProviderGenerationView, err error) {
		err = db.Transaction(func(tx *gorm.DB) error {
			out, err = m.requestProviderGenerationOn(ctx, tx, in)
			return err
		})
		return
	}
	first, err := request()
	require.NoError(t, err)
	require.Equal(t, "PENDING", first.State)
	in.Input = json.RawMessage(`{"duration":3,"prompt":"fixture"}`)
	replay, err := request()
	require.NoError(t, err)
	require.Equal(t, first.ID, replay.ID)
	allow = false
	_, err = request()
	require.ErrorIs(t, err, denied)
	allow = true
	in.Input = json.RawMessage(`{"duration":4,"prompt":"fixture"}`)
	_, err = request()
	require.ErrorIs(t, err, ErrConflict)

	// The adapter is an explicit local fixture; no live provider success is claimed.
	// Rotate current configuration after enqueue: the job must use its original pin.
	config := map[string]json.RawMessage{"endpoint": json.RawMessage(`"https://changed.invalid"`)}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.patchProviderOn(ctx, tx, provider.ID, health.Body.Data.Version, ProviderPatch{Config: &config})
		return err
	}))
	adapter.Generate = func(_ context.Context, conn ProviderConnection, key string, _ json.RawMessage) (string, error) {
		require.JSONEq(t, `{"endpoint":"https://example.invalid"}`, string(conn.Config))
		require.Equal(t, "fixture-secret-private", conn.Environment["ADAPTER_CREDENTIAL"])
		require.Equal(t, "provider-generation:"+first.ID.String(), key)
		return "fixture-job", nil
	}
	m.providerAdapters["fixture"] = adapter
	require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
	var completed ProviderGenerationView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		completed, err = m.readProviderGenerationOn(ctx, tx, first.ID)
		return err
	}))
	require.Equal(t, "COMPLETED", completed.State)
	require.Equal(t, first.ProviderVersion, completed.ProviderVersion)
	require.NotEmpty(t, completed.OutputSHA256)
	require.Len(t, completed.Output.Outputs, 1)
	before, err := json.Marshal(completed)
	require.NoError(t, err)
	require.NotContains(t, string(before), "fixture-secret-private")
	require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.cancelProviderGenerationOn(ctx, tx, first.ID)
		require.Equal(t, completed, out, "terminal cancellation must preserve output provenance")
		return err
	}))
}

func TestProviderGenerationRejectsUnboundAuthorityAndRollsBackEventFailure(t *testing.T) {
	m, db, ctx := templateHarness(t)
	in := ProviderGenerationRequest{ProjectID: 99, ExecutionID: 123, Kind: "VIDEO_GEN", Input: json.RawMessage(`{}`)}
	_, err := m.requestProviderGenerationOn(ctx, db, in)
	require.ErrorIs(t, err, ErrInvalid, "a real transaction is mandatory")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.requestProviderGenerationOn(ctx, tx, in)
		return err
	}), ErrUnavailable)
	providerFixture(t, m)
	injected := errors.New("outbox unavailable")
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, injected })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.createProviderOn(ctx, tx, ProviderDraft{Kind: "VIDEO_GEN", AdapterKey: "fixture", DisplayName: "Fixture", Secret: "private", AcceptLicenseNotice: true})
		return err
	}), injected)
	var count int64
	require.NoError(t, db.Table(m.names.Providers.String()).Count(&count).Error)
	require.Zero(t, count)
}

func TestProviderWorkerRejectsUnprovenOutputsAndCancelsActualJob(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	adapter := providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	health, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "worker-health"})
	require.NoError(t, e)
	binding, e := m.instanceOn(ctx, db, 99)
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.bindProvidersOn(ctx, tx, 99, binding.Version, map[string]guid.ID{"VIDEO_GEN": provider.ID})
		return e
	}))
	require.Equal(t, "ENABLED", health.Body.Data.State)
	m.providerExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }
	var generation ProviderGenerationView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		generation, e = m.requestProviderGenerationOn(ctx, tx, ProviderGenerationRequest{ProjectID: 99, ExecutionID: 123, Kind: "VIDEO_GEN", Input: json.RawMessage(`{}`)})
		return e
	}))
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{}), ErrInvalid)
	adapter.Generate = func(context.Context, ProviderConnection, string, json.RawMessage) (string, error) {
		return "", errors.New("upstream unavailable")
	}
	m.providerAdapters["fixture"] = adapter
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
	adapter.Generate = func(context.Context, ProviderConnection, string, json.RawMessage) (string, error) {
		return "fixture-job", nil
	}
	m.providerAdapters["fixture"] = adapter
	require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
	for _, output := range []ProviderPoll{{State: "UNKNOWN"}, {State: "COMPLETED"}, {State: "COMPLETED", Outputs: []ProviderOutput{{FileRef: "file", SHA256: "invalid", Model: "m", ModelVersion: "1"}}}, {State: "RUNNING", ReportedCost: "fixture-secret-private"}} {
		adapter.Poll = func(context.Context, ProviderConnection, string) (ProviderPoll, error) { return output, nil }
		m.providerAdapters["fixture"] = adapter
		require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrInvalid)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			out, e := m.readProviderGenerationOn(ctx, tx, generation.ID)
			require.Equal(t, "RUNNING", out.State)
			return e
		}))
	}
	adapter.Poll = func(context.Context, ProviderConnection, string) (ProviderPoll, error) {
		return ProviderPoll{}, errors.New("poll unavailable")
	}
	m.providerAdapters["fixture"] = adapter
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.cancelProviderGenerationOn(ctx, tx, generation.ID)
		require.Equal(t, "CANCEL_REQUESTED", out.State)
		return e
	}))
	adapter.Cancel = func(context.Context, ProviderConnection, string) error { return errors.New("cancel unavailable") }
	m.providerAdapters["fixture"] = adapter
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
	canceled := 0
	adapter.Cancel = func(_ context.Context, _ ProviderConnection, job string) error {
		require.Equal(t, "fixture-job", job)
		canceled++
		return nil
	}
	m.providerAdapters["fixture"] = adapter
	require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
	require.Equal(t, 1, canceled)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.readProviderGenerationOn(ctx, tx, generation.ID)
		require.Equal(t, "CANCELLED", out.State) //nolint:misspell // Preserve the published domain or serialized contract spelling.
		require.NotNil(t, out.CompletedAt)
		return e
	}))
	require.NoError(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}))
}

func TestProviderGenerationPersistenceAndAuthorityFailures(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	_, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "fault-health"})
	require.NoError(t, e)
	binding, e := m.instanceOn(ctx, db, 99)
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.bindProvidersOn(ctx, tx, 99, binding.Version, map[string]guid.ID{"VIDEO_GEN": provider.ID})
		return e
	}))
	m.providerExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }
	input := ProviderGenerationRequest{ProjectID: 99, ExecutionID: 123, Kind: "VIDEO_GEN", Input: json.RawMessage(`{}`)}
	injected := errors.New("generation storage unavailable")
	for _, table := range []string{m.names.Bindings.String(), m.names.ProviderGenerations.String(), m.names.Providers.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("generation-read-failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(injected)
			}
		}))
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.requestProviderGenerationOn(ctx, tx, input); return e }), injected)
		require.NoError(t, db.Callback().Query().Remove("generation-read-failure"))
	}
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("generation-create-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.ProviderGenerations.String() {
			tx.AddError(injected)
		}
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.requestProviderGenerationOn(ctx, tx, input); return e }), injected)
	require.NoError(t, db.Callback().Create().Remove("generation-create-failure"))
	var generation ProviderGenerationView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { generation, e = m.requestProviderGenerationOn(ctx, tx, input); return e }))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("generation-update-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.ProviderGenerations.String() {
			tx.AddError(injected)
		}
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.cancelProviderGenerationOn(ctx, tx, generation.ID); return e }), injected)
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), injected)
	require.NoError(t, db.Callback().Update().Remove("generation-update-failure"))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.readProviderGenerationOn(ctx, tx, 999); return e }), ErrNotFound)
	m.providerExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return ErrForbidden }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.readProviderGenerationOn(ctx, tx, generation.ID); return e }), ErrForbidden)
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrForbidden)
	m.providerExecution = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.readProviderGenerationOn(ctx, tx, generation.ID); return e }), ErrUnavailable)
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
}

func TestProviderWorkerPinsAndCorruptGenerationReadsFailClosed(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	adapter := providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	_, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "pin-health"})
	require.NoError(t, e)
	binding, e := m.instanceOn(ctx, db, 99)
	require.NoError(t, e)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.bindProvidersOn(ctx, tx, 99, binding.Version, map[string]guid.ID{"VIDEO_GEN": provider.ID})
		return e
	}))
	m.providerExecution = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return nil }
	var generation ProviderGenerationView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		generation, e = m.requestProviderGenerationOn(ctx, tx, ProviderGenerationRequest{ProjectID: 99, ExecutionID: 123, Kind: "VIDEO_GEN", Input: json.RawMessage(`{}`)})
		return e
	}))
	delete(m.providerAdapters, "fixture")
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.readProviderGenerationOn(ctx, tx, generation.ID)
		require.Equal(t, "UNAVAILABLE", out.Availability)
		return e
	}))
	m.providerAdapters["fixture"] = adapter
	require.NoError(t, db.Table(m.names.ProviderGenerations.String()).Where("id=?", generation.ID).Update("config_snapshot", "invalid").Error)
	require.ErrorIs(t, m.providerGenerationJob(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
	require.NoError(t, db.Table(m.names.ProviderGenerations.String()).Where("id=?", generation.ID).Update("output_snapshot", "invalid").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.readProviderGenerationOn(ctx, tx, generation.ID); return e }))
	require.ErrorIs(t, providerTransaction(nil), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.requestProviderGenerationOn(ctx, tx, ProviderGenerationRequest{})
		return e
	}), ErrInvalid)
	_, e = providerRequestBytes(json.RawMessage(`invalid`))
	require.ErrorIs(t, e, ErrInvalid)
}
