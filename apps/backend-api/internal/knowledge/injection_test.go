package knowledge

import (
	"7link.com/platform/internal/fleet"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func injectionHarness(t *testing.T) (*Module, *gorm.DB, InjectionRequest) {
	t.Helper()
	m, db, _ := knowledgeHarness(t)
	m.injection.work.VerifyOn = func(_ context.Context, _ *gorm.DB, tenant, project, work, agent guid.ID) error {
		if tenant != 1 || project != 99 || work != 100 || agent != 7 {
			return ErrForbidden
		}
		return nil
	}
	return m, db, InjectionRequest{TenantID: 1, ProjectID: 99, WorkItemID: 100, AgentPrincipalID: 7, ExecutionID: 200, Query: InjectionQuery{Title: "ledger"}, TokenBudget: 4000, TopK: 20}
}
func buildInjectionTest(t *testing.T, m *Module, db *gorm.DB, in InjectionRequest) InjectionResult {
	t.Helper()
	var out InjectionResult
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.buildInjectionOn(knowledgeContext(t, 7), tx, in)
		return err
	}))
	return out
}

func TestInjectionIncludesApprovedRulesWithoutQueryMatchAndCountsRealUsageOnce(t *testing.T) {
	m, db, request := injectionHarness(t)
	rule := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Independent evidence", Body: "Retain independent review evidence for every change.", Provenance: "Human rule"})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, rule.ID, ReviewInput{Version: rule.Version, Action: "APPROVED", Reason: "Independent rule approval"})
		return err
	}))
	experience := memoryInput()
	experience.Title = "Ledger lesson"
	experience.Body = "Ledger reconciliation requires immutable source receipts."
	writeKnowledge(t, m, db, experience)
	out := buildInjectionTest(t, m, db, request)
	require.Len(t, out.Rules, 1)
	require.Equal(t, rule.ID, out.Rules[0].EntryID)
	require.NotEmpty(t, out.Items)
	require.Equal(t, "PENDING", out.Rules[0].Inclusion)
	e := query.BindEntries(db, m.names)
	before, err := e.WithContext(t.Context()).Where(e.ID.Eq(rule.ID)).First()
	require.NoError(t, err)
	require.Zero(t, before.UseCount)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, out.RetrievalLogID, request.ExecutionID, "INCLUSION", "")
		}))
	}
	after, err := e.WithContext(t.Context()).Where(e.ID.Eq(rule.ID)).First()
	require.NoError(t, err)
	require.EqualValues(t, 1, after.UseCount)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, out.RetrievalLogID, request.ExecutionID, "CITATION", "Result follows [R1].")
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, stored, e := m.injectionLogOn(knowledgeContext(t, 7), tx, 1, out.RetrievalLogID, false)
		require.NoError(t, e)
		require.True(t, stored.Rules[0].Cited)
		require.Equal(t, "FULL", stored.Rules[0].Inclusion)
		return e
	}))
}

func TestInjectionBoundsPerSourceDeduplicatesAndUsesDeterministicTokenBudget(t *testing.T) {
	m, db, request := injectionHarness(t)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	var content strings.Builder
	// Distinct sections share the query, but not their lexical content. A single
	// source must not monopolize all returned knowledge positions.
	for i, words := range []string{"alpha bronze copper delta emerald", "forest glacier harbor ivory jade", "kite lemon marble nickel opal", "pearl quartz ruby silver topaz", "umber violet wheat xenon yellow"} {
		fmt.Fprintf(&content, "## Section %d\n\nledger %s\n\n", i, words)
	}
	source := sourceDraft()
	source.Title = "Ledger guide"
	source.Content = content.String()
	indexed := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, source), nil, false)
	request.TokenBudget = 200
	first := buildInjectionTest(t, m, db, request)
	require.NotEmpty(t, first.Items)
	require.LessOrEqual(t, len(first.Items), 3)
	for _, item := range first.Items {
		require.NotNil(t, item.SourceID)
		require.Equal(t, indexed.ID, *item.SourceID)
	}
	second := buildInjectionTest(t, m, db, request)
	first.RetrievalLogID, second.RetrievalLogID = 0, 0
	for i := range first.Items {
		first.Items[i].RetrievalLogID = 0
	}
	for i := range second.Items {
		second.Items[i].RetrievalLogID = 0
	}
	require.Equal(t, first, second)
	request.TokenBudget = 2
	limited := buildInjectionTest(t, m, db, request)
	require.LessOrEqual(t, limited.TokensTotal, 2)
	for _, item := range limited.Items {
		require.LessOrEqual(t, tokenCost(item.Body), 2)
		require.Equal(t, sourceHash(item.Title+"\n"+item.Body), item.SnapshotHash)
		require.True(t, item.Truncated)
	}
	// Same text from a second source is represented once, even with distinct IDs.
	other := source
	other.Locator = "/other-ledger-guide"
	syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, other), nil, false)
	request.TokenBudget = 200
	deduped := buildInjectionTest(t, m, db, request)
	seen := map[string]bool{}
	for _, item := range deduped.Items {
		require.False(t, seen[item.ContentHash])
		seen[item.ContentHash] = true
	}
}

func TestInjectionCurrentAuthorityRevocationAndExecutionProvenanceFailClosed(t *testing.T) {
	m, db, request := injectionHarness(t)
	in := memoryInput()
	in.Body = "Ledger reconciliation preserves immutable source evidence."
	writeKnowledge(t, m, db, in)
	out := buildInjectionTest(t, m, db, request)
	require.NotEmpty(t, out.Items)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, out.RetrievalLogID, 999, "INCLUSION", "")
	}), ErrForbidden)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, out.RetrievalLogID, request.ExecutionID, "CITATION", "[K1]")
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 2), tx, request); return err }), ErrForbidden)
	failure := errors.New("source permission dependency failed")
	m.tenantPolicy = core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return err }), failure)
	// Persisted selection is not reusable after source-owned assignment authority
	// is revoked, even when its content hash remains unchanged.
	m.injection.work.VerifyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.validateInjectionOn(knowledgeContext(t, 7), tx, 1, 99, request.ExecutionID, out)
	}), ErrForbidden)
}

func TestInjectionExcludesFeedbackFromCurrentWorkAndRevokedSources(t *testing.T) {
	m, db, request := injectionHarness(t)
	event := events.Envelope{EventID: 1001, TenantID: 1, ProjectID: 99, Actor: events.Actor{Kind: "HUMAN", ID: 2}}
	work := request.WorkItemID
	doc := EventSourceDocument{Kind: "FEEDBACK", Origin: "decision", OriginID: 1002, Version: 1, WorkItemID: &work, Title: "Ledger feedback", Content: "# Ledger feedback\n\nledger mutable balances failed validation."}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.upsertEventSourceOn(t.Context(), tx, event, doc) }))
	out := buildInjectionTest(t, m, db, request)
	require.Empty(t, out.Items)
	otherWork := guid.ID(101)
	doc.WorkItemID = &otherWork
	doc.OriginID = 1003
	doc.Content = "# Ledger feedback\n\nledger foreign rounding requires signed receipts."
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.upsertEventSourceOn(t.Context(), tx, event, doc) }))
	out = buildInjectionTest(t, m, db, request)
	require.Len(t, out.Items, 1)
	s := query.BindSources(db, m.names)
	_, err := s.WithContext(t.Context()).Where(s.ID.Eq(*out.Items[0].SourceID)).Update(s.State, "INVALIDATED")
	require.NoError(t, err)
	out = buildInjectionTest(t, m, db, request)
	require.Empty(t, out.Items)
}

func TestFrozenInjectionRejectsRetiredAgentSourceBeforeConsumerInvalidation(t *testing.T) {
	m, db, request := injectionHarness(t)
	input := sourceDraft()
	agent := request.AgentPrincipalID
	input.Scope = Scope{Scope: "AGENT", AgentPrincipalID: &agent}
	input.Content = "ledger reconciliation requires independent immutable evidence"
	syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, input), nil, false)
	out := buildInjectionTest(t, m, db, request)
	require.NotEmpty(t, out.Items)
	require.NotNil(t, out.Items[0].SourceID)
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 70, TenantID: tenant, PrincipalID: principal, OwnerPrincipalID: 2, State: "RETIRED"}, true, nil
	}
	// Use the human owner to isolate source liveness from the runtime actor fence.
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.validateInjectionSourceOn(knowledgeContext(t, 2), tx, 1, out.Items[0])
	}), ErrNotFound)
	// Keep actor authority active while invalidating only the source, to ensure
	// frozen execution validation cannot rely on the still-active entry alone.
	m.agents.ByPrincipalOn = func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 70, TenantID: tenant, PrincipalID: principal, OwnerPrincipalID: 2, State: "ACTIVE"}, true, nil
	}
	s := query.BindSources(db, m.names)
	_, err := s.WithContext(t.Context()).Where(s.ID.Eq(*out.Items[0].SourceID)).Update(s.State, "INVALIDATED")
	require.NoError(t, err)
	ctx := knowledgeContext(t, 7)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.validateInjectionOn(ctx, tx, 1, 99, request.ExecutionID, out)
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return m.recordInjectionOn(ctx, tx, 1, out.RetrievalLogID, request.ExecutionID, "INCLUSION", "")
	}))
}

func TestInjectionSelectionAndUseStorageFailuresLeaveNoPartialUsage(t *testing.T) {
	m, db, request := injectionHarness(t)
	input := memoryInput()
	input.Title = "Ledger evidence"
	entry := writeKnowledge(t, m, db, input)
	failure := errors.New("injection storage unavailable")
	rollback := errors.New("rollback injection diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	install := func() {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("injection-storage-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("injection-storage-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("injection-storage-failure", probe))
	}
	remove := func() {
		require.NoError(t, db.Callback().Query().Remove("injection-storage-failure"))
		require.NoError(t, db.Callback().Create().Remove("injection-storage-failure"))
		require.NoError(t, db.Callback().Update().Remove("injection-storage-failure"))
	}
	exercise := func(run func(*gorm.DB) error) {
		calls, failAt = 0, 0
		install()
		apply := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if e := run(tx); e != nil {
					return e
				}
				return rollback
			})
		}
		require.ErrorIs(t, apply(), rollback)
		total := calls
		require.Greater(t, total, 2)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, apply(), failure, "injection stage %d", i)
		}
		remove()
	}
	exercise(func(tx *gorm.DB) error { _, e := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return e })
	selected := buildInjectionTest(t, m, db, request)
	require.NotEmpty(t, selected.Items)
	exercise(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, selected.RetrievalLogID, request.ExecutionID, "INCLUSION", "")
	})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, selected.RetrievalLogID, request.ExecutionID, "INCLUSION", "")
	}))
	exercise(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, selected.RetrievalLogID, request.ExecutionID, "CITATION", "Result follows [K1].")
	})
	q := query.BindEntries(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, e)
	require.EqualValues(t, 1, row.UseCount)
	var citations int64
	require.NoError(t, query.BindUsageReceipts(db, m.names).Where("kind = ?", "CITATION").Count(&citations).Error)
	require.Zero(t, citations)
}

func TestInjectionConnectorsRejectMissingAndCompetingSourceOwners(t *testing.T) {
	m, db, request := injectionHarness(t)
	owner := m.injection.work
	m.injection.work = InjectionWorkReader{}
	s := module.NewServices()
	require.NoError(t, m.provideInjection(s))
	require.NoError(t, s.Seal())
	work, e := module.Resolve[InjectionWorkConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, work.Bind(InjectionWorkReader{}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.injectionWorkOn(knowledgeContext(t, 7), tx, 1, 99, 100, 7) }), ErrUnavailable)
	require.NoError(t, work.Bind(owner))
	require.ErrorIs(t, work.Bind(owner), ErrConflict)
	result, e := module.Resolve[ResultExecutionConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, result.Bind(ResultExecutionReader{}), ErrInvalid)
	source := ResultExecutionReader{ReadOn: func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 0, ErrNotFound }}
	require.NoError(t, result.Bind(source))
	require.ErrorIs(t, result.Bind(source), ErrConflict)
	require.ErrorIs(t, m.injectionWorkOn(knowledgeContext(t, 7), db, 1, 99, 100, 7), ErrInvalid)
	for _, v := range []InjectionRequest{{}, {TenantID: 1, ProjectID: 99, WorkItemID: 100, AgentPrincipalID: 7, ExecutionID: 200, TokenBudget: -1, TopK: 1}, {TenantID: 1, ProjectID: 99, WorkItemID: 100, AgentPrincipalID: 7, ExecutionID: 200, TokenBudget: 100, TopK: 21}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.buildInjectionOn(knowledgeContext(t, 7), tx, v); return e }))
	}
	request.Query.Title = strings.Repeat("x", 2001)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return e }), ErrInvalid)
}

func TestInjectionRuleQuotaDefaultBudgetAndExplicitCurrentSource(t *testing.T) {
	m, db, request := injectionHarness(t)
	for i := 0; i < 22; i++ {
		rule := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: fmt.Sprintf("Independent review guidance %02d", i), Body: "Retain source evidence with every change.", Provenance: "Human guidance"})
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, rule.ID, ReviewInput{Version: rule.Version, Action: "APPROVED", Reason: "Independently reviewed source policy"})
			return err
		}))
	}
	large := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Full policy text", Body: strings.Repeat("policy ", 1000), Provenance: "Human guidance"})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, large.ID, ReviewInput{Version: large.Version, Action: "APPROVED", Reason: "Independently reviewed full policy"})
		return err
	}))
	experience := memoryInput()
	experience.Title = "Ledger reconciliation"
	experience.Body = "Exact ledger reconciliation context."
	writeKnowledge(t, m, db, experience)
	request.TokenBudget, request.TopK = 0, 0
	out := buildInjectionTest(t, m, db, request)
	require.Equal(t, 4000, out.TokenBudget)
	require.Len(t, out.Rules, 20)
	require.Equal(t, 3, out.RulesOmitted)
	for _, item := range out.Rules {
		require.NotEqual(t, large.ID, item.EntryID)
	}
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	canonicalID := readKnowledgeChunks(t, m, db, source).Items[0].EntryID
	request.ExplicitIDs = []guid.ID{canonicalID}
	request.Query.Title = "unrelated query"
	out = buildInjectionTest(t, m, db, request)
	require.Len(t, out.Items, 1)
	require.Equal(t, canonicalID, out.Items[0].EntryID)
	request.ExplicitIDs = make([]guid.ID, 51)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return err }), ErrInvalid)
	request.ExplicitIDs = []guid.ID{999999}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return err }), gorm.ErrRecordNotFound)
	request.ExplicitIDs = nil
	request.ExecutionID = 0
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return err }), ErrInvalid)
	request.ExecutionID = 200
	m.archiveState.read = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return err }), ErrForbidden)
}

func TestInjectionFactRecordingUsesSelectedHashAndRollsBackStorageFailures(t *testing.T) {
	m, db, request := injectionHarness(t)
	input := memoryInput()
	input.Title = "Ledger policy"
	entry := writeKnowledge(t, m, db, input)
	selected := buildInjectionTest(t, m, db, request)
	require.Len(t, selected.Items, 1)
	fact := UsageFact{EntryID: entry.ID, ExecutionID: request.ExecutionID, ContentHash: entry.ContentHash, Kind: "INCLUSION", EvidenceID: 500}
	failure := errors.New("injection fact storage unavailable")
	rollback := errors.New("rollback injection fact diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("injection-fact-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("injection-fact-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("injection-fact-failure", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			handled, err := m.recordInjectionFactOn(knowledgeContext(t, 7), tx, 1, fact)
			if err != nil {
				return err
			}
			require.True(t, handled)
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 3)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "injection fact storage stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("injection-fact-failure"))
	require.NoError(t, db.Callback().Create().Remove("injection-fact-failure"))
	require.NoError(t, db.Callback().Update().Remove("injection-fact-failure"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		handled, err := m.recordInjectionFactOn(knowledgeContext(t, 7), tx, 1, fact)
		require.True(t, handled)
		return err
	}))
	fact.Kind = "CITATION"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		handled, err := m.recordInjectionFactOn(knowledgeContext(t, 7), tx, 1, fact)
		require.True(t, handled)
		return err
	}))
	fact.ContentHash = strings.Repeat("0", 64)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		handled, err := m.recordInjectionFactOn(knowledgeContext(t, 7), tx, 1, fact)
		require.False(t, handled)
		return err
	}), ErrConflict)
	q := query.BindEntries(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, err)
	require.EqualValues(t, 1, row.UseCount)
}

func TestInjectionRanksCanonicalSourceKindsWithBoundedPerSourceSelection(t *testing.T) {
	m, db, request := injectionHarness(t)
	event := events.Envelope{EventID: 301, TenantID: 1, ProjectID: 99, Actor: events.Actor{Kind: "HUMAN", ID: 2}}
	for i, kind := range []string{"REQUIREMENT", "DECISION", "DELIVERY", "FEEDBACK"} {
		origin := "decision"
		if kind == "REQUIREMENT" {
			origin = "requirement"
		}
		if kind == "DELIVERY" {
			origin = "work_item_delivery"
		}
		work := guid.ID(500 + i)
		doc := EventSourceDocument{Kind: kind, Origin: origin, OriginID: work, Version: 1, WorkItemID: &work, Title: "Ledger " + kind, Content: "# Ledger " + kind + "\n\nLedger " + []string{"requirements define signed immutable business scope and accountable criteria", "decisions explain policy alternatives rationale dependencies choices", "delivery describes shipped artifact content documentation installation", "feedback records customer observations behavior reproduction symptoms"}[i]}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.upsertEventSourceOn(t.Context(), tx, event, doc) }))
	}
	request.TopK = 20
	out := buildInjectionTest(t, m, db, request)
	kinds := map[string]bool{}
	for _, item := range out.Items {
		require.NotNil(t, item.SourceID)
		require.NotNil(t, item.ChunkID)
		require.Positive(t, item.ScorePPM)
		kinds[item.SourceKind] = true
	}
	require.Len(t, kinds, 4)
	// A single document may contribute at most three distinct current fragments.
	draft := sourceDraft()
	draft.Title = "Ledger multi-section document"
	var body strings.Builder
	for i, words := range []string{"accounts payable ledger", "invoice evidence ledger", "journal reconciliation ledger", "balance source ledger", "audit history ledger"} {
		fmt.Fprintf(&body, "# Section %d\n\n%s\n\n", i, words)
	}
	draft.Content = body.String()
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, draft), nil, false)
	out = buildInjectionTest(t, m, db, request)
	count := 0
	for _, item := range out.Items {
		if item.SourceID != nil && *item.SourceID == source.ID {
			count++
		}
	}
	require.Equal(t, 3, count)
}

func TestSourceBackedInjectionStorageFailuresCannotSaveSelectionOrUsage(t *testing.T) {
	m, db, request := injectionHarness(t)
	draft := sourceDraft()
	draft.Title = "Ledger reference"
	draft.Content = "Ledger reconciliation requires current immutable documents."
	syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, draft), nil, false)
	failure := errors.New("source injection storage unavailable")
	rollback := errors.New("rollback source injection diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	install := func() {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("source-injection-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("source-injection-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("source-injection-failure", probe))
	}
	remove := func() {
		require.NoError(t, db.Callback().Query().Remove("source-injection-failure"))
		require.NoError(t, db.Callback().Create().Remove("source-injection-failure"))
		require.NoError(t, db.Callback().Update().Remove("source-injection-failure"))
	}
	exercise := func(run func(*gorm.DB) error) {
		calls, failAt = 0, 0
		install()
		apply := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if err := run(tx); err != nil {
					return err
				}
				return rollback
			})
		}
		require.ErrorIs(t, apply(), rollback)
		total := calls
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, apply(), failure, "source injection stage %d", i)
		}
		remove()
	}
	exercise(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return err })
	selected := buildInjectionTest(t, m, db, request)
	require.Len(t, selected.Items, 1)
	require.NotNil(t, selected.Items[0].SourceID)
	exercise(func(tx *gorm.DB) error {
		return m.validateInjectionOn(knowledgeContext(t, 7), tx, 1, 99, request.ExecutionID, selected)
	})
	exercise(func(tx *gorm.DB) error {
		return m.recordInjectionOn(knowledgeContext(t, 7), tx, 1, selected.RetrievalLogID, request.ExecutionID, "INCLUSION", "")
	})
	m.ids = nil
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, request); return err }), ErrUnavailable)
}
