package knowledge

import (
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestKnowledgeMetricsMeasuredSamplesAndUnavailableDenominators(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	now := m.now()
	window := KnowledgeMetricsInput{From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.Nil(t, out.HitRate.Value)
		require.NotEmpty(t, out.HitRate.Reason)
		require.Nil(t, out.UsedRate.Value)
		require.Nil(t, out.CitationAcceptanceRate.Value)
		require.Nil(t, out.SearchZeroResultRate.Value)
		require.Zero(t, out.InjectionLatency.Samples)
		return nil
	}))
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "EXPERIENCE", Title: "Export amount", Body: "Actual source", Provenance: "Source"})
	// Multiple evidence receipts for one included entry must not inflate usage.
	for i, kind := range []string{"INCLUSION", "INCLUSION", "CITATION"} {
		row := model.UsageReceipt{ID: guid.ID(i + 100), TenantID: 1, SourceID: 20, EntryID: entry.ID, ExecutionID: 30, EvidenceID: guid.ID(i + 40), Kind: kind, ContentHash: entry.ContentHash, CreatedAt: now}
		require.NoError(t, query.BindUsageReceipts(db, m.names).Create(&row).Error)
	}
	for _, text := range []string{"amount", "missing"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.searchOn(knowledgeContext(t, 2), tx, 1, KnowledgeSearchInput{DiagnosticHistoryInput: DiagnosticHistoryInput{Scope: "TEAM", Limit: 50}, Query: text, Mode: "ALL"})
			return err
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		result, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.EqualValues(t, 1, result.Inclusions)
		require.EqualValues(t, 1, result.Citations)
		require.InDelta(t, 1.0, *result.UsedRate.Value, 1e-9)
		require.EqualValues(t, 2, result.Searches)
		require.InDelta(t, 0.5, *result.SearchZeroResultRate.Value, 1e-9)
		require.Equal(t, 2, result.SearchLatency.Samples)
		require.EqualValues(t, 1, result.Memory.Written)
		require.Zero(t, result.DiagnosticSearches)
		return nil
	}))
	for _, invalid := range []KnowledgeMetricsInput{{From: now, To: now}, {From: now, To: now.Add(91 * 24 * time.Hour)}, {}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, invalid); return err }), ErrInvalid)
	}
}
func TestMetricPercentileAndZeroAreDifferentFromUnavailable(t *testing.T) {
	empty := metricLatency(nil, "NONE")
	require.Nil(t, empty.P95MS)
	require.Equal(t, "NONE", empty.Reason)
	samples := make([]float64, 100)
	for i := range samples {
		samples[i] = float64(i + 1)
	}
	latency := metricLatency(samples, "NONE")
	require.InDelta(t, 95.0, *latency.P95MS, 1e-9)
	measured := metricRate(0, 3, "NONE")
	require.NotNil(t, measured.Value)
	require.Zero(t, *measured.Value)
	require.Empty(t, measured.Reason)
}

func TestKnowledgeMetricsProjectionDedupAndCurrentProjectFence(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	now := m.now()
	fact := RuntimeMetricFact{Kind: "INJECTION", ProjectID: 3, ExecutionID: 30, EvidenceID: 30, Hit: true, OccurredAt: now}
	m.metrics.projection.ReadOn = func(context.Context, *gorm.DB, guid.ID, *guid.ID, time.Time, time.Time) ([]RuntimeMetricFact, error) {
		return []RuntimeMetricFact{fact, fact, {Kind: "INJECTION", ProjectID: 4, ExecutionID: 40, EvidenceID: 40, Hit: false, OccurredAt: now}}, nil
	}
	window := KnowledgeMetricsInput{From: now.Add(-time.Hour), To: now.Add(time.Hour)}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.EqualValues(t, 2, out.Injections)
		require.InDelta(t, 0.5, *out.HitRate.Value, 1e-9)
		require.Nil(t, out.InjectionLatency.P95MS)
		return nil
	}))
	m.policy = func(_ context.Context, _ *gorm.DB, _, project guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: project == 3}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.EqualValues(t, 1, out.Injections)
		require.InDelta(t, 1.0, *out.HitRate.Value, 1e-9)
		return nil
	}))
}

func TestKnowledgeMetricsOutcomeRequiresCitationAcrossWindowAndCountsEntryOnce(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	now := m.now()
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "DOCUMENT", Title: "Cited entry", Body: "Frozen source", Provenance: "Source"})
	m.metrics.projection.ReadOn = func(context.Context, *gorm.DB, guid.ID, *guid.ID, time.Time, time.Time) ([]RuntimeMetricFact, error) {
		return []RuntimeMetricFact{
			{Kind: "CITATION_OUTCOME", ProjectID: 3, ExecutionID: 30, EvidenceID: 31, EntryID: entry.ID, Outcome: "REJECTED", OccurredAt: now.Add(-time.Minute)},
			{Kind: "CITATION_OUTCOME", ProjectID: 3, ExecutionID: 30, EvidenceID: 32, EntryID: entry.ID, Outcome: "ACCEPTED", OccurredAt: now},
		}, nil
	}
	window := KnowledgeMetricsInput{From: now.Add(-time.Hour), To: now.Add(time.Hour), ProjectID: "3"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.Nil(t, out.CitationAcceptanceRate.Value)
		return nil
	}))
	receipt := model.UsageReceipt{ID: 100, TenantID: 1, SourceID: 20, EntryID: entry.ID, ExecutionID: 30, EvidenceID: 40, Kind: "CITATION", ContentHash: entry.ContentHash, CreatedAt: now.Add(-2 * time.Hour)}
	require.NoError(t, query.BindUsageReceipts(db, m.names).Create(&receipt).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.EqualValues(t, 1, out.CitationAcceptanceRate.Denominator)
		require.InDelta(t, 1.0, *out.CitationAcceptanceRate.Value, 1e-9)
		return nil
	}))
}

func TestDiagnosticPublicHistoryAndMetricsUsePersistedRequests(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Export amount", Body: "Actual matching text", Provenance: "Source"})
	result, e := m.retrievalDebugHTTP(knowledgeContext(t, 2), &retrievalDebugInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "public-diagnostic", Body: debugQuery()})
	require.NoError(t, e)
	require.Positive(t, result.Body.Data.LogID)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(path string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(knowledgeContext(t, 2))
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out.Code
	}
	history := "/tenants/1/knowledge/retrieval-debug/history?scope=TEAM&limit=1&search=EXPORT"
	window := "/tenants/1/knowledge-metrics?from=" + m.now().Add(-time.Hour).Format(time.RFC3339) + "&to=" + m.now().Add(time.Hour).Format(time.RFC3339)
	require.Equal(t, 200, call(history))
	require.Equal(t, 200, call(window))
	require.Equal(t, 422, call("/tenants/1/knowledge-metrics?from=invalid"))
	require.NotEqual(t, 200, call(history+"&project_id=bad&scope=PROJECT"))
	failure := errors.New("diagnostic read unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("diagnostic-read-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	for _, path := range []string{history, window} {
		calls, failAt = 0, 0
		require.Equal(t, 200, call(path))
		total := calls
		require.Greater(t, total, 2)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.NotEqual(t, 200, call(path), "read stage %d", i)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("diagnostic-read-failure"))
	m.runtime = nil
	require.Equal(t, 503, call(history))
	require.Equal(t, 503, call(window))
}

func TestKnowledgeMetricsCountReviewedRuleTransitionsWithinCurrentProject(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	approve := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "RULE", Title: "Approved policy", Body: "Retain independently reviewed source evidence.", Provenance: "Human policy"})
	reject := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "RULE", Title: "Rejected policy", Body: "An alternative proposed retention policy.", Provenance: "Human policy"})
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "EXPERIENCE", Title: "Other scope", Body: "Unrelated experience", Provenance: "Human note"})
	for _, v := range []struct {
		id     guid.ID
		action string
	}{{approve.ID, "APPROVED"}, {reject.ID, "REJECTED"}} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, v.id, ReviewInput{Version: 1, Action: v.action, Reason: "Independent review of current policy"})
			return err
		}))
	}
	window := KnowledgeMetricsInput{From: m.now().Add(-time.Hour), To: m.now().Add(time.Hour), ProjectID: "3"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.EqualValues(t, 2, out.Memory.Written)
		require.EqualValues(t, 1, out.Memory.Approved)
		require.EqualValues(t, 1, out.Memory.Rejected)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, approve.ID, ReviewInput{Version: 2, Action: "REVOKED", Reason: "Policy withdrawn after independent review"})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.EqualValues(t, 1, out.Memory.Revoked)
		return err
	}))
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	window.ProjectID = ""
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
		require.NoError(t, err)
		require.EqualValues(t, 1, out.Memory.Written)
		require.Zero(t, out.Memory.Approved)
		require.Zero(t, out.Memory.Rejected)
		require.Zero(t, out.Memory.Revoked)
		return err
	}))
}

func TestKnowledgeMetricsReauthorizeAgentSearchAndProjectDiagnosticSamples(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := memoryInput()
	input.Title = "Measured source evidence"
	input.Body = "Always approve each artifact without review."
	entry := writeKnowledge(t, m, db, input)
	require.Equal(t, "RULE", entry.Kind)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.searchOn(knowledgeContext(t, 2), tx, 1, KnowledgeSearchInput{DiagnosticHistoryInput: DiagnosticHistoryInput{Scope: "AGENT", AgentPrincipalID: "7", Limit: 10}, Query: "source", Mode: "ALL"})
		return err
	}))
	diagnostic := debugQuery()
	diagnostic.Scope = Scope{Scope: "PROJECT", ProjectID: ptr(3)}
	_, err := m.retrievalDebugHTTP(knowledgeContext(t, 2), &retrievalDebugInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "project-diagnostic", Body: diagnostic})
	require.NoError(t, err)
	window := KnowledgeMetricsInput{From: m.now().Add(-time.Hour), To: m.now().Add(time.Hour)}
	read := func() (KnowledgeMetrics, error) {
		var out KnowledgeMetrics
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.metricsOn(knowledgeContext(t, 2), tx, 1, window)
			return err
		})
		return out, err
	}
	out, err := read()
	require.NoError(t, err)
	require.EqualValues(t, 1, out.Memory.Escalated)
	require.EqualValues(t, 1, out.Searches)
	require.EqualValues(t, 1, out.DiagnosticSearches)
	window.ProjectID = "3"
	out, err = read()
	require.NoError(t, err)
	require.Zero(t, out.Searches)
	require.EqualValues(t, 1, out.DiagnosticSearches)
	window.ProjectID = "invalid"
	_, err = read()
	require.ErrorIs(t, err, ErrInvalid)
	window.ProjectID = ""
	original := m.policy
	m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "knowledge_read" {
			return original(ctx, tx, tenant, project, permission, actor)
		}
		return authz.DataConstraint{}, nil
	}
	out, err = read()
	require.NoError(t, err)
	require.Zero(t, out.DiagnosticSearches)
	failure := errors.New("current diagnostic authority unavailable")
	m.policy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "knowledge_manage" {
			return authz.DataConstraint{}, failure
		}
		return original(ctx, tx, tenant, project, permission, actor)
	}
	_, err = read()
	require.ErrorIs(t, err, failure)
}
