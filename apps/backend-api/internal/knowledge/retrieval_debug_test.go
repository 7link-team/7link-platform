package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func debugQuery() RetrievalDebugQuery {
	return RetrievalDebugQuery{Scope: Scope{Scope: "TEAM"}, Query: "export amount", Mode: "ALL", TopK: 10}
}

func TestRetrievalDebugCJKFindsTermsInsideUnspacedText(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "平台权限管理", Body: "管理员维护平台权限管理策略。", Provenance: "真实维护文档"})
	for _, text := range []string{"权限", "权限管理 权限管理", "权"} {
		in := debugQuery()
		in.Query = text
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			out, err := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, in)
			if err != nil {
				return err
			}
			require.Len(t, out.Candidates, 1)
			require.Equal(t, entry.ID, out.Candidates[0].Entry.ID)
			require.Equal(t, 1, out.Candidates[0].Rank)
			return nil
		}))
	}
}

func TestRetrievalDebugActualSourcesExclusionsRankingAndScope(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return at }
	best := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Export amount", Body: "Export amount export amount", Provenance: "existing source"})
	other := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "EXPERIENCE", Title: "Export", Body: "Amount rounding", Provenance: "actual experience"})
	pending := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Export amount", Body: "Do not trust an unapproved rule", Provenance: "proposed rule"})
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "DOCUMENT", Title: "Secret export amount", Body: "Other scope", Provenance: "project source"})
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Unrelated", Body: "No matching terms", Provenance: "irrelevant source"})
	var out RetrievalDebugResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, debugQuery())
		return e
	}))
	require.Equal(t, 4, out.Scanned)
	require.False(t, out.Truncated)
	require.Equal(t, "LEXICAL_ENTRIES", out.Engine)
	require.Equal(t, best.ID, out.Candidates[0].Entry.ID)
	require.Equal(t, 6, out.Candidates[0].TextRaw)
	require.InDelta(t, 1.0, out.Candidates[0].T, 1e-12)
	require.InDelta(t, .85, out.Candidates[0].Final, 1e-12)
	require.Equal(t, other.ID, out.Candidates[1].Entry.ID)
	require.Equal(t, 2, out.Candidates[1].Rank)
	for _, c := range out.Candidates {
		if c.Entry.ID == pending.ID {
			require.Equal(t, "INACTIVE", c.Exclusion)
			require.Zero(t, c.Rank)
		}
		require.NotEqual(t, "PROJECT", c.Entry.Scope.Scope)
	}
	in := debugQuery()
	in.TopK = 1
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		result, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, in)
		require.Equal(t, "TOP_K", result.Candidates[1].Exclusion)
		return e
	}))
	in.Query = "Export unknown"
	in.Mode = "ANY"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		result, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, in)
		require.Equal(t, best.ID, result.Candidates[0].Entry.ID)
		return e
	}))
	in.Mode = "ALL"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		result, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, in)
		for _, c := range result.Candidates {
			require.NotEmpty(t, c.Exclusion)
		}
		return e
	}))
}

func TestRetrievalDebugValidationCurrentIAMAndNoUnauthorizedReads(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for _, change := range []func(*RetrievalDebugQuery){func(q *RetrievalDebugQuery) { q.Query = "" }, func(q *RetrievalDebugQuery) { q.Query = strings.Repeat("界", 501) }, func(q *RetrievalDebugQuery) { q.Query = "!!!" }, func(q *RetrievalDebugQuery) { q.Mode = "FAKE" }, func(q *RetrievalDebugQuery) { q.TopK = 0 }, func(q *RetrievalDebugQuery) { q.TopK = 201 }, func(q *RetrievalDebugQuery) { q.Scope.Scope = "OTHER" }} {
		in := debugQuery()
		change(&in)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, in); return e }), ErrInvalid)
	}
	require.Equal(t, []string{"export", "金额"}, retrievalTokens("EXPORT, 金额 export"))
	for _, actor := range []guid.ID{7, 8, 9} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.retrieveDebugOn(knowledgeContext(t, actor), tx, 1, debugQuery())
			return e
		}), ErrForbidden)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 2, debugQuery())
		return e
	}), ErrForbidden)
	// A missing table would fail if content were fetched before the management fence.
	require.NoError(t, db.Exec("DROP TABLE k_knowledge_entries").Error)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, debugQuery())
		return e
	}), ErrForbidden)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, errors.New("policy unavailable")
	}
	require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, debugQuery())
		return e
	}), "policy unavailable")
}

func TestRetrievalDebugIdempotencyAuditRollbackAndReplayRevocation(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Export amount", Body: "Actual content", Provenance: "real source"})
	in := &retrievalDebugInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "debug-one", Body: debugQuery()}
	a, e := m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	require.NoError(t, e)
	b, e := m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	require.NoError(t, e)
	require.Equal(t, a.Body.Data, b.Body.Data)
	var count int64
	require.NoError(t, db.Table("audit_probe").Where("event_type=?", "knowledge.retrieval_debugged").Count(&count).Error)
	require.EqualValues(t, 1, count)
	in.Body.Mode = "ANY"
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 409)
	in.Body.Mode = "ALL"
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 403)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, db.Exec("UPDATE k_knowledge_entries SET state='REVOKED',version=2 WHERE id=?", entry.ID).Error)
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 409)
	in.IdempotencyKey = "new-key"
	oldAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit offline") }
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 500)
	m.audit = oldAudit
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	require.NoError(t, e)
	require.NoError(t, db.Table("audit_probe").Where("event_type=?", "knowledge.retrieval_debugged").Count(&count).Error)
	require.EqualValues(t, 2, count)
	in.IdempotencyKey = ""
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 422)
}

func assertDebugStatus(t *testing.T, err error, status int) {
	t.Helper()
	var e huma.StatusError
	require.ErrorAs(t, err, &e)
	require.Equal(t, status, e.GetStatus())
}

func TestRetrievalDebugAgingThresholdsAndActualProjectAgentScopes(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return at }
	for _, scope := range []Scope{{Scope: "PROJECT", ProjectID: ptr(3)}, {Scope: "AGENT", AgentPrincipalID: ptr(7)}} {
		writeKnowledge(t, m, db, EntryInput{Scope: scope, Kind: "DOCUMENT", Title: "export amount", Body: "actual content", Provenance: "current source"})
		in := debugQuery()
		in.Scope = scope
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			out, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, in)
			require.NoError(t, e)
			require.Len(t, out.Candidates, 1)
			require.Equal(t, scope, out.Candidates[0].Entry.Scope)
			if scope.Scope == "AGENT" {
				require.InDelta(t, 1, out.Candidates[0].S, 1e-12)
			} else {
				require.InDelta(t, .7, out.Candidates[0].S, 1e-12)
			}
			return e
		}))
	}
	rows := []model.Entry{
		{ID: 1, Kind: "DOCUMENT", Title: strings.Repeat("export amount ", 50), State: "ACTIVE", CreatedAt: at},
		{ID: 2, Kind: "EXPERIENCE", Title: "export amount", Body: strings.Repeat("padding ", 2000), State: "ACTIVE", CreatedAt: at.AddDate(-10, 0, 0)},
		{ID: 3, Kind: "DOCUMENT", Title: strings.Repeat("export amount ", 3), Body: strings.Repeat("padding ", 250), State: "ACTIVE", CreatedAt: at.AddDate(-10, 0, 0)},
		{ID: 4, Kind: "RULE", Title: strings.Repeat("export amount ", 2), State: "ACTIVE", CreatedAt: at.AddDate(-10, 0, 0)},
	}
	// BM25 saturates repeated terms. A realistic corpus of short unrelated
	// documents makes padded weak matches exercise both frozen thresholds.
	for i := range 100 {
		rows = append(rows, model.Entry{ID: guid.ID(100 + i), Kind: "DOCUMENT", Title: "unrelated", State: "ACTIVE", CreatedAt: at})
	}
	for i := range rows {
		rows[i].TenantID, rows[i].Scope, rows[i].Version, rows[i].ContentHash, rows[i].CreatedBy = 1, "TEAM", 1, strings.Repeat("a", 64), 2
		rows[i].UpdatedAt = rows[i].CreatedAt
	}
	require.NoError(t, db.Table(m.names.Entries.String()).Create(&rows).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, debugQuery())
		require.NoError(t, e)
		byID := map[guid.ID]RetrievalDebugCandidate{}
		for _, c := range out.Candidates {
			byID[c.Entry.ID] = c
		}
		require.Equal(t, "TEXT_THRESHOLD", byID[2].Exclusion)
		require.Equal(t, "FINAL_THRESHOLD", byID[3].Exclusion)
		require.InDelta(t, 1, byID[4].W, 1e-12)
		require.InDelta(t, 1, byID[4].R, 1e-12)
		return e
	}))
}

func TestRetrievalDebugBoundedScanDeterministicTieBreaksAndFailClosedReads(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return at }
	rows := make([]model.Entry, 1002)
	for i := range rows {
		rows[i] = model.Entry{TenantID: 1, ID: guid.ID(i + 1), Scope: "TEAM", Kind: "DOCUMENT", State: "ACTIVE", Title: "export amount", Version: 1, ContentHash: strings.Repeat("a", 64), CreatedBy: 2, CreatedAt: at, UpdatedAt: at}
	}
	require.NoError(t, db.Table(m.names.Entries.String()).CreateInBatches(rows, 50).Error)
	read := func() (RetrievalDebugResult, error) {
		var out RetrievalDebugResult
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, debugQuery())
			return e
		})
		return out, e
	}
	a, e := read()
	require.NoError(t, e)
	require.True(t, a.Truncated)
	require.Equal(t, 1000, a.Scanned)
	require.Len(t, a.Candidates, 200)
	require.Equal(t, guid.ID(3), a.Candidates[0].Entry.ID) // newest 1000 selected, ID breaks equal-score ties
	b, e := read()
	require.NoError(t, e)
	require.Greater(t, a.Timings.TotalMS, 0.0)
	require.Greater(t, b.Timings.TotalMS, 0.0)
	// Ranking and content are deterministic; observed wall-clock timings vary.
	b.Timings = a.Timings
	require.Equal(t, a, b)
	require.True(t, sameDebugEntries(a, b))
	b.Candidates = b.Candidates[1:]
	require.False(t, sameDebugEntries(a, b))
	policy := m.tenantPolicy
	m.tenantPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission == "knowledge_manage"}, nil
	}
	_, e = read()
	require.ErrorIs(t, e, ErrForbidden) // management never substitutes for content read authority
	m.tenantPolicy = policy
	m.now = nil
	_, e = read()
	require.ErrorIs(t, e, ErrUnavailable)
	m.now = func() time.Time { return at }
	require.NoError(t, db.Exec("DROP TABLE k_knowledge_entries").Error)
	_, e = read()
	require.Error(t, e)
}

func TestRetrievalDebugHTTPInvalidTenantAndMissingCapabilities(t *testing.T) {
	m, _, _ := knowledgeHarness(t)
	in := &retrievalDebugInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "bad"}, IdempotencyKey: "key", Body: debugQuery()}
	_, e := m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 422)
	in.TenantID = "1"
	m.audit = nil
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 503)
	m.runtime = nil
	_, e = m.retrievalDebugHTTP(knowledgeContext(t, 2), in)
	assertDebugStatus(t, e, 503)
}
