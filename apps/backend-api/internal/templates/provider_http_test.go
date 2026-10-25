package templates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProviderHTTPNullableMachineLicenceReplayAndCurrentAuthority(t *testing.T) {
	m, db, ctx := templateHarness(t)
	providerFixture(t, m)
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "provider-wire", Method: "POST", Path: "/providers", DefaultStatus: 201}, func(_ context.Context, in *providerCreateInput) (*respx.Body[ProviderView], error) {
		return m.providerCreateHTTP(ctx, in)
	})
	input := ProviderDraft{Kind: "VIDEO_GEN", AdapterKey: "video_gen_http_v1", DisplayName: "Service", Config: map[string]json.RawMessage{"endpoint": json.RawMessage(`"https://example.invalid"`), "model": json.RawMessage(`"configured-model"`)}, AcceptLicenseNotice: true}
	raw, e := json.Marshal(input)
	require.NoError(t, e)
	require.Contains(t, string(raw), `"machine_id":null`)
	for _, invalid := range []string{`0`, `"0"`, `""`} {
		bad := strings.Replace(string(raw), `"machine_id":null`, `"machine_id":`+invalid, 1)
		require.Equal(t, 422, api.Post("/providers", "Idempotency-Key: invalid", strings.NewReader(bad)).Code)
	}
	first := api.Post("/providers", "Idempotency-Key: actual-provider", strings.NewReader(string(raw)))
	require.Equal(t, 201, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), `"state":"UNAVAILABLE"`)
	replay := api.Post("/providers", "Idempotency-Key: actual-provider", strings.NewReader(string(raw)))
	require.Equal(t, 201, replay.Code, replay.Body.String())
	var count int64
	require.NoError(t, db.Table(m.names.Providers.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	noLicence := strings.Replace(string(raw), `"accept_license_notice":true`, `"accept_license_notice":false`, 1)
	require.Equal(t, 422, api.Post("/providers", "Idempotency-Key: no-licence", strings.NewReader(noLicence)).Code) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Equal(t, 403, api.Post("/providers", "Idempotency-Key: actual-provider", strings.NewReader(string(raw))).Code)
}

func TestProviderHTTPReadPatchHealthAndBindingState(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	adapter := providerFixture(t, m)
	first := createProviderFixture(t, m, db, ctx)
	second := createProviderFixture(t, m, db, ctx)
	page, e := m.providerListHTTP(ctx, &providerListInput{Kind: "VIDEO_GEN", State: "UNAVAILABLE", Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	next, e := m.providerListHTTP(ctx, &providerListInput{Limit: 1, Sort: "id", Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Equal(t, second.ID, next.Body.Data.Items[0].ID)
	require.False(t, next.Body.Data.HasMore)
	got, e := m.providerGetHTTP(ctx, &providerGetInput{ID: first.ID.String()})
	require.NoError(t, e)
	require.Equal(t, first, got.Body.Data)
	adapters, e := m.providerAdaptersHTTP(ctx, &struct{}{})
	require.NoError(t, e)
	require.NotEmpty(t, adapters.Body.Data.Items)
	for i := 1; i < len(adapters.Body.Data.Items); i++ {
		require.Less(t, adapters.Body.Data.Items[i-1].Key, adapters.Body.Data.Items[i].Key)
	}
	name, state, secret := "Renamed", "DISABLED", "rotated-private"
	patch := &providerPatchInput{ID: first.ID.String(), IfMatch: "1", IdempotencyKey: "provider-change", Body: ProviderPatch{DisplayName: &name, State: &state, Secret: &secret}}
	changed, e := m.providerPatchHTTP(ctx, patch)
	require.NoError(t, e)
	require.Equal(t, "Renamed", changed.Body.Data.DisplayName)
	require.Equal(t, "DISABLED", changed.Body.Data.State)
	replay, e := m.providerPatchHTTP(ctx, patch)
	require.NoError(t, e)
	require.Equal(t, changed.Body.Data, replay.Body.Data)
	health, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: first.ID.String(), IdempotencyKey: "disabled-health"})
	require.NoError(t, e)
	require.Equal(t, "DISABLED", health.Body.Data.State)
	health, e = m.providerHealthHTTP(ctx, &providerHealthInput{ID: second.ID.String(), IdempotencyKey: "enabled-health"})
	require.NoError(t, e)
	require.Equal(t, "ENABLED", health.Body.Data.State)
	binding, e := m.providerBindingGetHTTP(ctx, &providerBindingInput{ProjectID: "99"})
	require.NoError(t, e)
	require.Empty(t, binding.Body.Data.Providers)
	selectInput := &providerBindingChangeInput{ProjectID: "99", IfMatch: strconv.Itoa(binding.Body.Data.Version), IdempotencyKey: "select-provider"}
	selectInput.Body.Providers = map[string]guid.ID{"VIDEO_GEN": second.ID}
	selected, e := m.providerBindingSetHTTP(ctx, selectInput)
	require.NoError(t, e)
	require.Len(t, selected.Body.Data.Providers, 1)
	require.Equal(t, second.ID, selected.Body.Data.Providers[0].ProviderID)
	selectedAgain, e := m.providerBindingSetHTTP(ctx, selectInput)
	require.NoError(t, e)
	require.Equal(t, selected.Body.Data, selectedAgain.Body.Data)
	adapter.Health = func(context.Context, ProviderConnection) (ProviderHealth, error) {
		return ProviderHealth{}, errors.New("private upstream error")
	}
	m.providerAdapters["fixture"] = adapter
	unavailable, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: second.ID.String(), IdempotencyKey: "failed-health"})
	require.NoError(t, e)
	require.Equal(t, "UNAVAILABLE", unavailable.Body.Data.State)
	require.NotContains(t, unavailable.Body.Data.Limitations, "private")
	adapter.Definition.LicenseNotice = "Changed acceptance terms"
	m.providerAdapters["fixture"] = adapter
	unavailable, e = m.providerHealthHTTP(ctx, &providerHealthInput{ID: second.ID.String(), IdempotencyKey: "changed-terms"})
	require.NoError(t, e)
	require.Equal(t, "UNAVAILABLE", unavailable.Body.Data.State)
	require.Contains(t, unavailable.Body.Data.Limitations, "unavailable")
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.providerGetHTTP(ctx, &providerGetInput{ID: first.ID.String()})
	assertStatus(t, e, 403)
	_, e = m.providerAdaptersHTTP(ctx, &struct{}{})
	assertStatus(t, e, 403)
	_, e = m.providerListHTTP(ctx, &providerListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 403)
	_, e = m.providerHealthHTTP(ctx, &providerHealthInput{ID: first.ID.String(), IdempotencyKey: "disabled-health"})
	assertStatus(t, e, 403)
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	_, e = m.providerBindingGetHTTP(ctx, &providerBindingInput{ProjectID: "99"})
	assertStatus(t, e, 403)
	_, e = m.providerBindingSetHTTP(ctx, selectInput)
	assertStatus(t, e, 403)
}
func TestProviderHTTPRejectsMalformedIdentityAndStorageFailures(t *testing.T) {
	m, db, ctx := templateHarness(t)
	providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	for _, in := range []providerListInput{{Limit: 0, Sort: "id"}, {Limit: 101, Sort: "id"}, {Limit: 1, Sort: "other"}, {Limit: 1, Sort: "id", Cursor: "bad"}} {
		_, e := m.providerListHTTP(ctx, &in)
		assertStatus(t, e, 422)
	}
	_, e := m.providerGetHTTP(ctx, &providerGetInput{ID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.providerGetHTTP(ctx, &providerGetInput{ID: "999"})
	assertStatus(t, e, 404)
	_, e = m.providerPatchHTTP(ctx, &providerPatchInput{ID: provider.ID.String(), IfMatch: "bad"})
	assertStatus(t, e, 422)
	_, e = m.providerHealthHTTP(ctx, &providerHealthInput{ID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.providerBindingGetHTTP(ctx, &providerBindingInput{ProjectID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.providerBindingSetHTTP(ctx, &providerBindingChangeInput{ProjectID: "99", IfMatch: "bad"})
	assertStatus(t, e, 422)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("provider-read-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Providers.String() {
			tx.AddError(errors.New("storage unavailable"))
		}
	}))
	_, e = m.providerListHTTP(ctx, &providerListInput{Limit: 1, Sort: "id"})
	assertStatus(t, e, 500)
	_, e = m.providerGetHTTP(ctx, &providerGetInput{ID: provider.ID.String()})
	assertStatus(t, e, 500)
	require.NoError(t, db.Callback().Query().Remove("provider-read-failure"))
	delete(m.providerAdapters, "fixture")
	_, e = m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "missing-adapter"})
	assertStatus(t, e, 422)
}

func TestProviderBindingHTTPCurrentHealthAndAtomicPersistence(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	providerFixture(t, m)
	provider := createProviderFixture(t, m, db, ctx)
	binding, e := m.instanceOn(ctx, db, 99)
	require.NoError(t, e)
	input := &providerBindingChangeInput{ProjectID: "99", IfMatch: strconv.Itoa(binding.Version), IdempotencyKey: "binding-health-required"}
	input.Body.Providers = map[string]guid.ID{"VIDEO_GEN": provider.ID}
	_, e = m.providerBindingSetHTTP(ctx, input)
	assertStatus(t, e, 422)
	_, e = m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "binding-health"})
	require.NoError(t, e)
	for _, choices := range []map[string]guid.ID{{}, {"OTHER": provider.ID}, {"VIDEO_GEN": 999}, {"IMAGE_GEN": provider.ID}} {
		input.IdempotencyKey = "invalid-choices-" + strconv.Itoa(len(choices)) + fmt.Sprint(choices)
		input.Body.Providers = choices
		_, e = m.providerBindingSetHTTP(ctx, input)
		assertStatus(t, e, 422)
	}
	input.Body.Providers = map[string]guid.ID{"VIDEO_GEN": provider.ID}
	for _, operation := range []string{"create", "update"} {
		injected := errors.New("binding persistence unavailable")
		callback := func(tx *gorm.DB) {
			if tx.Statement.Table == m.names.ProviderBindings.String() || tx.Statement.Table == m.names.Bindings.String() {
				tx.AddError(injected)
			}
		}
		if operation == "create" {
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("provider-binding-failure", callback))
		} else {
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("provider-binding-failure", callback))
		}
		input.IdempotencyKey = "binding-failure-" + operation
		_, e = m.providerBindingSetHTTP(ctx, input)
		assertStatus(t, e, 500)
		require.NoError(t, db.Callback().Create().Remove("provider-binding-failure"))
		require.NoError(t, db.Callback().Update().Remove("provider-binding-failure"))
		read, e := m.providerBindingGetHTTP(ctx, &providerBindingInput{ProjectID: "99"})
		require.NoError(t, e)
		require.Equal(t, binding.Version, read.Body.Data.Version)
		require.Empty(t, read.Body.Data.Providers)
	}
	input.IdempotencyKey = "binding-final"
	_, e = m.providerBindingSetHTTP(ctx, input)
	require.NoError(t, e)
	input.IdempotencyKey = "binding-stale"
	_, e = m.providerBindingSetHTTP(ctx, input)
	assertStatus(t, e, 409)
	m.providerSealer = nil
	_, e = m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "health-no-sealer"})
	assertStatus(t, e, 503)
}

func TestProviderHealthRechecksVersionAndRollsBackEffects(t *testing.T) {
	for _, failure := range []string{"version", "update", "event"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx := templateHarness(t)
			adapter := providerFixture(t, m)
			provider := createProviderFixture(t, m, db, ctx)
			injected := errors.New("health persistence unavailable")
			if failure == "version" {
				adapter.Health = func(context.Context, ProviderConnection) (ProviderHealth, error) {
					require.NoError(t, db.Table(m.names.Providers.String()).Where("id=?", provider.ID).Update("version", 2).Error)
					return ProviderHealth{Available: true}, nil
				}
				m.providerAdapters["fixture"] = adapter
			}
			if failure == "update" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("health-update-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Providers.String() {
						tx.AddError(injected)
					}
				}))
				defer db.Callback().Update().Remove("health-update-failure")
			}
			if failure == "event" {
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return injected }
			}
			_, e := m.providerHealthHTTP(ctx, &providerHealthInput{ID: provider.ID.String(), IdempotencyKey: "health-boundary"})
			if failure == "version" {
				assertStatus(t, e, 409)
			} else {
				assertStatus(t, e, 500)
			}
			got, e := m.providerGetHTTP(ctx, &providerGetInput{ID: provider.ID.String()})
			require.NoError(t, e)
			require.Equal(t, "UNAVAILABLE", got.Body.Data.State)
			require.Nil(t, got.Body.Data.HealthCheckedAt)
		})
	}
}
