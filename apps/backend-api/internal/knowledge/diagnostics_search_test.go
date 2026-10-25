package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKnowledgeSearchReadOnlyPermissionPaginationAndScope(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for _, title := range []string{"Export amount one", "Export amount two"} {
		writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: title, Body: "Actual source", Provenance: "Source"})
	}
	m.tenantPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "knowledge_read"}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		in := KnowledgeSearchInput{DiagnosticHistoryInput: DiagnosticHistoryInput{Scope: "TEAM", Offset: 1, Limit: 1}, Query: "export", Mode: "ALL"}
		page, err := m.searchOn(knowledgeContext(t, 2), tx, 1, in)
		require.NoError(t, err)
		require.Equal(t, 2, page.Total)
		require.Len(t, page.Items, 1)
		in.Offset = 99
		page, err = m.searchOn(knowledgeContext(t, 2), tx, 1, in)
		require.NoError(t, err)
		require.Empty(t, page.Items)
		in.Limit = 201
		_, err = m.searchOn(knowledgeContext(t, 2), tx, 1, in)
		require.ErrorIs(t, err, ErrInvalid)
		return nil
	}))
}

func TestKnowledgeSearchSamplesPersistNotApplicableFieldsAsNull(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.searchOn(knowledgeContext(t, 2), tx, 1, KnowledgeSearchInput{DiagnosticHistoryInput: DiagnosticHistoryInput{Scope: "TEAM", Limit: 50}, Query: "missing", Mode: "ALL"})
		return err
	}))
	var sample model.RuntimeMetricSample
	require.NoError(t, query.BindRuntimeMetricSamples(db, m.names).First(&sample).Error)
	require.Nil(t, sample.ProjectID)
	require.Nil(t, sample.ExecutionID)
	require.Nil(t, sample.EntryID)
	require.Nil(t, sample.Outcome)
	require.NotNil(t, sample.LatencyMS)
	require.False(t, sample.Hit)
	var nullRows int64
	require.NoError(t, query.BindRuntimeMetricSamples(db, m.names).Where(map[string]any{"id": sample.ID, "project_id": nil, "execution_id": nil, "entry_id": nil, "outcome": nil}).Count(&nullRows).Error)
	require.EqualValues(t, 1, nullRows)
	// Direct schema enforcement rejects the old zero/empty sentinels, rather than
	// relying on Go pointers alone to preserve the database contract.
	for _, change := range []func(*model.RuntimeMetricSample){
		func(r *model.RuntimeMetricSample) { r.ProjectID = ptr(0) },
		func(r *model.RuntimeMetricSample) { r.ExecutionID = ptr(0) },
		func(r *model.RuntimeMetricSample) { r.EntryID = ptr(0) },
		func(r *model.RuntimeMetricSample) { empty := ""; r.Outcome = &empty },
	} {
		invalid := sample
		id, err := m.nextSourceID()
		require.NoError(t, err)
		invalid.ID = id
		invalid.EvidenceID = id
		change(&invalid)
		require.Error(t, query.BindRuntimeMetricSamples(db, m.names).Create(&invalid).Error)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.searchOn(knowledgeContext(t, 2), tx, 1, KnowledgeSearchInput{DiagnosticHistoryInput: DiagnosticHistoryInput{Scope: "PROJECT", ProjectID: "3", Limit: 50}, Query: "missing", Mode: "ALL"})
		return err
	}))
	var projectSample model.RuntimeMetricSample
	require.NoError(t, query.BindRuntimeMetricSamples(db, m.names).Where(map[string]any{"project_id": 3}).First(&projectSample).Error)
	require.Equal(t, ptr(3), projectSample.ProjectID)
	require.Nil(t, projectSample.ExecutionID)
	require.Nil(t, projectSample.EntryID)
	require.Nil(t, projectSample.Outcome)
}

func TestManualKnowledgeSearchPublicRoutePersistsOnlyCompleteObservations(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Export amount", Body: "Actual source", Provenance: "Source"})
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(path string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil).WithContext(knowledgeContext(t, 2))
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out.Code
	}
	path := "/tenants/1/knowledge/search?scope=TEAM&query=amount&limit=1"
	require.Equal(t, 200, call(path))
	failure := errors.New("manual search persistence unavailable")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("manual-search-storage", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("manual-search-storage", probe))
	require.Equal(t, 200, call(path))
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.NotEqual(t, 200, call(path), "manual search stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("manual-search-storage"))
	require.NoError(t, db.Callback().Create().Remove("manual-search-storage"))
	var count int64
	require.NoError(t, query.BindRuntimeMetricSamples(db, m.names).Where("kind = ?", "SEARCH").Count(&count).Error)
	require.EqualValues(t, 2, count)
	require.Equal(t, 422, call("/tenants/1/knowledge/search?scope=PROJECT&project_id=bad&query=amount"))
	m.runtime = nil
	require.Equal(t, 503, call(path))
}

func TestManualSearchObservationAllocationAndWriteFailuresCannotCommitSample(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := KnowledgeSearchInput{DiagnosticHistoryInput: DiagnosticHistoryInput{Scope: "TEAM", Limit: 10}, Query: "evidence", Mode: "ALL"}
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.searchOn(knowledgeContext(t, 2), tx, 1, input); return err })
	}
	owner := m.ids
	m.ids = nil
	require.ErrorIs(t, run(), ErrUnavailable)
	m.ids = owner
	failure := errors.New("search observation unavailable")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("search-observation-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.RuntimeMetricSamples.String() {
			tx.AddError(failure)
		}
	}))
	require.ErrorIs(t, run(), failure)
	require.NoError(t, db.Callback().Create().Remove("search-observation-failure"))
	var count int64
	require.NoError(t, query.BindRuntimeMetricSamples(db, m.names).Count(&count).Error)
	require.Zero(t, count)
}
