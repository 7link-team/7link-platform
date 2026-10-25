package knowledge

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNotificationAudienceRequiresActualReviewAndCurrentRead(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "RULE", Title: "Policy", Body: "Persist source evidence.", Provenance: "Human proposal"})
	_, err := m.notificationLinkOn(t.Context(), db, 1, entry.ID, "memory.reviewed")
	require.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		link, err := m.notificationLinkOn(t.Context(), tx, 1, entry.ID, "memory.reviewed")
		require.Equal(t, "/admin/1/entities/3/governance/memory-review?entry_id="+entry.ID.String(), link)
		return err
	}))
	check := func(actor guid.ID) bool {
		var yes bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			project, ok, err := m.notificationAudienceOn(t.Context(), tx, 1, entry.ID, actor, "memory.reviewed")
			if ok {
				require.EqualValues(t, 3, project)
			}
			yes = ok
			return err
		}))
		return yes
	}
	require.False(t, check(2))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, entry.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Independent Human review of the evidence"})
		return err
	}))
	require.True(t, check(2))
	require.False(t, check(3))
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.False(t, check(2))
}

func TestPendingRulesUseCurrentIndependentScopeAuthority(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	_, err := m.pendingRulesOn(knowledgeContext(t, 3), db, 1, 0)
	require.ErrorIs(t, err, ErrInvalid)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: ptr(3)}, Kind: "RULE", Title: "Evidence rule", Body: "Keep source evidence.", Provenance: "Human proposal"})
	writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Not a rule", Body: "A persisted fact.", Provenance: "Human note"})
	read := func(actor, project guid.ID) []PendingRule {
		var rows []PendingRule
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var err error
			rows, err = m.pendingRulesOn(knowledgeContext(t, actor), tx, 1, project)
			return err
		}))
		return rows
	}
	require.Empty(t, read(2, 3), "writer cannot review their own rule")
	rows := read(3, 3)
	require.Len(t, rows, 1)
	require.Equal(t, entry.ID, rows[0].ID)
	require.Empty(t, read(3, 8))
	m.policy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.Empty(t, read(3, 3), "revoked current scope removes a pending source")
}

func TestExactMemoryEntryAuthorizesActualScopeBeyondFirstPage(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}, Kind: "RULE", Title: "Agent evidence rule", Body: "Keep evidence.", Provenance: "Human proposal"})
	permissions := []string{}
	m.tenantPolicy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		require.Contains(t, []string{"knowledge_read", "memory_approve", "agent_workspace_read"}, permission, "use registered canonical source actions")
		permissions = append(permissions, permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	read := func(actor, tenant, id guid.ID) (EntryView, error) {
		var out EntryView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.entryOn(knowledgeContext(t, actor), tx, tenant, id)
			return err
		})
		return out, err
	}
	actual, err := read(3, 1, entry.ID)
	require.NoError(t, err)
	require.Equal(t, entry.ID, actual.ID)
	require.Equal(t, "AGENT", actual.Scope.Scope)
	require.Equal(t, ptr(7), actual.AgentPrincipalID)
	require.Contains(t, permissions, "knowledge_read")
	_, err = read(3, 2, entry.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = read(3, 1, entry.ID+1)
	require.ErrorIs(t, err, ErrNotFound)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = read(3, 1, entry.ID)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestPendingRuleAndExactEntryReadsFailClosedAtEachStorageStage(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	in.Kind = "RULE"
	entry := writeKnowledge(t, m, db, in)
	ctx := knowledgeContext(t, 3)
	read := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			rows, e := m.pendingRulesOn(ctx, tx, 1, 0)
			if e == nil {
				require.Len(t, rows, 1)
				require.Equal(t, entry.ID, rows[0].ID)
				require.EqualValues(t, 7, rows[0].AgentPrincipalID)
			}
			return e
		})
	}
	failure := errors.New("pending rule storage unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("pending-rule-query", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	require.NoError(t, read())
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, read(), failure, "review read stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("pending-rule-query"))
	exact := entryInput{KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, EntryID: entry.ID.String()}
	out, e := m.entryHTTP(ctx, &exact)
	require.NoError(t, e)
	require.Equal(t, entry.ID, out.Body.Data.ID)
	require.Equal(t, "PENDING_REVIEW", out.Body.Data.State)
	for _, input := range []entryInput{{}, {KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, EntryID: "bad"}, {KnowledgeTenantInput: KnowledgeTenantInput{TenantID: "1"}, EntryID: "999"}} {
		_, e = m.entryHTTP(ctx, &input)
		require.Error(t, e)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		link, e := m.notificationLinkOn(ctx, tx, 1, entry.ID, "memory.reviewed")
		require.NoError(t, e)
		require.Contains(t, link, "agent_principal_id=7")
		return e
	}))
	m.runtime = nil
	_, e = m.entryHTTP(ctx, &exact)
	require.Error(t, e)
}
func TestFailedSourceNotificationRequiresCurrentOwnerAndPersistedFailure(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	require.NoError(t, m.failSourceJob(knowledgeContext(t, 2), jobs.Job{TenantID: 1}, sourceIndexJob{SourceID: source.ID, Version: source.Version, ActorID: 2}, "INGESTION_RETRIES_EXHAUSTED"))
	for _, actor := range []guid.ID{2, 3} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			project, yes, e := m.notificationAudienceOn(t.Context(), tx, 1, source.ID, actor, "knowledge.source_failed")
			require.NoError(t, e)
			require.Zero(t, project)
			require.Equal(t, actor == 2, yes)
			link, e := m.notificationLinkOn(t.Context(), tx, 1, source.ID, "knowledge.source_failed")
			require.Contains(t, link, source.ID.String())
			return e
		}))
	}
	failure := errors.New("failed source authority unavailable")
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, yes, e := m.notificationAudienceOn(t.Context(), tx, 1, source.ID, 2, "knowledge.source_failed")
		require.False(t, yes)
		return e
	}), failure)
}

func TestReviewedMemoryAndFailedSourceAudiencesPropagateCurrentStorageFailures(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "RULE", Title: "Review notification", Body: "Retain source evidence.", Provenance: "Human proposal"})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, entry.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Independent review for notification"})
		return err
	}))
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	require.NoError(t, m.failSourceJob(t.Context(), jobs.Job{TenantID: 1}, sourceIndexJob{SourceID: source.ID, Version: source.Version, ActorID: 2}, "INGESTION_RETRIES_EXHAUSTED"))
	failure := errors.New("notification source unavailable")
	calls, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("notification-audience-failure", func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}))
	for _, v := range []struct {
		id  guid.ID
		typ string
	}{{entry.ID, "memory.reviewed"}, {source.ID, "knowledge.source_failed"}} {
		run := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				_, yes, err := m.notificationAudienceOn(t.Context(), tx, 1, v.id, 2, v.typ)
				if failAt == 0 {
					require.True(t, yes)
				}
				return err
			})
		}
		calls, failAt = 0, 0
		require.NoError(t, run())
		total := calls
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, run(), failure, "audience storage stage %d", i)
		}
	}
	require.NoError(t, db.Callback().Query().Remove("notification-audience-failure"))
	for _, v := range []struct {
		id  guid.ID
		typ string
	}{{entry.ID + 1, "memory.reviewed"}, {source.ID + 1, "knowledge.source_failed"}} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, yes, err := m.notificationAudienceOn(t.Context(), tx, 1, v.id, 2, v.typ)
			require.False(t, yes)
			return err
		}))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.notificationAudienceOn(t.Context(), tx, 1, entry.ID, 2, "unknown")
		return err
	}), ErrInvalid)
	_, _, err := m.notificationAudienceOn(t.Context(), nil, 1, entry.ID, 2, "memory.reviewed")
	require.ErrorIs(t, err, ErrInvalid)
	_, _, err = m.notificationAudienceOn(t.Context(), db, 1, entry.ID, 2, "memory.reviewed")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.notificationLinkOn(t.Context(), nil, 1, entry.ID, "memory.reviewed")
	require.ErrorIs(t, err, ErrInvalid)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.notificationLinkOn(t.Context(), tx, 1, entry.ID+1, "memory.reviewed")
		return err
	}))
	var absent *Module
	_, err = absent.pendingRulesOn(t.Context(), db, 1, 0)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.pendingRulesOn(t.Context(), nil, 1, 0)
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.pendingRulesOn(t.Context(), tx, 1, 0); return err }), ErrForbidden)
	_, err = m.entryOn(t.Context(), nil, 1, entry.ID)
	require.ErrorIs(t, err, ErrInvalid)
}
