package knowledge

import (
	"7link.com/platform/internal/knowledge/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNativeSearchUsesCanonicalChunksHighlightsFiltersAndRevocation(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	source = syncKnowledgeSource(t, m, db, source, nil, false)
	var result NativeSearchResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "signed evidence", MatchMode: "ALL", Limit: 50}, true)
		return err
	}))
	require.Len(t, result.Results, 1)
	require.Equal(t, source.ID, result.Results[0].SourceID)
	require.NotEmpty(t, result.Results[0].Highlights)
	require.NotZero(t, result.RetrievalLogID)
	require.Contains(t, []string{"K1", "K1_FALLBACK"}, result.Mode)
	if result.Mode == "K1_FALLBACK" {
		require.Equal(t, "NATIVE_FTS_UNAVAILABLE_SQLITE", result.FallbackReason)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		none, err := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 50, Tags: []string{"missing"}}, false)
		require.NoError(t, err)
		require.Empty(t, none.Results)
		none, err = m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "signed missing", MatchMode: "ALL", Limit: 50}, false)
		require.NoError(t, err)
		require.Empty(t, none.Results)
		matched, err := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "signed missing", MatchMode: "ANY", Limit: 50}, false)
		require.NoError(t, err)
		require.Len(t, matched.Results, 1)
		page, err := m.listOn(knowledgeContext(t, 2), tx, 1, knowledgeListInput{Scope: "TEAM", State: "ACTIVE", Limit: 50, Query: "signed evidence"})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		_, err = m.nativeSearchOn(knowledgeContext(t, 2), tx, 99, NativeSearchRequest{Query: "evidence", Limit: 50}, false)
		require.ErrorIs(t, err, ErrInvalid)
		return nil
	}))
	q := query.BindSources(db, m.names)
	require.NoError(t, q.WithContext(knowledgeContext(t, 2)).Where(q.ID.Eq(source.ID)).UnderlyingDB().Update("state", "INVALIDATED").Error)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.reauthorizeNativeResultsOn(knowledgeContext(t, 2), tx, 1, result) }), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		current, err := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 50}, false)
		require.Empty(t, current.Results)
		return err
	}))
}
func TestNativeHighlightRunesDenseWindowMergedBigramsAndPlainText(t *testing.T) {
	text := strings.Repeat("空", 260) + "数据库 检索<script>"
	snippet, highlights := highlightedSnippet(text, []string{"数据", "据库", "检索"})
	require.Len(t, []rune(snippet), 240)
	require.Len(t, highlights, 2)
	runes := []rune(snippet)
	require.Equal(t, "数据库", string(runes[highlights[0].Start:highlights[0].End]))
	require.Equal(t, "检索", string(runes[highlights[1].Start:highlights[1].End]))
	body, err := json.Marshal(NativeSearchHit{Snippet: snippet, Highlights: highlights})
	require.NoError(t, err)
	require.NotContains(t, string(body), "<mark>")
	full, rangeFull := highlightedSnippet("ＡＢＣ evidence", []string{"abc"})
	require.Equal(t, "ＡＢＣ evidence", full)
	require.Equal(t, []Highlight{{0, 3}}, rangeFull)
}
func TestNativeSearchReceiptsReauthorizeAndHideScoresAfterManagementRevocation(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	syncKnowledgeSource(t, m, db, source, nil, false)
	var result NativeSearchResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 50, Explain: true}, true)
		return err
	}))
	require.Len(t, result.Results, 1)
	require.NotNil(t, result.Results[0].Scores)
	m.tenantPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "knowledge_read"}, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var saved NativeSearchResult
		require.NoError(t, m.nativeSearchLogOn(knowledgeContext(t, 2), tx, 1, result.RetrievalLogID, &saved))
		require.Nil(t, saved.Results[0].Scores)
		page, err := m.nativeSearchLogsOn(knowledgeContext(t, 2), tx, 1, NativeSearchLogInput{Limit: 50, Search: "evidence"})
		require.NoError(t, err)
		require.Equal(t, 1, page.Total)
		require.Len(t, page.Items, 1)
		require.ErrorIs(t, m.nativeSearchLogOn(knowledgeContext(t, 3), tx, 1, result.RetrievalLogID, &saved), ErrNotFound)
		_, err = m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "never-seen", Limit: 50, Explain: true}, false)
		require.ErrorIs(t, err, ErrForbidden)
		return nil
	}))
}
func TestNativeSearchDebugReflectsActualNativeOrFallbackMode(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	syncKnowledgeSource(t, m, db, source, nil, false)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		result, err := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, RetrievalDebugQuery{Scope: Scope{Scope: "TEAM"}, Query: "signed", Mode: "ALL", TopK: 10})
		require.NoError(t, err)
		require.Len(t, result.Candidates, 1)
		require.Contains(t, []string{"K1", "K1_FALLBACK"}, result.Engine)
		require.Equal(t, result.Engine == "K1", result.Plan.NativeSQLAvailable)
		require.Equal(t, guid.ID(2), result.ActorID)
		return nil
	}))
}

func TestNativeCorpusBoundsEntryBindsWithoutLosingLargeCorpusChunks(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	source = syncKnowledgeSource(t, m, db, source, nil, false)
	q := query.BindChunks(db, m.names)
	original, e := q.WithContext(t.Context()).Where(q.SourceID.Eq(source.ID)).First()
	require.NoError(t, e)
	entriesQuery := query.BindEntries(db, m.names)
	originalEntry, e := entriesQuery.WithContext(t.Context()).Where(entriesQuery.ID.Eq(original.EntryID)).First()
	require.NoError(t, e)
	entries := make([]model.Entry, 1500)
	chunks := make([]model.Chunk, 1500)
	for i := range chunks {
		chunks[i] = *original
		chunks[i].ID = guid.ID(100000 + i)
		chunks[i].Ordinal = i + 1
		chunks[i].Content = original.Content + strconv.Itoa(i)
		chunks[i].ContentHash = sourceHash(chunks[i].Content)
		entries[i] = *originalEntry
		entries[i].ID = guid.ID(200000 + i)
		entries[i].Body = chunks[i].Content
		entries[i].ContentHash = chunks[i].ContentHash
		chunks[i].EntryID = entries[i].ID
	}
	require.NoError(t, db.Table(m.names.Entries.String()).CreateInBatches(&entries, 25).Error)
	require.NoError(t, db.Table(m.names.Chunks.String()).CreateInBatches(&chunks, 25).Error)
	maxBinds := 0
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:bounded-corpus-binds", func(tx *gorm.DB) { maxBinds = max(maxBinds, len(tx.Statement.Vars)) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		corpus, e := m.nativeCorpusOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{})
		require.NoError(t, e)
		require.Len(t, corpus.chunks, 1501)
		require.Len(t, corpus.entries, 1501)
		return e
	}))
	require.LessOrEqual(t, maxBinds, 502, "large corpus reads must stay below conservative SQL bind limits")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		first, err := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 200, Explain: true}, true)
		require.NoError(t, err)
		require.Equal(t, 200, first.TotalCandidates, "candidate cap bounds persisted search receipts")
		require.Len(t, first.Results, 200)
		later, err := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 50, Offset: 150}, false)
		require.NoError(t, err)
		require.Len(t, later.Results, 50)
		for i, hit := range later.Results {
			require.Equal(t, first.Results[150+i].ChunkID, hit.ChunkID)
			require.NotNil(t, first.Results[150+i].Scores)
		}
		empty, err := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 1, Offset: 201}, false)
		require.NoError(t, err)
		require.Empty(t, empty.Results)
		return nil
	}))
}
func TestNativeCoverageEligibilityMatchesRetrievalIncludingExpiryAndInactiveEntries(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	at := m.now()
	m.now = func() time.Time { return at }
	for i := range 4 {
		in := sourceDraft()
		in.Locator += "/" + string(rune('a'+i))
		source := bindKnowledgeSource(t, m, db, in)
		source = syncKnowledgeSource(t, m, db, source, nil, false)
		q := query.BindChunks(db, m.names)
		chunk, e := q.WithContext(t.Context()).Where(q.SourceID.Eq(source.ID)).First()
		require.NoError(t, e)
		update := map[string]any{}
		if i == 1 {
			update["kind"] = "EXPERIENCE"
			update["expires_at"] = at
		}
		if i == 2 {
			update["state"] = "REVOKED"
		}
		if i == 3 {
			update["kind"] = "EXPERIENCE"
			update["expires_at"] = at.Add(time.Hour)
		}
		if len(update) > 0 {
			require.NoError(t, db.Table(m.names.Entries.String()).Where("id = ?", chunk.EntryID).Updates(update).Error)
		}
	}
	facts, e := query.EligibleChunkFacts(db, m.names, 1, at)
	require.NoError(t, e)
	require.Len(t, facts, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		corpus, e := m.nativeCorpusOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{})
		require.NoError(t, e)
		require.Len(t, corpus.chunks, len(facts))
		return e
	}))
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	facts, e = query.EligibleChunkFacts(db, m.names, 1, at)
	require.NoError(t, e)
	require.Len(t, facts, 2, "coverage remains tenant-wide metadata independent of operator visibility")
}

func TestNativeSearchPublicRoutesPersistReplayAndRecheckCurrentSourceAuthority(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(method, path, key string, body any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw))).WithContext(knowledgeContext(t, 2))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, r)
		return out
	}
	request := NativeSearchRequest{Query: "signed evidence", MatchMode: "ALL", Limit: 10}
	first := call(http.MethodPost, "/tenants/1/knowledge-searches", "actual-search", request)
	require.Equal(t, 201, first.Code, first.Body.String())
	var created struct{ Data NativeSearchResult }
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &created))
	require.Len(t, created.Data.Results, 1)
	require.Positive(t, created.Data.RetrievalLogID)
	replay := call(http.MethodPost, "/tenants/1/knowledge-searches", "actual-search", request)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	var replayed struct{ Data NativeSearchResult }
	require.NoError(t, json.Unmarshal(replay.Body.Bytes(), &replayed))
	require.Equal(t, created.Data, replayed.Data)
	path := "/tenants/1/knowledge-retrieval-logs/" + created.Data.RetrievalLogID.String()
	read := call(http.MethodGet, path, "", nil)
	require.Equal(t, 200, read.Code, read.Body.String())
	require.Contains(t, read.Body.String(), source.ID.String())
	listed := call(http.MethodGet, "/tenants/1/knowledge-retrieval-logs?limit=1&search=evidence", "", nil)
	require.Equal(t, 200, listed.Code, listed.Body.String())
	require.Contains(t, listed.Body.String(), created.Data.RetrievalLogID.String())
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied := call(http.MethodPost, "/tenants/1/knowledge-searches", "actual-search", request)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	denied = call(http.MethodGet, path, "", nil)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}
func TestNativeSearchStorageFailuresNeverPersistPartialSearchReceipts(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	failure := errors.New("current native corpus unavailable")
	rollback := errors.New("rollback native search")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("native-search-storage", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("native-search-storage", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 10}, true)
			if e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "native corpus stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("native-search-storage"))
	require.NoError(t, db.Callback().Create().Remove("native-search-storage"))
	var n int64
	require.NoError(t, query.BindSearchLogs(db, m.native).Count(&n).Error)
	require.Zero(t, n)
}

func TestNativeCorpusCanonicalScopeTagDateAndCursorFilters(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	now := m.now()
	scopes := []Scope{{Scope: "TEAM"}, {Scope: "PROJECT", ProjectID: ptr(3)}, {Scope: "AGENT", AgentPrincipalID: ptr(7)}, {Scope: "AGENT", ProjectID: ptr(3), AgentPrincipalID: ptr(7)}}
	sources := []SourceView{}
	for i, scope := range scopes {
		in := sourceDraft()
		in.Scope = scope
		in.Locator = fmt.Sprintf("/canonical-filter-%d", i)
		in.Title = fmt.Sprintf("Evidence %d", i)
		source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, in), nil, false)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			source, e = m.sourceMetadataOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceMetadataInput{Version: source.Version, Title: in.Title, Tags: []string{"verified"}})
			return e
		}))
		sources = append(sources, source)
	}
	run := func(in NativeSearchRequest) NativeSearchResult {
		var out NativeSearchResult
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, in, false)
			return e
		}))
		return out
	}
	all := run(NativeSearchRequest{Query: "evidence", Limit: 20, Explain: true})
	require.Len(t, all.Results, 4)
	for _, hit := range all.Results {
		require.NotNil(t, hit.Scores)
	}
	after, before := now.Add(-time.Hour), now.Add(time.Hour)
	filtered := run(NativeSearchRequest{Query: "evidence", Limit: 20, Scopes: []string{"PROJECT"}, ProjectIDs: []guid.ID{3}, SourceKinds: []string{"UPLOAD"}, Tags: []string{"verified"}, UpdatedAfter: &after, UpdatedBefore: &before})
	require.Len(t, filtered.Results, 1)
	require.Equal(t, sources[1].ID, filtered.Results[0].SourceID)
	missing := run(NativeSearchRequest{Query: "evidence", Limit: 20, Tags: []string{"absent"}})
	require.Empty(t, missing.Results)
	scoped := run(NativeSearchRequest{Query: "evidence", Limit: 20, Scopes: []string{"AGENT"}, AgentIDs: []guid.ID{7}, exactScope: &scopes[2]})
	require.Len(t, scoped.Results, 1)
	require.Equal(t, sources[2].ID, scoped.Results[0].SourceID)
	other := run(NativeSearchRequest{Query: "evidence", Limit: 20, ProjectIDs: []guid.ID{4}})
	for _, hit := range other.Results {
		require.Nil(t, hit.ProjectID)
	}
	var page KnowledgePage
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		page, e = m.listSearchOn(knowledgeContext(t, 2), tx, 1, knowledgeListInput{Scope: "AGENT", AgentPrincipalID: "7", State: "ACTIVE", Query: "evidence", Limit: 1})
		return e
	}))
	require.Len(t, page.Items, 1)
	for _, in := range []NativeSearchRequest{{Scopes: []string{"bad"}}, {ProjectIDs: []guid.ID{0}}, {AgentIDs: []guid.ID{-1}}, {Tags: []string{""}}, {Scopes: []string{"TEAM", "TEAM", "TEAM", "TEAM"}}, {UpdatedAfter: &before, UpdatedBefore: &after}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.nativeCorpusOn(knowledgeContext(t, 2), tx, 1, in); return e }), ErrInvalid)
	}
	q := query.BindSources(db, m.names)
	_, e := q.WithContext(t.Context()).Where(q.ID.Eq(sources[0].ID)).Update(q.TagsJSON, `{}`)
	require.NoError(t, e)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.nativeCorpusOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{})
		return e
	}), ErrUnavailable)
}

func TestNativeReceiptReadsPaginationStorageFailuresAndCorruptHistory(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	var search NativeSearchResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		search, e = m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 10, Explain: true}, true)
		return e
	}))
	failure := errors.New("search receipt storage unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("search-receipt-query", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	var observed NativeSearchLogPage
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			page, e := m.nativeSearchLogsOn(knowledgeContext(t, 2), tx, 1, NativeSearchLogInput{Purpose: "DEBUG", Search: "EVIDENCE", Limit: 1})
			observed = page
			if e == nil && failAt == 0 {
				require.Equal(t, 1, page.Total)
				require.Len(t, page.Items, 1)
			}
			return e
		})
	}
	require.NoError(t, run())
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		e := run()
		if e != nil {
			require.ErrorIs(t, e, failure, "receipt read stage %d", i)
		} else {
			require.Empty(t, observed.Items, "failed source revalidation hides receipt")
			require.Zero(t, observed.Total)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("search-receipt-query"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.nativeSearchLogsOn(knowledgeContext(t, 2), tx, 1, NativeSearchLogInput{Purpose: "DEBUG", Offset: 1, Limit: 1})
		require.Equal(t, 1, page.Total)
		require.Empty(t, page.Items)
		return e
	}))
	for _, in := range []NativeSearchLogInput{{Limit: 0}, {Limit: 1, Offset: -1}, {Limit: 1, Purpose: "UNKNOWN"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.nativeSearchLogsOn(knowledgeContext(t, 2), tx, 1, in); return e }), ErrInvalid)
	}
	require.NoError(t, query.BindSearchLogs(db, m.native).Where("id = ?", search.RetrievalLogID).Update("request_json", `[]`).Error)
	require.Error(t, run())
}

func TestNativeSearchPublicFailuresNeverReturnReceiptsOrWriteDiagnostics(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	call := func(method, path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(`{"query":"evidence","limit":10,"match_mode":"ALL","offset":0,"explain":false}`)).WithContext(knowledgeContext(t, 2))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	for _, path := range []string{"/tenants/9223372036854775808/knowledge-searches", "/tenants/9223372036854775808/knowledge-retrieval-logs", "/tenants/9223372036854775808/knowledge-retrieval-logs/1", "/tenants/1/knowledge-retrieval-logs/9223372036854775808"} {
		method := "GET"
		if strings.HasSuffix(path, "knowledge-searches") {
			method = "POST"
		}
		out := call(method, path, "valid-search-key")
		require.Equal(t, 422, out.Code, "%s: %s", path, out.Body.String())
	}
	replay := m.replay
	m.replay = nil
	unavailable := call("POST", "/tenants/1/knowledge-searches", "valid-search-key")
	require.Equal(t, 503, unavailable.Code, unavailable.Body.String())
	m.replay = replay
	invalid := call("POST", "/tenants/1/knowledge-searches", " ")
	require.Equal(t, 422, invalid.Code, invalid.Body.String())
	m.runtime = nil
	for _, path := range []string{"/tenants/1/knowledge-searches", "/tenants/1/knowledge-retrieval-logs", "/tenants/1/knowledge-retrieval-logs/1"} {
		method := "GET"
		if strings.HasSuffix(path, "knowledge-searches") {
			method = "POST"
		}
		out := call(method, path, "valid-search-key")
		require.Equal(t, 503, out.Code, "%s: %s", path, out.Body.String())
	}
	var count int64
	require.NoError(t, query.BindSearchLogs(db, m.native).Count(&count).Error)
	require.Zero(t, count)
}

func TestNativeListSearchPagesCurrentIndexedEntriesAndFailsClosedOnStorage(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for _, title := range []string{"First evidence", "Second evidence"} {
		draft := sourceDraft()
		draft.Title = title
		syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, draft), nil, false)
	}
	input := knowledgeListInput{Scope: "TEAM", State: "ACTIVE", Query: "evidence", Limit: 1}
	var first KnowledgePage
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = m.listSearchOn(knowledgeContext(t, 2), tx, 1, input)
		require.Len(t, first.Items, 1)
		require.True(t, first.HasMore)
		require.NotEmpty(t, first.NextCursor)
		return err
	}))
	input.Cursor = first.NextCursor
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, err := m.listSearchOn(knowledgeContext(t, 2), tx, 1, input)
		require.Len(t, page.Items, 1)
		require.False(t, page.HasMore)
		require.NotEqual(t, first.Items[0].ID, page.Items[0].ID)
		return err
	}))
	input.Cursor = ""
	failure := errors.New("native list source unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("native-list-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { _, err := m.listSearchOn(knowledgeContext(t, 2), tx, 1, input); return err })
	}
	require.NoError(t, run())
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "native list query stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("native-list-failure"))
	for _, bad := range []knowledgeListInput{{Scope: "PROJECT", ProjectID: "bad", State: "ACTIVE", Query: "evidence", Limit: 1}, {Scope: "TEAM", State: "REVOKED", Query: "evidence", Limit: 1}, {Scope: "TEAM", State: "ACTIVE", Query: "evidence", Limit: 1, Cursor: "bad"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.listSearchOn(knowledgeContext(t, 2), tx, 1, bad); return err }), ErrInvalid)
	}
}
