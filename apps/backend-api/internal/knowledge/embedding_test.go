package knowledge

import (
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/secrets"
	"7link.com/platform/internal/knowledge/model"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func embeddingFixture(t *testing.T, handler http.HandlerFunc) (*Module, *model.EmbeddingConfig) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	sealer := secrets.NewModule(secrets.Config{ActiveKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))})
	require.NoError(t, sealer.Initialize(t.Context()))
	require.NoError(t, sealer.RegisterPurpose(embeddingPurpose))
	aad, err := secrets.AssociatedData(1, 1)
	require.NoError(t, err)
	sealed, err := sealer.Seal(embeddingPurpose, aad, []byte("credential-never-returned"))
	require.NoError(t, err)
	m := &Module{now: func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) }}
	m.embedding.sealer = sealer
	m.embedding.client = server.Client()
	m.embedding.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return m, &model.EmbeddingConfig{TenantID: 1, BaseURL: server.URL, Model: "fixture-model", Dims: 384, APIKeySealed: sealed, TimeoutMS: 1000, RPM: 60, TPM: 150000}
}
func TestEmbeddingUsesRealHTTPSealedKeyDimensionsOrderAndTenantCache(t *testing.T) {
	var requests atomic.Int32
	m, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !assert.Equal(t, "/v1/embeddings", r.URL.Path) {
			return
		}
		if !assert.Equal(t, "Bearer credential-never-returned", r.Header.Get("Authorization")) {
			return
		}
		var body struct {
			Model      string
			Input      []string
			Dimensions int
		}
		if !assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) {
			return
		}
		if !assert.Equal(t, "fixture-model", body.Model) {
			return
		}
		if !assert.Equal(t, 384, body.Dimensions) {
			return
		}
		v := make([]float64, 384)
		v[0] = 1
		if !assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})) {
			return
		}
	})
	first, reason := m.embed(t.Context(), c, []string{"same query"}, true)
	require.Empty(t, reason)
	require.Len(t, first[0], 384)
	first[0][0] = 9
	second, reason := m.embed(t.Context(), c, []string{"same query"}, true)
	require.Empty(t, reason)
	require.Equal(t, 1.0, second[0][0]) //nolint:testifylint // Exact numeric equality is frozen by this regression contract.
	require.EqualValues(t, 1, requests.Load())
	other := *c
	other.TenantID = 2
	_, reason = m.embed(t.Context(), &other, []string{"same query"}, true)
	require.Equal(t, "EMBEDDING_CREDENTIAL_UNAVAILABLE", reason)
	require.EqualValues(t, 1, requests.Load(), "tenant-bound cache must not bypass credential binding")
	view, err := json.Marshal(embeddingView(c))
	require.NoError(t, err)
	require.NotContains(t, string(view), "credential-never-returned")
	require.NotContains(t, string(view), c.APIKeySealed)
}
func TestEmbeddingFailuresReturnStableReasonsNeverUpstreamSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, body, reason string
		status             int
	}{
		{"wrong-dimensions", `{"data":[{"index":0,"embedding":[1,2]}]}`, "DIMS_MISMATCH", 200},
		{"rate-limit", `secret provider body`, "EMBEDDING_RATE_LIMIT", 429},
		{"provider-error", `private credential detail`, "EMBEDDING_HTTP_ERROR", 500},
		{"invalid-json", `secret malformed`, "EMBEDDING_RESPONSE_INVALID", 200},
		{"empty-vector", `{"data":[]}`, "EMBEDDING_RESPONSE_INVALID", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			v, reason := m.embed(t.Context(), c, []string{"test"}, false)
			require.Nil(t, v)
			require.Equal(t, tc.reason, reason)
			require.NotContains(t, reason, "secret")
		})
	}
	m, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(30 * time.Millisecond) })
	c.TimeoutMS = 10
	_, reason := m.embed(t.Context(), c, []string{"timeout"}, false)
	require.Equal(t, "EMBEDDING_REQUEST_FAILED", reason)
}
func TestEmbeddingInputAndNonPostgresFallbackAreExplicit(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ranks, mode, reason := m.hybridRanksOn(knowledgeContext(t, 2), tx, 1, "query", nil)
		require.Nil(t, ranks)
		require.Equal(t, "K2_FALLBACK", mode)
		require.Equal(t, "VECTOR_REQUIRES_POSTGRESQL", reason)
		_, err := m.saveEmbeddingOn(knowledgeContext(t, 2), tx, 1, EmbeddingConfigInput{})
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
	for _, base := range []string{"http://api.example", "https://127.0.0.1", "https://user:password@api.example", "https://api.example/#secret"} {
		in := EmbeddingConfigInput{BaseURL: base, Model: "model", Dims: 384}
		require.ErrorIs(t, m.validateEmbeddingInput(&in), ErrInvalid)
	}
	in := EmbeddingConfigInput{BaseURL: "https://api.example", Model: "model", Dims: 384}
	require.NoError(t, m.validateEmbeddingInput(&in))
	require.Equal(t, 64, in.BatchSize)
	require.Equal(t, 1500, in.TimeoutMS)
	require.NotEqual(t, embeddingModelKey(in.BaseURL, in.Model, 384), embeddingModelKey(in.BaseURL, in.Model, 768))
}

// Only the deployment gate is overridden; SQL still uses the SQLite fixture.
// This exercises proof/HTTP/non-persistence semantics, not PostgreSQL vectors.
type embeddingGateDialect struct{ gorm.Dialector }

func (embeddingGateDialect) Name() string { return "postgres" }
func TestEmbeddingUnsavedProbeUsesProofAndDoesNotPersistConfiguration(t *testing.T) {
	fixture, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		v := make([]float64, 384)
		v[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	})
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	m.embedding.sealer = fixture.embedding.sealer
	m.embedding.client = fixture.embedding.client
	endpoint, err := url.Parse(c.BaseURL)
	require.NoError(t, err)
	m.config.Embedding.AllowedHosts = []string{endpoint.Hostname()}
	db.Dialector = embeddingGateDialect{db.Dialector}
	proofs := 0
	m.embedding.consumeProof = func(_ context.Context, _ *gorm.DB, actor guid.ID, cookie, proof, purpose, target string) error {
		proofs++
		require.Equal(t, guid.ID(2), actor)
		require.Equal(t, "session", cookie)
		require.Equal(t, "proof", proof)
		require.Equal(t, "knowledge_embedding_write", purpose)
		require.Equal(t, "tenant:1:embedding-config", target)
		return nil
	}
	in := EmbeddingConfigInput{BaseURL: c.BaseURL, Model: c.Model, Dims: c.Dims, APIKey: "credential-never-returned", ReauthProof: "proof"}
	ctx := context.WithValue(knowledgeContext(t, 2), embeddingCookieKey{}, "session")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.probeEmbeddingOn(ctx, tx, 1, &in)
		require.NoError(t, e)
		require.True(t, out.OK)
		require.Equal(t, 384, out.DimsReturned)
		return e
	}))
	require.Equal(t, 1, proofs)
	var count int64
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Count(&count).Error)
	require.Zero(t, count)
	in.APIKey = ""
	in.BaseURL = "https://other.example"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.probeEmbeddingOn(ctx, tx, 1, &in)
		require.ErrorIs(t, e, ErrInvalid)
		return nil
	}))
}
func TestEmbeddingScheduledBackfillDeclarationAndDegradationWindow(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	at := m.now()
	m.now = func() time.Time { return at }
	m.config.Embedding.Enabled = true
	require.Len(t, m.JobHandlers(), 5)
	require.Len(t, m.JobSchedules(), 4)
	for _, schedule := range m.JobSchedules() {
		if schedule.Kind == "knowledge.embedding_backfill" {
			require.Equal(t, time.Minute, schedule.Interval)
			require.Equal(t, jobs.ScopePerTenant, schedule.TenantScope)
		}
	}
	require.Error(t, m.embeddingBackfillJob(t.Context(), jobs.Job{}))
	require.NoError(t, m.embeddingBackfillJob(t.Context(), jobs.Job{TenantID: 1}))
	c := &model.EmbeddingConfig{TenantID: 1, BaseURL: "https://provider.example", Model: "model", Dims: 384, BatchSize: 64, RPM: 60, TPM: 150000, TimeoutMS: 1500, State: "ACTIVE", Version: 1, UpdatedBy: 2, UpdatedAt: m.now()}
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Create(c).Error)
	for range 4 {
		require.NoError(t, m.embeddingFailureOn(t.Context(), db, c, "EMBEDDING_HTTP_ERROR"))
	}
	current, e := m.embeddingConfigOn(t.Context(), db, 1, false)
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", current.State)
	require.NoError(t, m.embeddingFailureOn(t.Context(), db, c, "EMBEDDING_HTTP_ERROR"))
	current, e = m.embeddingConfigOn(t.Context(), db, 1, false)
	require.NoError(t, e)
	require.Equal(t, "DEGRADED", current.State)
	require.Equal(t, m.now().Add(15*time.Minute), *current.DegradedUntil)
}

func TestEmbeddingBlueGreenUsesPriorProviderUntilPromotion(t *testing.T) {
	prior := model.EmbeddingConfig{TenantID: 1, BaseURL: "https://old.example", Model: "old", Dims: 384, APIKeySealed: "sealed-prior", CoveragePPM: 1000000, Version: 1}
	prior.ActiveModelKey = embeddingModelKey(prior.BaseURL, prior.Model, prior.Dims)
	snapshot, e := json.Marshal(prior)
	require.NoError(t, e)
	pending := prior
	pending.BaseURL = "https://new.example"
	pending.Model = "new"
	pending.Dims = 768
	pending.APIKeySealed = "sealed-new"
	pending.Version = 2
	pending.CoveragePPM = 100
	pending.ActiveProviderJSON = string(snapshot)
	active, e := activeEmbeddingConfig(&pending)
	require.NoError(t, e)
	require.Equal(t, prior.BaseURL, active.BaseURL)
	require.Equal(t, prior.APIKeySealed, active.APIKeySealed)
	require.Equal(t, 384, active.Dims)
	require.Equal(t, 2, active.Version)
	pending.ActiveProviderJSON = ""
	_, e = activeEmbeddingConfig(&pending)
	require.ErrorIs(t, e, ErrUnavailable)
	pending.ActiveModelKey = embeddingModelKey(pending.BaseURL, pending.Model, pending.Dims)
	active, e = activeEmbeddingConfig(&pending)
	require.NoError(t, e)
	require.Equal(t, "sealed-new", active.APIKeySealed)
}

func TestEmbeddingDisabledHTTPRoutesReturnExplicitUnavailable(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/tenants/1/knowledge-embedding-config", ""},
		{http.MethodPut, "/tenants/1/knowledge-embedding-config", `{"base_url":"https://provider.example","model":"model","dims":384,"enabled":false,"version":0,"batch_size":64,"rpm":60,"tpm":150000,"timeout_ms":1500}`},
		{http.MethodPost, "/tenants/1/knowledge-embedding-probes", `{}`},
		{http.MethodPost, "/tenants/1/knowledge-embedding-backfills", `{}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)).WithContext(knowledgeContext(t, 2))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	}
}

func TestEmbeddingSaveRejectsRedirectingStoredCredentialWithoutProof(t *testing.T) {
	fixture, c := embeddingFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("rejected redirect must not contact provider") })
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	m.embedding.sealer = fixture.embedding.sealer
	m.embedding.client = fixture.embedding.client
	db.Dialector = embeddingGateDialect{db.Dialector}
	c.Version = 1
	c.State = "ACTIVE"
	c.BatchSize = 64
	c.UpdatedBy = 2
	c.UpdatedAt = m.now()
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Create(c).Error)
	in := EmbeddingConfigInput{BaseURL: "https://different.example", Model: c.Model, Dims: c.Dims, Version: 1, Enabled: true}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.saveEmbeddingOn(knowledgeContext(t, 2), tx, 1, in)
		require.ErrorIs(t, e, ErrInvalid)
		return nil
	}))
	current, e := m.embeddingConfigOn(t.Context(), db, 1, false)
	require.NoError(t, e)
	require.Equal(t, c.BaseURL, current.BaseURL)
	require.Equal(t, c.APIKeySealed, current.APIKeySealed)
	require.Equal(t, 1, current.Version)
}

func TestEmbeddingBatchTimeoutIsIndependentFromQueryLatencyLimit(t *testing.T) {
	m, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond)
		v := make([]float64, 384)
		v[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	})
	c.TimeoutMS = 10
	_, reason := m.embed(t.Context(), c, []string{"query"}, false)
	require.Equal(t, "EMBEDDING_REQUEST_FAILED", reason)
	vectors, reason := m.embedBatch(t.Context(), c, []string{"batch"})
	require.Empty(t, reason)
	require.Len(t, vectors, 1)
	require.Equal(t, 10, c.TimeoutMS)
}

func TestEmbeddingConfigurationPersistsSealedCASAndRollsBackQueueFailure(t *testing.T) {
	fixture, c := embeddingFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("configuration must not contact embedding provider") })
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	m.embedding.sealer = fixture.embedding.sealer
	m.embedding.client = fixture.embedding.client
	endpoint, e := url.Parse(c.BaseURL)
	require.NoError(t, e)
	m.config.Embedding.AllowedHosts = []string{endpoint.Hostname()}
	m.events = embeddingOutboxFixture{owner: m.events, dialect: db.Dialector}
	db.Dialector = embeddingGateDialect{db.Dialector}
	proofs := 0
	m.embedding.consumeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { proofs++; return nil }
	input := EmbeddingConfigInput{BaseURL: c.BaseURL, Model: c.Model, Dims: c.Dims, Enabled: true, APIKey: "credential-never-returned", ReauthProof: "current-proof"}
	save := func(in EmbeddingConfigInput) (out EmbeddingConfigView, e error) {
		e = db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.saveEmbeddingOn(knowledgeContext(t, 2), tx, 1, in)
			return e
		})
		return
	}
	out, e := save(input)
	require.NoError(t, e)
	require.Equal(t, 1, out.Version)
	require.Equal(t, "ACTIVE", out.State)
	require.Equal(t, 1, proofs)
	stored, e := m.embeddingConfigOn(t.Context(), db, 1, false)
	require.NoError(t, e)
	require.NotEmpty(t, stored.APIKeySealed)
	require.NotEqual(t, input.APIKey, stored.APIKeySealed)
	input.Version = 1
	input.APIKey = ""
	input.Enabled = false
	out, e = save(input)
	require.NoError(t, e)
	require.Equal(t, 2, out.Version)
	require.Equal(t, "DISABLED", out.State)
	require.Equal(t, 1, proofs)
	_, e = save(input)
	require.ErrorIs(t, e, ErrConflict)
	input.Version = 2
	input.Enabled = true
	failure := errors.New("embedding queue unavailable")
	m.queue.(*knowledgeQueue).failure = failure
	_, e = save(input)
	require.ErrorIs(t, e, failure)
	after, e := m.embeddingConfigOn(t.Context(), db, 1, false)
	require.NoError(t, e)
	require.Equal(t, 2, after.Version)
	require.Equal(t, "DISABLED", after.State)
	require.Equal(t, stored.APIKeySealed, after.APIKeySealed)
}
func TestEmbeddingConfigurationStorageFailuresCannotPersistPartialCredentialRotation(t *testing.T) {
	fixture, c := embeddingFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("save does not call remote provider") })
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	m.embedding.sealer = fixture.embedding.sealer
	m.events = embeddingOutboxFixture{owner: m.events, dialect: db.Dialector}
	db.Dialector = embeddingGateDialect{db.Dialector}
	endpoint, e := url.Parse(c.BaseURL)
	require.NoError(t, e)
	m.config.Embedding.AllowedHosts = []string{endpoint.Hostname()}
	m.embedding.consumeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	failure := errors.New("embedding configuration storage unavailable")
	rollback := errors.New("rollback configuration")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("embedding-config-storage", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("embedding-config-storage", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("embedding-config-storage", probe))
	input := EmbeddingConfigInput{BaseURL: c.BaseURL, Model: c.Model, Dims: c.Dims, APIKey: "new-credential", Enabled: true, ReauthProof: "proof"}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.saveEmbeddingOn(knowledgeContext(t, 2), tx, 1, input)
			if e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 1)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "configuration stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("embedding-config-storage"))
	require.NoError(t, db.Callback().Create().Remove("embedding-config-storage"))
	require.NoError(t, db.Callback().Update().Remove("embedding-config-storage"))
	_, e = m.embeddingConfigOn(t.Context(), db, 1, false)
	require.ErrorIs(t, e, ErrNotFound)
}

// Gate tests keep the real SQLite outbox; only the embedding deployment guard
// uses the PostgreSQL label. These tests do not claim vector SQL verification.
type embeddingOutboxFixture struct {
	owner   events.Publisher
	dialect gorm.Dialector
}

func (p embeddingOutboxFixture) Publish(ctx context.Context, tx *gorm.DB, in events.Event) (guid.ID, error) {
	prior := tx.Dialector
	tx.Dialector = p.dialect
	defer func() { tx.Dialector = prior }()
	return p.owner.Publish(ctx, tx, in)
}

func TestEmbeddingPublicConfigurationProbeAndFallbackRetainSealedCredential(t *testing.T) {
	fixture, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer credential-never-returned", r.Header.Get("Authorization"))
		vector := make([]float64, 384)
		vector[0] = 1
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}}))
	})
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	m.embedding.sealer = fixture.embedding.sealer
	m.embedding.client = fixture.embedding.client
	m.embedding.consumeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	endpoint, e := url.Parse(c.BaseURL)
	require.NoError(t, e)
	m.config.Embedding.AllowedHosts = []string{endpoint.Hostname()}
	m.events = embeddingOutboxFixture{owner: m.events, dialect: db.Dialector}
	db.Dialector = embeddingGateDialect{db.Dialector}
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		r := httptest.NewRequest(method, "/tenants/1/"+path, strings.NewReader(string(raw))).WithContext(knowledgeContext(t, 2))
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out
	}
	input := EmbeddingConfigInput{BaseURL: c.BaseURL, Model: c.Model, Dims: 384, APIKey: "credential-never-returned", BatchSize: 8, RPM: 60, TPM: 150000, TimeoutMS: 1000, Enabled: true, ReauthProof: "proof"}
	saved := call("PUT", "knowledge-embedding-config", input)
	require.Equal(t, 200, saved.Code, saved.Body.String())
	require.NotContains(t, saved.Body.String(), input.APIKey)
	read := call("GET", "knowledge-embedding-config", nil)
	require.Equal(t, 200, read.Code, read.Body.String())
	require.Contains(t, read.Body.String(), `"has_api_key":true`)
	probed := call("POST", "knowledge-embedding-probes", map[string]any{})
	require.Equal(t, 200, probed.Code, probed.Body.String())
	require.Contains(t, probed.Body.String(), `"ok":true`)
	unsaved := call("POST", "knowledge-embedding-probes", EmbeddingProbeConfigInput{BaseURL: c.BaseURL, Model: c.Model, Dims: 384, TimeoutMS: 1000})
	require.Equal(t, 200, unsaved.Code, unsaved.Body.String())
	require.Contains(t, unsaved.Body.String(), `"dims_returned":384`)
	backfill := call("POST", "knowledge-embedding-backfills", map[string]int{"limit": 1})
	require.Equal(t, 503, backfill.Code, backfill.Body.String(), "SQLite cannot provide PostgreSQL vector storage")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, mode, reason := m.hybridRanksOn(knowledgeContext(t, 2), tx, 1, "evidence", nil)
		require.Equal(t, "K2_FALLBACK", mode)
		require.Equal(t, "EMBEDDING_COVERAGE_LOW", reason)
		return nil
	}))
	m.runtime = nil
	for _, v := range []struct {
		method, path string
		body         any
	}{{"GET", "knowledge-embedding-config", nil}, {"PUT", "knowledge-embedding-config", input}, {"POST", "knowledge-embedding-probes", map[string]any{}}, {"POST", "knowledge-embedding-backfills", map[string]int{"limit": 1}}} {
		r := call(v.method, v.path, v.body)
		require.Equal(t, 503, r.Code, r.Body.String())
	}
}

func TestEmbeddingInitializationRequiresLeaseSealerProofAndValidNetworkPolicy(t *testing.T) {
	fixture, _ := embeddingFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("initialization must not contact provider") })
	m, _, services := knowledgeHarness(t)
	lease, e := module.Resolve[dlock.TransactionLease](services)
	require.NoError(t, e)
	for omitted := 0; omitted < 4; omitted++ {
		s := module.NewServices()
		if omitted != 0 {
			require.NoError(t, module.Provide[dlock.TransactionLease](s, lease))
		}
		if omitted != 1 {
			require.NoError(t, module.Provide[secrets.Sealer](s, fixture.embedding.sealer))
		}
		if omitted != 2 {
			require.NoError(t, module.Provide[authn.ReauthenticationConsumer](s, func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return ErrForbidden }))
		}
		require.NoError(t, s.Seal())
		m.config.Embedding.Enabled = true
		m.config.Embedding.AllowedNetworks = nil
		e = m.initializeEmbeddingServices(s)
		if omitted < 3 {
			require.Error(t, e)
		} else {
			require.NoError(t, e)
			require.NotNil(t, m.embedding.client)
			r := httptest.NewRequest(http.MethodGet, "https://provider.example.test/redirect", nil)
			require.ErrorIs(t, m.embedding.client.CheckRedirect(r, nil), http.ErrUseLastResponse)
			m.config.Embedding.AllowedNetworks = []string{"not-a-network"}
			require.ErrorIs(t, m.initializeEmbeddingServices(s), ErrInvalid)
		}
	}
}
func TestEmbeddingRateRetryDateCacheEvictionAndInvalidProviderVectors(t *testing.T) {
	for _, retry := range []string{"120", "Thu, 22 Oct 2026 00:03:00 GMT"} {
		m, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", retry)
			w.WriteHeader(http.StatusTooManyRequests)
		})
		_, reason := m.embed(t.Context(), c, []string{"query"}, false)
		require.Equal(t, "EMBEDDING_RATE_LIMIT", reason)
		require.True(t, m.embedding.rate[1].retryUntil.After(m.now().Add(time.Minute)))
		_, reason = m.embed(t.Context(), c, []string{"again"}, false)
		require.Equal(t, "EMBEDDING_RATE_LIMIT", reason)
	}
	for _, tc := range []struct {
		index int
		zero  bool
	}{{-1, false}, {1, false}, {0, true}} {
		m, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
			vector := make([]float64, 384)
			if !tc.zero {
				vector[0] = 1
			}
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": tc.index, "embedding": vector}}}))
		})
		_, reason := m.embed(t.Context(), c, []string{"query"}, false)
		require.Equal(t, "EMBEDDING_RESPONSE_INVALID", reason)
	}
	requests := 0
	m, c := embeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		v := make([]float64, 384)
		v[0] = 1
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}}))
	})
	m.embedding.cache = map[string]embeddingCacheItem{}
	for i := 0; i < 1000; i++ {
		m.embedding.cache[strconv.Itoa(i)] = embeddingCacheItem{used: uint64(i + 1), expires: m.now().Add(time.Hour), vector: []float64{1}}
	}
	m.embedding.sequence = 1000
	vectors, reason := m.embed(t.Context(), c, []string{"new query"}, true)
	require.Empty(t, reason)
	require.Len(t, vectors[0], 384)
	require.Len(t, m.embedding.cache, 1000)
	require.NotContains(t, m.embedding.cache, "0")
	require.Equal(t, 1, requests)
	c.RPM = 1
	_, reason = m.embed(t.Context(), c, []string{"different query"}, false)
	require.Equal(t, "EMBEDDING_RATE_LIMIT", reason)
	m.embedding.rate = nil
	c.RPM = 60
	c.BaseURL = ":invalid"
	_, reason = m.embed(t.Context(), c, []string{"query"}, false)
	require.Equal(t, "EMBEDDING_REQUEST_INVALID", reason)
}
func TestHybridFallbackStatesNeverClaimVectorExecution(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	db.Dialector = embeddingGateDialect{db.Dialector}
	check := func(want string) {
		ranks, mode, reason := m.hybridRanksOn(knowledgeContext(t, 2), db, 1, "evidence", nil)
		require.Nil(t, ranks)
		if want != "" {
			require.Equal(t, "K2_FALLBACK", mode)
		} else {
			require.Empty(t, mode)
		}
		require.Equal(t, want, reason)
	}
	check("EMBEDDING_NOT_CONFIGURED")
	c := model.EmbeddingConfig{TenantID: 1, BaseURL: "https://provider.example.test", Model: "model", Dims: 384, BatchSize: 8, RPM: 60, TPM: 1000, TimeoutMS: 1000, State: "DISABLED", Version: 1, UpdatedBy: 2, UpdatedAt: m.now()}
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Create(&c).Error)
	check("")
	until := m.now().Add(time.Minute)
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Where("tenant_id = ?", 1).Updates(map[string]any{"state": "DEGRADED", "degraded_until": until}).Error)
	check("EMBEDDING_DEGRADED")
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Where("tenant_id = ?", 1).Updates(map[string]any{"state": "ACTIVE", "degraded_until": nil, "active_model_key": "wrong-key"}).Error)
	check("EMBEDDING_ACTIVE_CONFIG_UNAVAILABLE")
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Where("tenant_id = ?", 1).Updates(map[string]any{"active_model_key": embeddingModelKey(c.BaseURL, c.Model, c.Dims), "coverage_ppm": 1000000}).Error)
	check("PGVECTOR_0_8_UNAVAILABLE")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("hybrid-current-config", func(tx *gorm.DB) { tx.AddError(errors.New("configuration unavailable")) }))
	check("EMBEDDING_CONFIG_UNAVAILABLE")
	require.NoError(t, db.Callback().Query().Remove("hybrid-current-config"))
}

func TestEmbeddingCredentialRotationRetainsPreviousProviderAndRequiresNewProof(t *testing.T) {
	fixture, c := embeddingFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("configuration changes must not contact provider") })
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	m.embedding.sealer = fixture.embedding.sealer
	endpoint, err := url.Parse(c.BaseURL)
	require.NoError(t, err)
	m.config.Embedding.AllowedHosts = []string{endpoint.Hostname()}
	m.events = embeddingOutboxFixture{owner: m.events, dialect: db.Dialector}
	db.Dialector = embeddingGateDialect{db.Dialector}
	input := EmbeddingConfigInput{BaseURL: c.BaseURL, Model: c.Model, Dims: c.Dims, APIKey: "rotation-credential", Enabled: true}
	save := func() (EmbeddingConfigView, error) {
		var out EmbeddingConfigView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.saveEmbeddingOn(knowledgeContext(t, 2), tx, 1, input)
			return err
		})
		return out, err
	}
	_, err = save()
	require.ErrorIs(t, err, ErrUnavailable)
	m.embedding.consumeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return ErrForbidden }
	_, err = save()
	require.ErrorIs(t, err, ErrForbidden)
	m.embedding.consumeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return nil }
	sealer := m.embedding.sealer
	m.embedding.sealer = nil
	_, err = save()
	require.ErrorIs(t, err, ErrUnavailable)
	m.embedding.sealer = sealer
	input.Version = 1
	_, err = save()
	require.ErrorIs(t, err, ErrConflict)
	input.Version = 0
	out, err := save()
	require.NoError(t, err)
	old, err := m.embeddingConfigOn(t.Context(), db, 1, false)
	require.NoError(t, err)
	active := embeddingModelKey(input.BaseURL, input.Model, input.Dims)
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Where("tenant_id = ?", 1).Update("active_model_key", active).Error)
	input.Version = out.Version
	input.APIKey = ""
	input.Model = "updated-model"
	out, err = save()
	require.NoError(t, err)
	require.Equal(t, active, out.ActiveModelKey)
	require.NotEqual(t, out.ActiveModelKey, out.PendingModelKey)
	after, err := m.embeddingConfigOn(t.Context(), db, 1, false)
	require.NoError(t, err)
	require.NotEmpty(t, after.ActiveProviderJSON)
	var prior model.EmbeddingConfig
	require.NoError(t, json.Unmarshal([]byte(after.ActiveProviderJSON), &prior))
	require.Equal(t, old.Model, prior.Model)
	require.Equal(t, old.APIKeySealed, prior.APIKeySealed)
	input.Version = out.Version
	input.BaseURL = "https://other.example.test"
	_, err = save()
	require.ErrorIs(t, err, ErrInvalid)
}

func TestEmbeddingProbeRejectsMissingCredentialAndCurrentProofWithoutPersisting(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	m.config.Embedding.Enabled = true
	db.Dialector = embeddingGateDialect{db.Dialector}
	run := func(input *EmbeddingConfigInput) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.probeEmbeddingOn(knowledgeContext(t, 2), tx, 1, input)
			return err
		})
	}
	require.ErrorIs(t, run(nil), ErrNotFound)
	require.ErrorIs(t, run(&EmbeddingConfigInput{BaseURL: "http://invalid"}), ErrInvalid)
	input := EmbeddingConfigInput{BaseURL: "https://provider.example.test", Model: "current", Dims: 384}
	require.ErrorIs(t, run(&input), ErrInvalid)
	input.APIKey = "new-credential"
	require.ErrorIs(t, run(&input), ErrUnavailable)
	fixture, _ := embeddingFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("failed proof must not call provider") })
	m.embedding.sealer = fixture.embedding.sealer
	m.embedding.consumeProof = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return ErrForbidden }
	require.ErrorIs(t, run(&input), ErrForbidden)
	var count int64
	require.NoError(t, query.BindEmbeddingConfig(db, m.native).Count(&count).Error)
	require.Zero(t, count)
}

func TestEmbeddingAllowedNetworkPolicyIsAppliedBeforeSendingCredential(t *testing.T) {
	m, c := embeddingFixture(t, func(http.ResponseWriter, *http.Request) {
		t.Error("invalid network or untrusted TLS must prevent request dispatch")
	})
	endpoint, err := url.Parse(c.BaseURL)
	require.NoError(t, err)
	m.config.Embedding.AllowedHosts = []string{endpoint.Hostname()}
	m.config.Embedding.AllowedNetworks = []string{"invalid-prefix"}
	vectors, reason := m.embed(t.Context(), c, []string{"query"}, false)
	require.Nil(t, vectors)
	require.Equal(t, "EMBEDDING_NETWORK_POLICY_INVALID", reason)
	m.config.Embedding.AllowedNetworks = []string{"127.0.0.0/8"}
	vectors, reason = m.embed(t.Context(), c, []string{"query"}, false)
	require.Nil(t, vectors)
	require.Equal(t, "EMBEDDING_REQUEST_FAILED", reason, "the allowlist does not bypass TLS certificate verification")
}
