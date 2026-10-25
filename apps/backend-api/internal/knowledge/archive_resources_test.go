package knowledge

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestArchivedSourceJobsKeepPendingHistoryAndDoNotRequeue(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	project := guid.ID(99)
	draft := sourceDraft()
	draft.Scope = Scope{Scope: "PROJECT", ProjectID: &project}
	source := bindKnowledgeSource(t, m, db, draft)
	clock := m.now().Add(24 * time.Hour)
	m.now = func() time.Time { return clock }
	frozen := true
	m.archiveState.read = func(_ context.Context, tx *gorm.DB, _, id guid.ID) (bool, error) {
		_, transactional := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, transactional)
		require.Equal(t, project, id)
		return frozen, nil
	}
	queue := m.queue.(*knowledgeQueue)
	before := map[string]int64{}
	for _, table := range []string{"audit_probe", "k_domain_events"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		before[table] = count
	}
	require.NoError(t, m.failSourceJob(t.Context(), jobs.Job{TenantID: 1}, sourceIndexJob{SourceID: source.ID, Version: source.Version}, "INGESTION_RETRIES_EXHAUSTED"))
	require.NoError(t, m.reconcileSourcesJob(t.Context(), jobs.Job{ID: 88, TenantID: 1}))
	require.Empty(t, queue.requests)
	q := query.BindSources(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", row.State)
	require.Equal(t, source.Version, row.Version)
	for table, count := range before {
		var after int64
		require.NoError(t, db.Table(table).Count(&after).Error)
		require.Equal(t, count, after, table)
	}
	frozen = false
	require.NoError(t, m.reconcileSourcesJob(t.Context(), jobs.Job{ID: 89, TenantID: 1}))
	require.Len(t, queue.requests, 1)
	require.NoError(t, m.failSourceJob(t.Context(), jobs.Job{TenantID: 1}, sourceIndexJob{SourceID: source.ID, Version: source.Version}, "INGESTION_RETRIES_EXHAUSTED"))
	row, err = q.WithContext(t.Context()).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "FAILED", row.State)
	require.Equal(t, source.Version+1, row.Version)
}

func TestArchivedProjectMemoryDoesNotExpireUntilExplicitRestore(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	in := memoryInput()
	project := guid.ID(99)
	in.ProjectID = &project
	entry := writeKnowledge(t, m, db, in)
	clock := m.now().AddDate(0, 0, 31)
	m.now = func() time.Time { return clock }
	frozen := true
	m.archiveState.read = func(_ context.Context, tx *gorm.DB, _, id guid.ID) (bool, error) {
		_, transactional := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, transactional)
		require.Equal(t, project, id)
		return frozen, nil
	}
	before := map[string]int64{}
	for _, table := range []string{m.names.Reviews.String(), "audit_probe", "k_domain_events"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		before[table] = count
	}
	require.NoError(t, m.memoryForgetJob(t.Context(), jobs.Job{TenantID: 1}))
	q := query.BindEntries(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ACTIVE", row.State)
	require.Equal(t, entry.Version, row.Version)
	for table, count := range before {
		var after int64
		require.NoError(t, db.Table(table).Count(&after).Error)
		require.Equal(t, count, after, table)
	}
	frozen = false
	require.NoError(t, m.memoryForgetJob(t.Context(), jobs.Job{TenantID: 1}))
	row, err = q.WithContext(t.Context()).Where(q.ID.Eq(entry.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "EXPIRED", row.State)
	require.Equal(t, entry.Version+1, row.Version)
}

func TestProjectArchiveExcludesSearchContextAndDelayedSourceIngestion(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	project := guid.ID(99)
	draft := sourceDraft()
	draft.Scope = Scope{Scope: "PROJECT", ProjectID: &project}
	source := bindKnowledgeSource(t, m, db, draft)
	source = syncKnowledgeSource(t, m, db, source, nil, false)
	archived := false
	m.archiveState.read = func(_ context.Context, tx *gorm.DB, tenant, id guid.ID) (bool, error) {
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, project, id)
		return archived, nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		corpus, err := m.nativeCorpusOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{})
		require.Contains(t, corpus.sources, source.ID)
		return err
	}))
	archived = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		corpus, err := m.nativeCorpusOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{})
		require.NoError(t, err)
		require.Empty(t, corpus.sources)
		history, historyErr := m.nativeSearchOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{Query: "evidence", Limit: 50, IncludeArchivedProjects: true}, false)
		require.NoError(t, historyErr)
		require.Len(t, history.Results, 1)
		require.True(t, history.Results[0].ProjectArchived)
		// Explicit source history retains its original current policy checks.
		_, _, err = m.sourceOn(knowledgeContext(t, 2), tx, 1, source.ID, "knowledge_read", false)
		return err
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.buildInjectionOn(knowledgeContext(t, 7), tx, InjectionRequest{TenantID: 1, ProjectID: project})
		return err
	}), ErrForbidden)
	m.ingestion.readers = []EventSourceReader{{ReadOn: func(context.Context, *gorm.DB, events.Envelope) ([]EventSourceDocument, error) {
		t.Fatal("archived source must not be ingested")
		return nil, nil
	}}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.ingestEventOn(t.Context(), tx, events.Envelope{TenantID: 1, ProjectID: project, EventID: 123, EventType: "requirement.versioned"})
	}))
	boom := errors.New("project state unavailable")
	m.archiveState.read = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, boom }
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.nativeCorpusOn(knowledgeContext(t, 2), tx, 1, NativeSearchRequest{})
		return err
	}), boom)
}

func TestProjectArchiveMemoryApprovalUsesCanonicalReview(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	project := guid.ID(99)
	var entry EntryView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		entry, err = m.writeOn(ctx, tx, 1, EntryInput{Scope: Scope{Scope: "PROJECT", ProjectID: &project}, Kind: "RULE", Title: "Review rule", Body: "Use a reviewed checklist.", Provenance: "Human proposed rule"})
		return err
	}))
	require.Equal(t, "PENDING_REVIEW", entry.State)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, e := m.projectArchiveFactsOn(ctx, tx, 1, project)
		require.NoError(t, e)
		require.Len(t, facts, 1)
		require.Equal(t, entry.ID, facts[0].ID)
		other, e := m.projectArchiveFactsOn(ctx, tx, 2, project)
		require.NoError(t, e)
		require.Empty(t, other)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, entry.ID, ReviewInput{Version: 1, Action: "REJECTED", Reason: "No longer applicable to this project"})
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, err := m.projectArchiveFactsOn(ctx, tx, 1, project)
		require.NoError(t, err)
		require.Empty(t, facts)
		return nil
	}))
	_, err := m.projectArchiveFactsOn(ctx, db, 1, project)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestArchiveCapabilitiesRejectInvalidOwnersAndPropagateStorageErrors(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	m.archiveState.read = nil
	services := module.NewServices()
	require.NoError(t, m.provideArchiveResources(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[ProjectArchiveStateConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalid)
	archived, err := m.archivedProjectOn(t.Context(), db, 1, 0)
	require.NoError(t, err)
	require.False(t, archived)
	_, err = m.archivedProjectOn(t.Context(), db, 1, 99)
	require.ErrorIs(t, err, ErrUnavailable)
	reader := ProjectArchiveStateReader(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	require.NoError(t, connector.Bind(reader))
	require.ErrorIs(t, connector.Bind(reader), ErrInvalid)
	archived, err = m.archivedProjectOn(t.Context(), db, 1, 99)
	require.NoError(t, err)
	require.True(t, archived)
	facts, err := module.Resolve[ProjectArchiveFacts](services)
	require.NoError(t, err)
	_, err = facts(t.Context(), nil, 1, 99)
	require.ErrorIs(t, err, ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := facts(t.Context(), tx, 0, 99)
		require.ErrorIs(t, err, ErrInvalid)
		_, err = facts(t.Context(), tx, 1, 0)
		require.ErrorIs(t, err, ErrInvalid)
		return nil
	}))
	boom := errors.New("archive storage unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("archive_resources_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Entries.String() {
			tx.AddError(boom)
		}
	}))
	defer db.Callback().Query().Remove("archive_resources_failure")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := facts(t.Context(), tx, 1, 99); return err }), boom)
}
