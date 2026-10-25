package knowledge

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/knowledge/query"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMemoryListOffsetFiltersStatsAndCurrentAuthority(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for i := 0; i < 4; i++ {
		in := memoryInput()
		in.Title = fmt.Sprintf("Experience %d", i)
		writeKnowledge(t, m, db, in)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.memoryListOn(knowledgeContext(t, 2), tx, 1, 7, MemoryListInput{Limit: 2, Offset: 2, Search: "Experience", State: "ACTIVE"})
		require.NoError(t, e)
		require.Equal(t, 4, page.Total)
		require.Len(t, page.Items, 2)
		require.Equal(t, 4, page.Stats.ActiveExperience)
		require.Equal(t, 0, page.Stats.TodayWrites)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.memoryListOn(knowledgeContext(t, 2), tx, 1, 7, MemoryListInput{Limit: 201})
		return e
	}), ErrInvalid)
	m.tenantPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, _ string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.memoryListOn(knowledgeContext(t, 3), tx, 1, 7, MemoryListInput{Limit: 50})
		require.Empty(t, page.Items)
		return e
	}), ErrForbidden)
}

func TestMemoryHTTPDedupStatusReplayAndSecretDenialAudit(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	router := chi.NewRouter()
	api := humachi.New(router, huma.DefaultConfig("test", "1"))
	require.NoError(t, m.RegisterHTTP(t.Context(), api))
	denied := []audit.DomainEvent{}
	appender := m.audit
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "knowledge.memory_write_denied" {
			denied = append(denied, event)
		}
		return appender(ctx, tx, event)
	}
	call := func(path, key string, body any) *httptest.ResponseRecorder {
		raw, e := json.Marshal(body)
		require.NoError(t, e)
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))).WithContext(knowledgeContext(t, 2))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		result := httptest.NewRecorder()
		router.ServeHTTP(result, request)
		return result
	}
	body := memoryInput()
	first := call("/tenants/1/knowledge", "first-write", body)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	var created struct {
		Data EntryView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &created))
	duplicate := call("/tenants/1/knowledge", "duplicate-write", body)
	require.Equal(t, http.StatusOK, duplicate.Code, duplicate.Body.String())
	var dedup struct {
		Data EntryView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(duplicate.Body.Bytes(), &dedup))
	require.True(t, dedup.Data.Deduplicated)
	require.Equal(t, created.Data.ID, dedup.Data.ID)
	replay := call("/tenants/1/knowledge", "duplicate-write", body)
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	secret := "password=not-a-real-credential"
	body.Title = "Denied secret"
	body.Body = secret
	failure := call("/tenants/1/knowledge", "secret-write", body)
	require.Equal(t, http.StatusUnprocessableEntity, failure.Code, failure.Body.String())
	require.Contains(t, failure.Body.String(), "MEMORY_SECRET")
	edit := call("/tenants/1/memories/"+created.Data.ID.String()+"/edits", "secret-edit", MemoryMutation{Version: 1, Title: "Secret edit", Body: secret, TTLDays: 30, Reason: "Reject secret replacement"})
	require.Equal(t, http.StatusUnprocessableEntity, edit.Code, edit.Body.String())
	require.Len(t, denied, 2)
	for _, event := range denied {
		encoded, e := json.Marshal(event.Detail)
		require.NoError(t, e)
		require.NotContains(t, string(encoded), secret)
		require.NotContains(t, event.Detail, "body")
		require.Contains(t, event.Detail, "rule_name")
	}
	q := query.BindEntries(db, m.names)
	rows, e := q.WithContext(t.Context()).Find()
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, "ACTIVE", rows[0].State)
	reviews := query.BindReviews(db, m.names)
	history, e := reviews.WithContext(t.Context()).Where(reviews.EntryID.Eq(created.Data.ID)).Find()
	require.NoError(t, e)
	require.Len(t, history, 1)
}

func TestMemoryPublicListHistoryAndReplacementRetainLifecycleFacts(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	original := writeKnowledge(t, m, db, memoryInput())
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
	page := call("GET", "/tenants/1/agents/7/memories?kind=EXPERIENCE&limit=1&search=", "", nil)
	require.Equal(t, 200, page.Code, page.Body.String())
	require.Contains(t, page.Body.String(), original.ID.String())
	path := "/tenants/1/memories/" + original.ID.String()
	mutation := MemoryMutation{Version: original.Version, Title: "Corrected experience", Body: "Keep the corrected verification evidence.", TTLDays: 14, Reason: "Correct original guidance"}
	edited := call("POST", path+"/edits", "replace-memory", mutation)
	require.Equal(t, 201, edited.Code, edited.Body.String())
	var replacement struct{ Data EntryView }
	require.NoError(t, json.Unmarshal(edited.Body.Bytes(), &replacement))
	require.Equal(t, original.ID, *replacement.Data.SupersedesID)
	replay := call("POST", path+"/edits", "replace-memory", mutation)
	require.Equal(t, 201, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("Idempotent-Replayed"))
	history := call("GET", path+"/reviews?search=SUPERSEDED&limit=1", "", nil)
	require.Equal(t, 200, history.Code, history.Body.String())
	var h struct{ Data MemoryHistoryPage }
	require.NoError(t, json.Unmarshal(history.Body.Bytes(), &h))
	require.Equal(t, 1, h.Data.Total)
	require.Len(t, h.Data.Items, 1)
	require.Equal(t, "SUPERSEDED", h.Data.Items[0].ToState)
	next := call("GET", path+"/reviews?search=SUPERSEDED&limit=1&offset=1", "", nil)
	require.Equal(t, 200, next.Code, next.Body.String())
	require.NoError(t, json.Unmarshal(next.Body.Bytes(), &h))
	require.Empty(t, h.Data.Items)
	require.Equal(t, 1, h.Data.Total)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	denied := call("GET", path+"/reviews", "", nil)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}
func TestMemoryReadStorageFailuresReturnNoSuccessfulPartialPage(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, memoryInput())
	failure := errors.New("memory read unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("memory-read-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	list := MemoryListInput{MemoryAgentInput: MemoryAgentInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, AgentPrincipalID: "7"}, Limit: 50, Kind: "EXPERIENCE", ProjectID: "3"}
	history := MemoryReviewListInput{MemoryIDInput: MemoryIDInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, MemoryID: entry.ID.String()}, Limit: 50, Search: "CREATED"}
	for _, run := range []func() error{func() error { _, e := m.memoryListHTTP(knowledgeContext(t, 2), &list); return e }, func() error { _, e := m.memoryHistoryHTTP(knowledgeContext(t, 2), &history); return e }} {
		calls, failAt = 0, 0
		require.NoError(t, run())
		total := calls
		require.Greater(t, total, 1)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.Error(t, run(), "read stage %d", i)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("memory-read-failure"))
	for _, in := range []MemoryListInput{{}, {MemoryAgentInput: MemoryAgentInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, AgentPrincipalID: "invalid"}}} {
		_, e := m.memoryListHTTP(knowledgeContext(t, 2), &in)
		require.Error(t, e)
	}
	for _, in := range []MemoryReviewListInput{{}, {MemoryIDInput: MemoryIDInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, MemoryID: "bad"}, Limit: 1}, {MemoryIDInput: history.MemoryIDInput, Limit: 201}} {
		_, e := m.memoryHistoryHTTP(knowledgeContext(t, 2), &in)
		require.Error(t, e)
	}
	for _, in := range []MemoryIDInput{{}, {KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, MemoryID: "bad"}} {
		_, e := m.memoryEditHTTP(knowledgeContext(t, 2), &memoryEditHTTPInput{MemoryIDInput: in})
		require.Error(t, e)
		_, e = m.memoryPromoteHTTP(knowledgeContext(t, 2), &memoryPromoteHTTPInput{MemoryIDInput: in})
		require.Error(t, e)
	}
	m.runtime = nil
	_, e := m.memoryListHTTP(knowledgeContext(t, 2), &list)
	require.Error(t, e)
	_, e = m.memoryHistoryHTTP(knowledgeContext(t, 2), &history)
	require.Error(t, e)
}
