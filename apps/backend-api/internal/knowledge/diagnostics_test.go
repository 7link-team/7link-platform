package knowledge

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDiagnosticTimingsPlansAndAgentIntersection(t *testing.T) {
	var nilDB *gorm.DB
	m0 := &Module{}
	_, nilErr := m0.retrieveDebugOn(knowledgeContext(t, 2), nilDB, 1, debugQuery())
	require.ErrorIs(t, nilErr, ErrInvalid)
	m, db, _ := knowledgeHarness(t)
	// Add a known query delay so coarse Windows clocks cannot collapse the
	// measurement to zero; the assertion proves elapsed query work is observed.
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("diagnostic_observed_delay", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Entries.String() {
			time.Sleep(5 * time.Millisecond)
		}
	}))
	project := guid.ID(3)
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: &project}, Kind: "DOCUMENT", Title: "Export amount", Body: "Real matching text", Provenance: "Source"})
	input := debugQuery()
	input.Scope = Scope{Scope: "PROJECT", ProjectID: &project}
	input.SimulateAgentID = ptr(7)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		result, err := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, input)
		if err != nil {
			return err
		}
		require.Equal(t, "sqlite", result.Dialect)
		require.False(t, result.Plan.NativeSQLAvailable)
		require.False(t, result.Plan.VectorAvailable)
		require.NotEmpty(t, result.Plan.Expression)
		require.Nil(t, result.Timings.VectorMS)
		require.Greater(t, result.Timings.TotalMS, 0.0)
		require.Greater(t, result.Timings.CandidatesMS, 0.0)
		require.GreaterOrEqual(t, result.Timings.TotalMS, result.Timings.TokenizationMS+result.Timings.CandidatesMS+result.Timings.RankingMS)
		return nil
	}))
	m.policy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, _ string, actor guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: actor == 2}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, input); return err }), ErrForbidden)
	input.SimulateAgentID = ptr(2)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, input); return err }), ErrForbidden)
}
func TestDiagnosticPersistedOnceRollbackAndCurrentHistory(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Export amount", Body: "Actual", Provenance: "Source"})
	request := &retrievalDebugInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, IdempotencyKey: "history", Body: debugQuery()}
	a, err := m.retrievalDebugHTTP(knowledgeContext(t, 2), request)
	require.NoError(t, err)
	b, err := m.retrievalDebugHTTP(knowledgeContext(t, 2), request)
	require.NoError(t, err)
	require.Equal(t, a.Body.Data, b.Body.Data)
	var count int64
	require.NoError(t, query.BindDiagnosticLogs(db, m.names).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.historyOn(knowledgeContext(t, 2), tx, 1, DiagnosticHistoryInput{Scope: "TEAM", Search: "EXPORT", Limit: 50})
		require.NoError(t, e)
		require.EqualValues(t, 1, page.Total)
		require.Equal(t, a.Body.Data.LogID, page.Items[0].LogID)
		page, e = m.historyOn(knowledgeContext(t, 2), tx, 1, DiagnosticHistoryInput{Scope: "TEAM", Search: "%", Limit: 50})
		require.NoError(t, e)
		require.Zero(t, page.Total)
		return nil
	}))
	old := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("offline") }
	request.IdempotencyKey = "rollback"
	_, err = m.retrievalDebugHTTP(knowledgeContext(t, 2), request)
	require.Error(t, err)
	m.audit = old
	require.NoError(t, query.BindDiagnosticLogs(db, m.names).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Exec("UPDATE k_knowledge_entries SET state='REVOKED', version=2 WHERE id=?", entry.ID).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.historyOn(knowledgeContext(t, 2), tx, 1, DiagnosticHistoryInput{Scope: "TEAM", Limit: 50})
		require.NoError(t, e)
		require.Zero(t, page.Total)
		return nil
	}))
}

func TestDiagnosticAndSearchExcludeExpiredExperienceBeforeForgetJob(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	now := m.now()
	expired := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "EXPERIENCE", Title: "Export amount", Body: "Actually expired experience", Provenance: "Source"})
	active := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Export amount", Body: "Current document", Provenance: "Source"})
	require.NoError(t, db.Exec("UPDATE k_knowledge_entries SET expires_at=? WHERE id=?", now.Add(-time.Millisecond), expired.ID).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		result, err := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, debugQuery())
		require.NoError(t, err)
		require.Len(t, result.Candidates, 2)
		for _, candidate := range result.Candidates {
			if candidate.Entry.ID == expired.ID {
				require.Equal(t, "EXPIRED", candidate.Exclusion)
				require.Zero(t, candidate.Rank)
				require.Zero(t, candidate.LexicalScore)
			}
		}
		page, err := m.searchOn(knowledgeContext(t, 2), tx, 1, KnowledgeSearchInput{DiagnosticHistoryInput: DiagnosticHistoryInput{Scope: "TEAM", Limit: 50}, Query: "export amount", Mode: "ALL"})
		require.NoError(t, err)
		require.Equal(t, 1, page.Total)
		require.Equal(t, active.ID, page.Items[0].Entry.ID)
		return nil
	}))
}

func TestRuntimeMetricConnectorKeepsOriginalReaderAfterCompetingBinding(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideDiagnostics(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[RuntimeMetricProjectionConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(RuntimeMetricProjection{}), ErrInvalid)
	failure := errors.New("authoritative runtime metric source unavailable")
	seen := 0
	source := RuntimeMetricProjection{ReadOn: func(context.Context, *gorm.DB, guid.ID, *guid.ID, time.Time, time.Time) ([]RuntimeMetricFact, error) {
		seen++
		return nil, failure
	}}
	require.NoError(t, connector.Bind(source))
	require.ErrorIs(t, connector.Bind(source), ErrConflict)
	now := m.now()
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.metricsOn(knowledgeContext(t, 2), tx, 1, KnowledgeMetricsInput{From: now.Add(-time.Hour), To: now.Add(time.Hour)})
		return err
	}), failure)
	require.Equal(t, 1, seen)
}

func TestDiagnosticSimulationRejectsMissingOrUnavailableCurrentAgent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	input := debugQuery()
	input.Scope = Scope{Scope: "PROJECT", ProjectID: ptr(3)}
	input.SimulateAgentID = ptr(7)
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error { return m.authorizeDebugPerspectiveOn(knowledgeContext(t, 2), tx, 1, input) })
	}
	original := m.agents.ByPrincipalOn
	m.agents.ByPrincipalOn = nil
	require.ErrorIs(t, run(), ErrUnavailable)
	m.agents.ByPrincipalOn = original
	input.SimulateAgentID = ptr(0)
	require.ErrorIs(t, run(), ErrInvalid)
	input.SimulateAgentID = ptr(7)
	input.ProjectID = nil
	require.ErrorIs(t, run(), ErrInvalid)
}
