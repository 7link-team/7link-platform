package knowledge

import (
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge/model"
	"7link.com/platform/internal/knowledge/query"
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMemoryForgettingQuotaExpiresUnusedOldestAndKeepsOtherKinds(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
	seed := writeKnowledge(t, m, db, memoryInput())
	q := query.BindEntries(db, m.names)
	original, err := q.WithContext(t.Context()).Where(q.ID.Eq(seed.ID)).First()
	require.NoError(t, err)
	rows := make([]*model.Entry, 0, 502)
	for i := 0; i < 502; i++ {
		row := *original
		row.ID, err = m.nextSkillID()
		require.NoError(t, err)
		row.Title = fmt.Sprintf("Retention fixture %d", i)
		row.ContentHash = sourceHash(row.Title)
		row.CreatedAt = clock.Add(-time.Duration(600-i) * time.Hour)
		// The oldest row was used; the next two were never used and must
		// expire first even though both are newer than the used row.
		if i != 1 && i != 2 {
			used := clock.Add(-time.Hour)
			row.LastUsedAt = &used
		}
		rows = append(rows, &row)
	}
	require.NoError(t, q.WithContext(t.Context()).CreateInBatches(rows, 20))
	// 503 live EXPERIENCE rows require exactly three victims. The seed is
	// unused but newer, so it follows the two older unused rows.
	ruleInput := memoryInput()
	ruleInput.Kind, ruleInput.Title = "RULE", "Retained reviewed rule"
	rule := writeKnowledge(t, m, db, ruleInput)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reviewOn(knowledgeContext(t, 3), tx, 1, rule.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Independently checked permanent guidance"})
		return e
	}))
	require.NoError(t, m.memoryForgetJob(t.Context(), jobs.Job{TenantID: 1}))
	for _, tc := range []struct {
		id    guid.ID
		state string
	}{{rows[0].ID, "ACTIVE"}, {rows[1].ID, "EXPIRED"}, {rows[2].ID, "EXPIRED"}, {seed.ID, "EXPIRED"}, {rule.ID, "ACTIVE"}} {
		row, e := q.WithContext(t.Context()).Where(q.ID.Eq(tc.id)).First()
		require.NoError(t, e)
		require.Equal(t, tc.state, row.State)
		if tc.state == "EXPIRED" {
			require.Nil(t, row.LiveSlot)
		}
	}
	live, err := q.WithContext(t.Context()).Where(q.Kind.Eq("EXPERIENCE"), q.State.Eq("ACTIVE")).Count()
	require.NoError(t, err)
	require.EqualValues(t, 500, live)
	var expiredEvents int64
	require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "knowledge.memory_expired").Count(&expiredEvents).Error)
	require.EqualValues(t, 3, expiredEvents)
	// Replaying the scheduled job neither retires more rows nor invents usage.
	require.NoError(t, m.memoryForgetJob(t.Context(), jobs.Job{TenantID: 1}))
	var replayEvents int64
	require.NoError(t, db.Table("audit_probe").Where("event_type = ?", "knowledge.memory_expired").Count(&replayEvents).Error)
	require.Equal(t, expiredEvents, replayEvents)
}

func TestMemoryForgettingAuditFailureRollsBackExpiryAndHistory(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	clock := m.now()
	m.now = func() time.Time { return clock }
	entry := writeKnowledge(t, m, db, memoryInput())
	clock = clock.AddDate(0, 0, 31)
	before := map[string]int64{}
	for _, table := range []string{m.names.Reviews.String(), "audit_probe", "k_domain_events"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		before[table] = count
	}
	require.NoError(t, db.Exec("CREATE TRIGGER reject_memory_expiry BEFORE INSERT ON audit_probe WHEN NEW.event_type = 'knowledge.memory_expired' BEGIN SELECT RAISE(ABORT, 'expiry audit unavailable'); END").Error)
	require.ErrorContains(t, m.memoryForgetJob(t.Context(), jobs.Job{TenantID: 1}), "expiry audit unavailable")
	q := query.BindEntries(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	require.Equal(t, entry.Version, row.Version)
	require.NotNil(t, row.LiveSlot)
	for table, count := range before {
		var after int64
		require.NoError(t, db.Table(table).Count(&after).Error)
		require.Equal(t, count, after, table)
	}
}

func TestMemoryForgettingInvalidatesChunksWithoutDeletingHistoricalContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	clock := m.now()
	m.now = func() time.Time { return clock }
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	chunk := readKnowledgeChunks(t, m, db, source).Items[0]
	q := query.BindEntries(db, m.names)
	expiry := clock.Add(-time.Hour)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(chunk.EntryID)).Updates(map[string]any{"scope": "AGENT", "agent_principal_id": 7, "kind": "EXPERIENCE", "expires_at": expiry, "ttl_days": 7, "live_slot": 1})
	require.NoError(t, err)
	before, err := q.WithContext(t.Context()).Where(q.ID.Eq(chunk.EntryID)).First()
	require.NoError(t, err)
	require.NoError(t, m.memoryForgetJob(t.Context(), jobs.Job{TenantID: 1}))
	entry, err := q.WithContext(t.Context()).Where(q.ID.Eq(chunk.EntryID)).First()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", entry.State)
	require.Equal(t, before.Body, entry.Body)
	require.Equal(t, before.ContentHash, entry.ContentHash)
	chunks := query.BindChunks(db, m.names)
	retired, err := chunks.WithContext(t.Context()).Where(chunks.EntryID.Eq(entry.ID)).Find()
	require.NoError(t, err)
	require.NotEmpty(t, retired)
	for _, row := range retired {
		require.NotNil(t, row.InvalidatedAt)
		require.True(t, clock.Equal(*row.InvalidatedAt))
	}
	reviews := query.BindReviews(db, m.names)
	history, err := reviews.WithContext(t.Context()).Where(reviews.EntryID.Eq(entry.ID), reviews.Action.Eq("EXPIRED")).Find()
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, "ACTIVE", history[0].FromState)
	require.Equal(t, "EXPIRED", history[0].ToState)
}

func TestMemoryEditSupersedesAndRollsBackInvalidReplacement(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	old := writeKnowledge(t, m, db, memoryInput())
	var updated EntryView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		updated, e = m.editMemoryOn(knowledgeContext(t, 2), tx, 1, old.ID, MemoryMutation{Version: old.Version, Title: "Updated experience", Body: "Keep separately verified test evidence.", TTLDays: 14, Reason: "Correct the original guidance"})
		return e
	}))
	require.Equal(t, old.ID, *updated.SupersedesID)
	q := query.BindEntries(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(old.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "SUPERSEDED", row.State)
	require.Nil(t, row.LiveSlot)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.editMemoryOn(knowledgeContext(t, 2), tx, 1, updated.ID, MemoryMutation{Version: updated.Version, Title: "Invalid", Body: "short", TTLDays: 14, Reason: "Invalid replacement must roll back"})
		return e
	}))
	row, err = q.WithContext(t.Context()).Where(q.ID.Eq(updated.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
}
func TestMemoryForgettingHonorsVerifiedUsageAndPreservesRules(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
	old := writeKnowledge(t, m, db, memoryInput())
	in := memoryInput()
	in.Title = "Recently used memory"
	recent := writeKnowledge(t, m, db, in)
	in.Kind = "RULE"
	in.Title = "Permanent rule"
	rule := writeKnowledge(t, m, db, in)
	clock = clock.AddDate(0, 0, 29)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindEntries(tx, m.names)
		r, e := q.WithContext(t.Context()).Where(q.ID.Eq(recent.ID)).First()
		if e != nil {
			return e
		}
		return m.memoryUsageOn(t.Context(), tx, r)
	}))
	clock = clock.AddDate(0, 0, 2)
	qBefore := query.BindEntries(db, m.names)
	expiredBeforeJob, e := qBefore.WithContext(t.Context()).Where(qBefore.ID.Eq(old.ID)).First()
	require.NoError(t, e)
	require.False(t, m.memoryActive(expiredBeforeJob))
	recentBeforeJob, e := qBefore.WithContext(t.Context()).Where(qBefore.ID.Eq(recent.ID)).First()
	require.NoError(t, e)
	require.True(t, m.memoryActive(recentBeforeJob))
	require.NoError(t, m.memoryForgetJob(t.Context(), jobs.Job{TenantID: 1}))
	q := query.BindEntries(db, m.names)
	for _, tc := range []struct {
		id    EntryView
		state string
	}{{old, "EXPIRED"}, {recent, "ACTIVE"}, {rule, "PENDING_REVIEW"}} {
		row, e := q.WithContext(t.Context()).Where(q.ID.Eq(tc.id.ID)).First()
		require.NoError(t, e)
		require.Equal(t, tc.state, row.State)
	}
}

func TestMemoryPromotionKeepsReviewedRuleActiveAndPreservesHistory(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	in.Kind = "RULE"
	original := writeKnowledge(t, m, db, in)
	var active EntryView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		active, e = m.reviewOn(knowledgeContext(t, 3), tx, 1, original.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Independently checked evidence"})
		return e
	}))
	var promoted EntryView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		promoted, e = m.promoteMemoryOn(knowledgeContext(t, 3), tx, 1, active.ID, MemoryPromotion{Version: active.Version, Target: "TEAM", Reason: "Share independently reviewed guidance"})
		return e
	}))
	require.Equal(t, "ACTIVE", promoted.State)
	require.Equal(t, "TEAM", promoted.Scope.Scope)
	require.Equal(t, active.ID, *promoted.SupersedesID)
	q := query.BindEntries(db, m.names)
	old, e := q.WithContext(t.Context()).Where(q.ID.Eq(active.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "SUPERSEDED", old.State)
	reviews := query.BindReviews(db, m.names)
	history, e := reviews.WithContext(t.Context()).Where(reviews.EntryID.Eq(promoted.ID), reviews.Action.Eq("PROMOTED")).Find()
	require.NoError(t, e)
	require.Len(t, history, 1)
}

func TestMemoryPromotionUpdateFailureRollsBackReplacementAndEvents(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	in.Kind = "RULE"
	original := writeKnowledge(t, m, db, in)
	var active EntryView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		active, err = m.reviewOn(knowledgeContext(t, 3), tx, 1, original.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Independently checked evidence"})
		return err
	}))
	tables := []string{m.names.Entries.String(), m.names.Reviews.String(), "audit_probe", "k_domain_events"}
	before := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		before[table] = count
	}
	require.NoError(t, db.Exec("CREATE TRIGGER fail_memory_promotion BEFORE UPDATE OF supersedes_id ON "+m.names.Entries.String()+" WHEN NEW.supersedes_id IS NOT NULL BEGIN SELECT RAISE(ABORT, 'injected promotion update failure'); END").Error)
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.promoteMemoryOn(knowledgeContext(t, 3), tx, 1, active.ID, MemoryPromotion{Version: active.Version, Target: "TEAM", Reason: "Share independently reviewed guidance"})
		return err
	})
	require.ErrorContains(t, err, "injected promotion update failure")
	q := query.BindEntries(db, m.names)
	retained, err := q.WithContext(t.Context()).Where(q.ID.Eq(active.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", retained.State)
	require.Equal(t, active.Version, retained.Version)
	require.NotNil(t, retained.LiveSlot)
	for _, table := range tables {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Equal(t, before[table], count, table)
	}
}

func TestMemoryExpiredBeforeJobDeniesNewContextAndUsageButPreservesOldReceipts(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	clock := m.now()
	m.now = func() time.Time { return clock }
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	chunk := readKnowledgeChunks(t, m, db, source).Items[0]
	q := query.BindEntries(db, m.names)
	expiry := clock.Add(time.Hour)
	_, err := q.WithContext(t.Context()).Where(q.ID.Eq(chunk.EntryID)).Updates(map[string]any{"scope": "AGENT", "agent_principal_id": 7, "kind": "EXPERIENCE", "expires_at": expiry, "ttl_days": 7})
	require.NoError(t, err)
	entry, err := q.WithContext(t.Context()).Where(q.ID.Eq(chunk.EntryID)).First()
	require.NoError(t, err)
	fact := UsageFact{EntryID: entry.ID, ExecutionID: 88, EvidenceID: 90, Kind: "INCLUSION", ContentHash: entry.ContentHash}
	m.usage.evidence = UsageEvidence{VerifyOn: func(_ context.Context, _ *gorm.DB, tenant guid.ID, in UsageFact) error {
		if tenant != 1 || in.EntryID != entry.ID || in.ExecutionID != 88 || (in.EvidenceID != 90 && in.EvidenceID != 91) {
			return ErrNotFound
		}
		return nil
	}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.recordUsageOn(knowledgeContext(t, 2), tx, 1, fact) }))
	var frozen []ContextItem
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		frozen, e = m.contextItemsOn(knowledgeContext(t, 2), tx, 1, 3, []guid.ID{entry.ID})
		return e
	}))
	require.Len(t, frozen, 1)
	// The verified inclusion extends TTL by7days; cross that deadline without J4.
	clock = clock.AddDate(0, 0, 8)
	fact.EvidenceID = 91
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.contextItemsOn(knowledgeContext(t, 2), tx, 1, 3, []guid.ID{entry.ID})
		return e
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.recordUsageOn(knowledgeContext(t, 2), tx, 1, fact) }), ErrConflict)
	var receipts int64
	require.NoError(t, query.BindUsageReceipts(db, m.names).Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	require.Equal(t, entry.Body, frozen[0].Body)
	require.Equal(t, entry.ContentHash, frozen[0].ContentHash)
}

func TestMemoryDistinctSameDayUsageIncrementsCountWithoutMovingRetentionAnchor(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	clock := time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
	entry := writeKnowledge(t, m, db, memoryInput())
	apply := func() {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			q := query.BindEntries(tx, m.names)
			row, e := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
			if e != nil {
				return e
			}
			return m.memoryUsageOn(t.Context(), tx, row)
		}))
	}
	apply()
	firstUse := clock
	clock = clock.Add(2 * time.Hour)
	apply()
	q := query.BindEntries(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, e)
	require.EqualValues(t, 2, row.UseCount)
	require.True(t, firstUse.Equal(*row.LastUsedAt))
	require.True(t, firstUse.AddDate(0, 0, 30).Equal(*row.ExpiresAt))
}

func TestMemoryReplacementStorageFailuresKeepOriginalLiveAndHistoryAtomic(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	old := writeKnowledge(t, m, db, memoryInput())
	ctx := knowledgeContext(t, 2)
	q := query.BindEntries(db, m.names)
	before, e := q.WithContext(ctx).Where(q.ID.Eq(old.ID)).First()
	require.NoError(t, e)
	counts := map[string]int64{}
	for _, table := range []string{m.names.Entries.String(), m.names.Reviews.String(), "audit_probe", "k_domain_events"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		counts[table] = n
	}
	failure := errors.New("memory replacement storage unavailable")
	rollback := errors.New("rollback memory edit")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("memory-edit-storage", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("memory-edit-storage", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("memory-edit-storage", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.editMemoryOn(ctx, tx, 1, old.ID, MemoryMutation{Version: old.Version, Title: "Updated current experience", Body: "Retain independently checked source receipts for every changed outcome.", TTLDays: 14, Reason: "Correct the original guidance"})
			if e != nil {
				return e
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 8)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "memory edit stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("memory-edit-storage"))
	require.NoError(t, db.Callback().Create().Remove("memory-edit-storage"))
	require.NoError(t, db.Callback().Update().Remove("memory-edit-storage"))
	after, e := q.WithContext(ctx).Where(q.ID.Eq(old.ID)).First()
	require.NoError(t, e)
	require.Equal(t, before, after)
	for table, want := range counts {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Equal(t, want, n, table)
	}
}

func TestMemoryPromotionStorageFailuresKeepApprovedOriginalActive(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	in.Kind = "RULE"
	entry := writeKnowledge(t, m, db, in)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reviewOn(knowledgeContext(t, 3), tx, 1, entry.ID, ReviewInput{Version: entry.Version, Action: "APPROVED", Reason: "Independently reviewed rule"})
		return e
	}))
	failure := errors.New("transactional storage unavailable")
	rollback := errors.New("rollback diagnostic transaction")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	install := func() {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("transaction-storage-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("transaction-storage-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("transaction-storage-failure", probe))
	}
	remove := func() {
		require.NoError(t, db.Callback().Query().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Create().Remove("transaction-storage-failure"))
		require.NoError(t, db.Callback().Update().Remove("transaction-storage-failure"))
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
			require.ErrorIs(t, apply(), failure, "storage stage %d", i)
		}
		remove()
	}
	exercise(func(tx *gorm.DB) error {
		_, e := m.promoteMemoryOn(knowledgeContext(t, 3), tx, 1, entry.ID, MemoryPromotion{Version: 2, Target: "TEAM", Reason: "Promote independently reviewed guidance"})
		return e
	})
	q := query.BindEntries(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", row.State)
	require.Equal(t, "AGENT", row.Scope)
	require.Equal(t, 2, row.Version)
}

func TestMemoryExpiryRechecksProjectAndPrincipalAndRollsBackEveryStorageStage(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	in.ProjectID = ptr(3)
	entry := writeKnowledge(t, m, db, in)
	now := m.now()
	m.now = func() time.Time { return now.Add(31 * 24 * time.Hour) }
	job := jobs.Job{TenantID: 1}
	failure := errors.New("memory expiry unavailable")
	principal := m.principals.LockActiveRow
	m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, failure
	}
	require.ErrorIs(t, m.memoryForgetJob(t.Context(), job), failure)
	m.principals.LockActiveRow = principal
	archive := m.archiveState.read
	m.archiveState.read = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, failure }
	require.ErrorIs(t, m.memoryForgetJob(t.Context(), job), failure)
	m.archiveState.read = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	require.NoError(t, m.memoryForgetJob(t.Context(), job))
	m.archiveState.read = archive
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("expiry-storage-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("expiry-storage-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("expiry-storage-failure", probe))
	appender := m.audit
	m.audit = nil
	require.ErrorIs(t, m.memoryForgetJob(t.Context(), job), ErrUnavailable)
	total := calls
	require.Positive(t, total)
	m.audit = appender
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, m.memoryForgetJob(t.Context(), job), failure, "expiry storage stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("expiry-storage-failure"))
	require.NoError(t, db.Callback().Create().Remove("expiry-storage-failure"))
	require.NoError(t, db.Callback().Update().Remove("expiry-storage-failure"))
	q := query.BindEntries(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	require.NoError(t, m.memoryForgetJob(t.Context(), job))
	row, err = q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", row.State)
	require.Error(t, m.memoryForgetJob(t.Context(), jobs.Job{}))
	m.runtime = nil
	require.ErrorIs(t, m.memoryForgetJob(t.Context(), job), ErrUnavailable)
}

func TestAgentMemoryEditsPreserveHumanOriginAndRollbackBrokenAncestorReads(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	original := writeKnowledge(t, m, db, memoryInput())
	edit := func(id guid.ID, version int, title string) (EntryView, error) {
		var out EntryView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.editMemoryOn(knowledgeContext(t, 7), tx, 1, id, MemoryMutation{Version: version, Title: title, Body: "Keep the corrected source evidence.", TTLDays: 30, Reason: "Correct local guidance"})
			return err
		})
		return out, err
	}
	first, err := edit(original.ID, original.Version, "First corrected experience")
	require.NoError(t, err)
	second, err := edit(first.ID, first.Version, "Second corrected experience")
	require.NoError(t, err)
	require.Equal(t, "HUMAN", first.Origin)
	require.Equal(t, "HUMAN", second.Origin)
	require.Nil(t, second.SourceExecutionID)
	require.Equal(t, first.ID, *second.SupersedesID)
	failure := errors.New("ancestor read unavailable")
	reads, failAt := 0, 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("ancestor-read-failure", func(tx *gorm.DB) {
		reads++
		if reads == failAt {
			tx.AddError(failure)
		}
	}))
	rollback := errors.New("rollback edit probe")
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.editMemoryOn(knowledgeContext(t, 7), tx, 1, second.ID, MemoryMutation{Version: second.Version, Title: "Third correction", Body: "Keep the corrected source evidence.", TTLDays: 30, Reason: "Correct local guidance"})
			if err != nil {
				return err
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := reads
	for i := 1; i <= total; i++ {
		reads, failAt = 0, i
		require.ErrorIs(t, run(), failure, "edit ancestor read stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("ancestor-read-failure"))
	q := query.BindEntries(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(second.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	_, err = edit(original.ID, original.Version, "Stale original")
	require.ErrorIs(t, err, ErrConflict)
}

func TestMemoryPromotionToAgentGlobalRequiresApprovedProjectRule(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	in.Kind = "RULE"
	in.ProjectID = ptr(3)
	rule := writeKnowledge(t, m, db, in)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, rule.ID, ReviewInput{Version: rule.Version, Action: "APPROVED", Reason: "Independent project policy review"})
		return err
	}))
	promote := func(input MemoryPromotion) (EntryView, error) {
		var out EntryView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.promoteMemoryOn(knowledgeContext(t, 3), tx, 1, rule.ID, input)
			return err
		})
		return out, err
	}
	for _, input := range []MemoryPromotion{{Version: 1, Target: "AGENT_GLOBAL", Reason: "Promote reviewed policy"}, {Version: 2, Target: "AGENT_GLOBAL", Reason: "bad"}, {Version: 2, Target: "INVALID", Reason: "Promote reviewed policy"}} {
		_, err := promote(input)
		require.Error(t, err)
	}
	global, err := promote(MemoryPromotion{Version: 2, Target: "AGENT_GLOBAL", Reason: "Promote independently reviewed project policy"})
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", global.State)
	require.Equal(t, "AGENT", global.Scope.Scope)
	require.Nil(t, global.ProjectID)
	require.EqualValues(t, 7, *global.AgentPrincipalID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.promoteMemoryOn(knowledgeContext(t, 3), tx, 1, global.ID, MemoryPromotion{Version: global.Version, Target: "AGENT_GLOBAL", Reason: "Already global policy"})
		require.ErrorIs(t, err, ErrConflict)
		return nil
	}))
}
