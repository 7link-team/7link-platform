package knowledge

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUsedKnowledgeHTTPExposesOnlyVerifiedCitationsAndCurrentOwnerSnapshots(t *testing.T) {
	m, db, request := injectionHarness(t)
	input := memoryInput()
	input.Title = "Ledger note"
	input.Body = "Ledger balances require an immutable audit trail."
	entry := writeKnowledge(t, m, db, input)
	out := buildInjectionTest(t, m, db, request)
	require.Len(t, out.Items, 1)
	require.Equal(t, entry.ID, out.Items[0].EntryID)
	m.metrics.projection.ReadOn = func(context.Context, *gorm.DB, guid.ID, *guid.ID, time.Time, time.Time) ([]RuntimeMetricFact, error) {
		return []RuntimeMetricFact{{Kind: "CITATION_OUTCOME", ExecutionID: request.ExecutionID, EntryID: entry.ID, Outcome: "ACCEPTED"}}, nil
	}
	in := &InjectionHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ExecutionID: request.ExecutionID.String()}
	before, err := m.usedKnowledgeHTTP(knowledgeContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, "no-store", before.CacheControl)
	require.True(t, before.Body.Data.Available)
	require.Empty(t, before.Body.Data.Retrieval.Items[0].CitationOutcome)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, out.RetrievalLogID, request.ExecutionID, "INCLUSION", "")
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, out.RetrievalLogID, request.ExecutionID, "CITATION", "Answer cites [K1].")
	}))
	after, err := m.usedKnowledgeHTTP(knowledgeContext(t, 2), in)
	require.NoError(t, err)
	require.True(t, after.Body.Data.Retrieval.Items[0].Cited)
	require.Equal(t, "ACCEPTED", after.Body.Data.Retrieval.Items[0].CitationOutcome)
	// Source changes never replace frozen bytes or the original citation hash.
	q := query.BindEntries(db, m.names)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).Update(q.Version, entry.Version+1)
	require.NoError(t, err)
	changed, err := m.usedKnowledgeHTTP(knowledgeContext(t, 2), in)
	require.NoError(t, err)
	require.True(t, changed.Body.Data.Retrieval.Items[0].SourceChanged)
	require.Equal(t, entry.Body, changed.Body.Data.Retrieval.Items[0].Body)
	_, err = q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).Delete()
	require.NoError(t, err)
	retained, err := m.usedKnowledgeHTTP(knowledgeContext(t, 2), in)
	require.NoError(t, err)
	require.Equal(t, entry.Body, retained.Body.Data.Retrieval.Items[0].Body)
	require.True(t, retained.Body.Data.Retrieval.Items[0].SourceChanged)
	// A general project reader does not inherit the responsible Human's private
	// memory permission through an execution or review panel.
	m.tenantPolicy = core.TenantConstraintOn(func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: permission != "agent_workspace_read" || actor == 2}, nil
	})
	_, err = m.usedKnowledgeHTTP(knowledgeContext(t, 3), in)
	assertKnowledgeHTTPStatus(t, err, http.StatusForbidden)
	for _, bad := range []string{"0", "-1", "9223372036854775808", "not-an-id"} {
		_, err = m.usedKnowledgeHTTP(knowledgeContext(t, 2), &InjectionHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ExecutionID: bad})
		assertKnowledgeHTTPStatus(t, err, http.StatusUnprocessableEntity)
	}
}

func TestReviewedResultKnowledgeUsesCanonicalResultSourceAndDoesNotGuessIDs(t *testing.T) {
	m, _, _ := injectionHarness(t)
	seen := guid.ID(0)
	m.injection.results = ResultExecutionReader{ReadOn: func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) (guid.ID, error) {
		require.Equal(t, guid.ID(1), tenant)
		seen = id
		if id == 900 {
			return 0, nil
		}
		return 0, ErrForbidden
	}}
	result, err := m.resultKnowledgeHTTP(knowledgeContext(t, 2), &ResultKnowledgeHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ResultID: "900"})
	require.NoError(t, err)
	require.Equal(t, guid.ID(900), seen)
	require.False(t, result.Body.Data.Available)
	require.Equal(t, "RESULT_HAS_NO_SOURCE_EXECUTION", result.Body.Data.Reason)
	_, err = m.resultKnowledgeHTTP(knowledgeContext(t, 2), &ResultKnowledgeHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ResultID: "901"})
	assertKnowledgeHTTPStatus(t, err, http.StatusForbidden)
}

func TestUsedKnowledgeReadFailuresNeverExposeSuccessfulPartialSnapshot(t *testing.T) {
	m, db, request := injectionHarness(t)
	in := memoryInput()
	in.Title = "Ledger evidence"
	writeKnowledge(t, m, db, in)
	selected := buildInjectionTest(t, m, db, request)
	require.NotEmpty(t, selected.Items)
	m.injection.results = ResultExecutionReader{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return request.ExecutionID, nil }}
	execution := InjectionHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ExecutionID: request.ExecutionID.String()}
	result := ResultKnowledgeHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ResultID: "900"}
	failure := errors.New("snapshot query unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("snapshot-read-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	for _, run := range []func() error{func() error {
		out, e := m.usedKnowledgeHTTP(knowledgeContext(t, 2), &execution)
		if e == nil {
			require.True(t, out.Body.Data.Available)
		}
		return e
	}, func() error {
		out, e := m.resultKnowledgeHTTP(knowledgeContext(t, 2), &result)
		if e == nil {
			require.True(t, out.Body.Data.Available)
		}
		return e
	}} {
		calls, failAt = 0, 0
		require.NoError(t, run())
		total := calls
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.Error(t, run(), "snapshot read stage %d", i)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("snapshot-read-failure"))
	for _, input := range []ResultKnowledgeHTTPInput{{}, {KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ResultID: "invalid"}} {
		_, e := m.resultKnowledgeHTTP(knowledgeContext(t, 2), &input)
		require.Error(t, e)
	}
	_, e := m.usedKnowledgeHTTP(knowledgeContext(t, 2), &InjectionHTTPInput{})
	require.Error(t, e)
	m.injection.results = ResultExecutionReader{}
	_, e = m.resultKnowledgeHTTP(knowledgeContext(t, 2), &result)
	require.Error(t, e)
	m.runtime = nil
	_, e = m.resultKnowledgeHTTP(knowledgeContext(t, 2), &result)
	require.Error(t, e)
	_, e = m.usedKnowledgeHTTP(knowledgeContext(t, 2), &execution)
	require.Error(t, e)
}

func TestUsedKnowledgeLegacySnapshotRetainsActualContextAndCurrentAuthority(t *testing.T) {
	m, db, request := injectionHarness(t)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	entryID := readKnowledgeChunks(t, m, db, source).Items[0].EntryID
	var contexts []ContextItem
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		contexts, err = m.contextItemsOn(knowledgeContext(t, 2), tx, 1, 99, []guid.ID{entryID})
		return err
	}))
	require.Len(t, contexts, 1)
	frozen := contexts[0]
	rule := frozen
	rule.Label = "[R1]"
	m.view.read = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionKnowledgeSnapshot, error) {
		return ExecutionKnowledgeSnapshot{ProjectID: 99, WorkItemID: 100, Items: []ContextItem{frozen, rule}}, nil
	}
	in := InjectionHTTPInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, ExecutionID: request.ExecutionID.String()}
	out, err := m.usedKnowledgeHTTP(knowledgeContext(t, 2), &in)
	require.NoError(t, err)
	require.True(t, out.Body.Data.Available)
	require.False(t, out.Body.Data.ScoresAvailable)
	require.Equal(t, "SNAPSHOT_ONLY", out.Body.Data.Retrieval.Mode)
	require.Len(t, out.Body.Data.Retrieval.Items, 1)
	require.Len(t, out.Body.Data.Retrieval.Rules, 1)
	require.Equal(t, frozen.Body, out.Body.Data.Retrieval.Items[0].Body)
	failure := errors.New("legacy context storage unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("legacy-context-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	_, err = m.usedKnowledgeHTTP(knowledgeContext(t, 2), &in)
	require.NoError(t, err)
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		failed, err := m.usedKnowledgeHTTP(knowledgeContext(t, 2), &in)
		require.Error(t, err, "legacy context read stage %d", i)
		require.Nil(t, failed)
	}
	require.NoError(t, db.Callback().Query().Remove("legacy-context-failure"))
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	out, err = m.usedKnowledgeHTTP(knowledgeContext(t, 2), &in)
	require.Error(t, err)
	require.Nil(t, out)
}
