package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func sourceDraft() SourceInput {
	return SourceInput{Scope: Scope{Scope: "TEAM"}, SourceKind: "UPLOAD", Title: "Evidence policy", Locator: "/documents/evidence", Format: "MARKDOWN", Content: "# Evidence\n\nRetain signed source evidence for amount exports."}
}
func bindKnowledgeSource(t *testing.T, m *Module, db *gorm.DB, in SourceInput) SourceView {
	t.Helper()
	var out SourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.bindSourceOn(knowledgeContext(t, 2), tx, 1, in)
		return e
	}))
	return out
}
func syncKnowledgeSource(t *testing.T, m *Module, db *gorm.DB, v SourceView, content *string, force bool) SourceView {
	t.Helper()
	var out SourceView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.syncSourceOn(knowledgeContext(t, 2), tx, 1, v.ID, SourceSyncInput{Version: v.Version, Content: content}, force)
		return e
	}))
	return out
}
func readKnowledgeChunks(t *testing.T, m *Module, db *gorm.DB, v SourceView) SourceChunkPage {
	t.Helper()
	var out SourceChunkPage
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.sourceChunksOn(knowledgeContext(t, 2), tx, 1, v.ID, 0, 100)
		return e
	}))
	return out
}

func TestKnowledgeSettingsPersistCASAndControlActualCandidateRanking(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		initial, e := m.settingsOn(ctx, tx, 1)
		require.NoError(t, e)
		require.Equal(t, 50, initial.CandidateLimit)
		require.Zero(t, initial.Version)
		require.Equal(t, "sqlite", initial.Dialect)
		require.False(t, initial.FullTextAvailable)
		require.False(t, initial.HybridAvailable)
		require.False(t, initial.RemoteSyncAvailable)
		saved, e := m.saveSettingsOn(ctx, tx, 1, KnowledgeSettingsInput{CandidateLimit: 1})
		require.Equal(t, 1, saved.Version)
		require.Equal(t, 1, saved.CandidateLimit)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.saveSettingsOn(ctx, tx, 1, KnowledgeSettingsInput{CandidateLimit: 20})
		return e
	}), ErrConflict)
	for _, in := range []KnowledgeSettingsInput{{CandidateLimit: 0}, {CandidateLimit: 201}, {CandidateLimit: 20, Version: -1}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveSettingsOn(ctx, tx, 1, in); return e }), ErrInvalid)
	}
	for _, actor := range []guid.ID{7, 8, 9} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.saveSettingsOn(knowledgeContext(t, actor), tx, 1, KnowledgeSettingsInput{CandidateLimit: 2, Version: 1})
			return e
		}), ErrForbidden)
	}
	for range 2 {
		writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "TEAM"}, Kind: "DOCUMENT", Title: "Export amount", Body: "Export amount evidence", Provenance: "Human-provided document"})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.retrieveDebugOn(ctx, tx, 1, debugQuery())
		require.NoError(t, e)
		require.Len(t, out.Candidates, 2)
		require.Equal(t, 1, out.Candidates[0].Rank)
		require.Equal(t, "CANDIDATE_LIMIT", out.Candidates[1].Exclusion)
		saved, e := m.saveSettingsOn(ctx, tx, 1, KnowledgeSettingsInput{CandidateLimit: 2, Version: 1})
		require.Equal(t, 2, saved.Version)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.retrieveDebugOn(ctx, tx, 1, debugQuery())
		require.Equal(t, 2, out.Candidates[1].Rank)
		return e
	}))
}

func TestKnowledgeSettingsPoliciesEnforceSourceWritesAndMemoryApproval(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	ctx := knowledgeContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.saveSettingsOn(ctx, tx, 1, KnowledgeSettingsInput{CandidateLimit: 50, IndexStrategy: "LEXICAL", SourcePermission: "MANAGERS", MemoryApproval: "ALL_MEMORIES"})
		require.NoError(t, err)
		require.Equal(t, "MANAGERS", out.SourcePermission)
		require.Len(t, out.EmbeddingRequirements, 4)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveSettingsOn(ctx, tx, 1, KnowledgeSettingsInput{CandidateLimit: 50, Version: 1, IndexStrategy: "HYBRID"})
		return err
	}), ErrUnavailable)
	entry := writeKnowledge(t, m, db, EntryInput{Scope: Scope{Scope: "AGENT", AgentPrincipalID: ptr(7)}, Kind: "EXPERIENCE", Title: "Evidence", Body: "Keep original evidence", Provenance: "Human authored"})
	require.Equal(t, "PENDING_REVIEW", entry.State)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.reviewOn(knowledgeContext(t, 3), tx, 1, entry.ID, ReviewInput{Version: 1, Action: "APPROVED", Reason: "Verified evidence policy"})
		require.Equal(t, "ACTIVE", out.State)
		return err
	}))
	previous := m.tenantPolicy
	m.tenantPolicy = func(ctx context.Context, tx *gorm.DB, tenant guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "knowledge_manage" {
			return authz.DataConstraint{}, nil
		}
		return previous(ctx, tx, tenant, permission, actor)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.bindSourceOn(ctx, tx, 1, sourceDraft()); return err }), ErrForbidden)
	// A rejected source command leaves no source or audit/outbox fact behind.
	q := query.BindSources(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestKnowledgeSourceSyncReusesHashesAndInvalidatesSearchAtomically(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	v := bindKnowledgeSource(t, m, db, sourceDraft())
	require.Equal(t, "PENDING", v.State)
	require.Empty(t, readKnowledgeChunks(t, m, db, v).Items)
	v = syncKnowledgeSource(t, m, db, v, nil, false)
	require.Equal(t, "INDEXED", v.State)
	require.Equal(t, 1, v.ChunkCount)
	require.NotNil(t, v.IndexedAt)
	first := readKnowledgeChunks(t, m, db, v).Items[0]
	require.Equal(t, "Evidence", first.HeadingPath)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		in := debugQuery()
		in.Query = "signed source"
		out, e := m.retrieveDebugOn(knowledgeContext(t, 2), tx, 1, in)
		require.Equal(t, first.EntryID, out.Candidates[0].Entry.ID)
		require.Empty(t, out.Candidates[0].Exclusion)
		return e
	}))
	v = syncKnowledgeSource(t, m, db, v, nil, false)
	require.Equal(t, first.ID, readKnowledgeChunks(t, m, db, v).Items[0].ID)
	v = syncKnowledgeSource(t, m, db, v, nil, true)
	require.Equal(t, first.ID, readKnowledgeChunks(t, m, db, v).Items[0].ID)
	content := sourceDraft().Content + "\n\n# Limits\n\nExports must use a verified tenant boundary."
	v = syncKnowledgeSource(t, m, db, v, &content, false)
	chunks := readKnowledgeChunks(t, m, db, v).Items
	require.Len(t, chunks, 2)
	require.Equal(t, first.ID, chunks[0].ID)
	replacement := "# Limits\n\nExports must use a verified tenant boundary."
	keepID := chunks[1].ID
	v = syncKnowledgeSource(t, m, db, v, &replacement, false)
	require.Equal(t, keepID, readKnowledgeChunks(t, m, db, v).Items[0].ID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindEntries(tx, m.names)
		old, e := q.WithContext(t.Context()).Where(q.ID.Eq(first.EntryID)).First()
		require.Equal(t, "REVOKED", old.State)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.syncSourceOn(knowledgeContext(t, 2), tx, 1, v.ID, SourceSyncInput{Version: 1}, false)
		return e
	}), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = m.invalidateSourceOn(knowledgeContext(t, 2), tx, 1, v.ID, v.Version)
		return e
	}))
	require.Equal(t, "INVALIDATED", v.State)
	require.Zero(t, v.ChunkCount)
	require.Empty(t, readKnowledgeChunks(t, m, db, v).Items)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.syncSourceOn(knowledgeContext(t, 2), tx, 1, v.ID, SourceSyncInput{Version: v.Version}, false)
		return e
	}), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.listOn(knowledgeContext(t, 2), tx, 1, knowledgeListInput{Scope: "TEAM", State: "ACTIVE", Limit: 50})
		require.Empty(t, page.Items)
		return e
	}))
}

func TestKnowledgeSourcesNeverFabricateRemoteOrPDFContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for _, kind := range []string{"URL", "REPOSITORY"} {
		in := sourceDraft()
		in.SourceKind = kind
		in.Content = ""
		in.Locator = "https://example.invalid/source"
		v := bindKnowledgeSource(t, m, db, in)
		require.Equal(t, "FAILED", v.State)
		require.Equal(t, "UNSUPPORTED_SOURCE", v.StateReason)
		v = syncKnowledgeSource(t, m, db, v, nil, true)
		require.Equal(t, "FAILED", v.State)
		require.Empty(t, v.ContentHash)
		require.Empty(t, readKnowledgeChunks(t, m, db, v).Items)
		content := "cannot impersonate remotely fetched text"
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.syncSourceOn(knowledgeContext(t, 2), tx, 1, v.ID, SourceSyncInput{Version: v.Version, Content: &content}, false)
			return e
		}), ErrInvalid)
	}
	in := sourceDraft()
	in.Format = "PDF"
	v := bindKnowledgeSource(t, m, db, in)
	require.Equal(t, "PDF_EXTRACTION_UNAVAILABLE", v.StateReason)
	v = syncKnowledgeSource(t, m, db, v, nil, false)
	require.Equal(t, "FAILED", v.State)
	require.Empty(t, readKnowledgeChunks(t, m, db, v).Items)
	in = sourceDraft()
	v = bindKnowledgeSource(t, m, db, in)
	v = syncKnowledgeSource(t, m, db, v, nil, false)
	empty := ""
	v = syncKnowledgeSource(t, m, db, v, &empty, false)
	require.Equal(t, "FAILED", v.State)
	require.Equal(t, "TEXT_NOT_EXTRACTABLE", v.StateReason)
	require.Zero(t, v.ChunkCount)
	require.Empty(t, readKnowledgeChunks(t, m, db, v).Items)
}

func TestKnowledgeSourceCurrentScopeChecksAndPagination(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	first := bindKnowledgeSource(t, m, db, sourceDraft())
	bindKnowledgeSource(t, m, db, sourceDraft())
	project := sourceDraft()
	project.Scope = Scope{Scope: "PROJECT", ProjectID: ptr(3)}
	bindKnowledgeSource(t, m, db, project)
	var cursor string
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.sourcesOn(knowledgeContext(t, 2), tx, 1, sourceListInput{Scope: "TEAM", Limit: 1})
		require.Len(t, page.Items, 1)
		require.Equal(t, first.ID, page.Items[0].ID)
		require.True(t, page.HasMore)
		cursor = page.NextCursor
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.sourcesOn(knowledgeContext(t, 2), tx, 1, sourceListInput{Scope: "TEAM", Limit: 1, Cursor: cursor})
		require.Len(t, page.Items, 1)
		require.False(t, page.HasMore)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.sourcesOn(knowledgeContext(t, 2), tx, 1, sourceListInput{Scope: "PROJECT", ProjectID: "3", Limit: 1, Cursor: cursor})
		return e
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.sourceOn(knowledgeContext(t, 2), tx, 2, first.ID, "knowledge_read", false)
		return e
	}), ErrNotFound)
	m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, e := m.sourceOn(knowledgeContext(t, 2), tx, 1, first.ID, "knowledge_read", false)
		return e
	}), ErrNotFound)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.sourceChunksOn(knowledgeContext(t, 2), tx, 1, first.ID, 0, 50)
		return e
	}), ErrNotFound)
}

func TestKnowledgeSourceAuditFailureRollsBackEntriesChunksAndSourceVersion(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	v := bindKnowledgeSource(t, m, db, sourceDraft())
	original := m.audit
	fail := errors.New("audit unavailable")
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "knowledge.source_synced" {
			return fail
		}
		return original(ctx, tx, event)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.syncSourceOn(knowledgeContext(t, 2), tx, 1, v.ID, SourceSyncInput{Version: v.Version}, false)
		return e
	}), fail)
	require.Empty(t, readKnowledgeChunks(t, m, db, v).Items)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, _, e := m.sourceOn(knowledgeContext(t, 2), tx, 1, v.ID, "knowledge_read", false)
		require.Equal(t, "PENDING", row.State)
		require.Equal(t, 1, row.Version)
		q := query.BindEntries(tx, m.names)
		count, err := q.WithContext(t.Context()).Count()
		require.NoError(t, err)
		require.Zero(t, count)
		return e
	}))
	m.audit = original
	v = syncKnowledgeSource(t, m, db, v, nil, false)
	require.Equal(t, "INDEXED", v.State)
}

func TestKnowledgeSourceValidation(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	for _, change := range []func(*SourceInput){func(in *SourceInput) { in.Scope.Scope = "BAD" }, func(in *SourceInput) { in.Title = " " }, func(in *SourceInput) { in.Title = strings.Repeat("x", 201) }, func(in *SourceInput) { in.Locator = "" }, func(in *SourceInput) { in.Format = "BINARY" }, func(in *SourceInput) { in.SourceKind = "REMOTE" }, func(in *SourceInput) { in.SourceKind = "URL" }, func(in *SourceInput) { in.Content = string([]byte{0xff}) }} {
		in := sourceDraft()
		change(&in)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.bindSourceOn(knowledgeContext(t, 2), tx, 1, in); return e }), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.bindSourceOn(knowledgeContext(t, 7), tx, 1, sourceDraft())
		return e
	}), ErrForbidden)
}

func TestSourceSyncStorageFailuresPreserveCurrentChunksContentAndHistory(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	ctx := knowledgeContext(t, 2)
	q := query.BindSources(db, m.names)
	before, e := q.WithContext(ctx).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, e)
	counts := map[string]int64{}
	for _, table := range []string{m.names.Entries.String(), m.names.Chunks.String(), m.names.Reviews.String(), "audit_probe", "k_domain_events"} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		counts[table] = n
	}
	failure := errors.New("source replacement storage unavailable")
	rollback := errors.New("rollback source replacement")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("source-replacement-storage", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("source-replacement-storage", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("source-replacement-storage", probe))
	content := "# Revised evidence\n\nRetain the actual signed owner receipt before accepting any changed artifact."
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.syncSourceOn(ctx, tx, 1, source.ID, SourceSyncInput{Version: source.Version, Content: &content}, false)
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
		require.ErrorIs(t, run(), failure, "source replacement stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("source-replacement-storage"))
	require.NoError(t, db.Callback().Create().Remove("source-replacement-storage"))
	require.NoError(t, db.Callback().Update().Remove("source-replacement-storage"))
	after, e := q.WithContext(ctx).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, e)
	require.Equal(t, before, after)
	for table, want := range counts {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Equal(t, want, n, table)
	}
	chunks := readKnowledgeChunks(t, m, db, source)
	require.Len(t, chunks.Items, source.ChunkCount)
}

func TestKnowledgeSettingsStorageFailureCannotAdvanceVersionOrPublish(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	failure, rollback := errors.New("settings storage unavailable"), errors.New("rollback settings probe")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("settings-storage-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("settings-storage-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("settings-storage-failure", probe))
	exercise := func(in KnowledgeSettingsInput) {
		run := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				_, err := m.saveSettingsOn(knowledgeContext(t, 2), tx, 1, in)
				if err != nil {
					return err
				}
				return rollback
			})
		}
		calls, failAt = 0, 0
		require.ErrorIs(t, run(), rollback)
		total := calls
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, run(), failure, "settings storage stage %d", i)
		}
		calls, failAt = 0, 0
	}
	exercise(KnowledgeSettingsInput{CandidateLimit: 100})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.saveSettingsOn(knowledgeContext(t, 2), tx, 1, KnowledgeSettingsInput{CandidateLimit: 100})
		return err
	}))
	exercise(KnowledgeSettingsInput{CandidateLimit: 120, Version: 1})
	require.NoError(t, db.Callback().Query().Remove("settings-storage-failure"))
	require.NoError(t, db.Callback().Create().Remove("settings-storage-failure"))
	require.NoError(t, db.Callback().Update().Remove("settings-storage-failure"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, err := m.settingsOn(knowledgeContext(t, 2), tx, 1)
		require.Equal(t, 1, out.Version)
		require.Equal(t, 100, out.CandidateLimit)
		return err
	}))
	for _, in := range []KnowledgeSettingsInput{{CandidateLimit: 100, IndexStrategy: "invalid"}, {CandidateLimit: 100, SourcePermission: "invalid"}, {CandidateLimit: 100, MemoryApproval: "invalid"}, {CandidateLimit: 201}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.saveSettingsOn(knowledgeContext(t, 2), tx, 1, in); return err }), ErrInvalid)
	}
}

func TestSourceIDAllocationFailuresNeverCreatePartialBindingOrChunks(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	owner := m.ids
	failure := errors.New("source id allocation unavailable")
	for _, invalid := range []guid.Source{nil, knowledgeIDs(func() (int64, error) { return 0, nil }), knowledgeIDs(func() (int64, error) { return 0, failure })} {
		m.ids = invalid
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.bindSourceOn(knowledgeContext(t, 2), tx, 1, sourceDraft())
			return err
		}))
	}
	m.ids = owner
	source := bindKnowledgeSource(t, m, db, sourceDraft())
	rollback := errors.New("rollback source allocation diagnostic")
	calls, failAt := 0, 0
	m.ids = knowledgeIDs(func() (int64, error) {
		calls++
		if calls == failAt {
			return 0, failure
		}
		return owner.Next()
	})
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.syncSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, SourceSyncInput{Version: source.Version}, false)
			if err != nil {
				return err
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	require.Greater(t, total, 1)
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "source id stage %d", i)
	}
	m.ids = owner
	q := query.BindSources(db, m.names)
	row, err := q.WithContext(t.Context()).Where(q.ID.Eq(source.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "PENDING", row.State)
	require.Equal(t, source.Version, row.Version)
	chunks, err := query.BindChunks(db, m.names).WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, chunks)
}

func TestSourceInvalidationStorageFailuresRetainIndexedContent(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	source := syncKnowledgeSource(t, m, db, bindKnowledgeSource(t, m, db, sourceDraft()), nil, false)
	failure := errors.New("source invalidation unavailable")
	rollback := errors.New("rollback invalidation diagnostic")
	calls, failAt := 0, 0
	probe := func(tx *gorm.DB) {
		calls++
		if calls == failAt {
			tx.AddError(failure)
		}
	}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("invalidate-source-failure", probe))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("invalidate-source-failure", probe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("invalidate-source-failure", probe))
	run := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, err := m.invalidateSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, source.Version)
			if err != nil {
				return err
			}
			return rollback
		})
	}
	require.ErrorIs(t, run(), rollback)
	total := calls
	for i := 1; i <= total; i++ {
		calls, failAt = 0, i
		require.ErrorIs(t, run(), failure, "invalidation stage %d", i)
	}
	require.NoError(t, db.Callback().Query().Remove("invalidate-source-failure"))
	require.NoError(t, db.Callback().Create().Remove("invalidate-source-failure"))
	require.NoError(t, db.Callback().Update().Remove("invalidate-source-failure"))
	require.Len(t, readKnowledgeChunks(t, m, db, source).Items, source.ChunkCount)
	for _, version := range []int{0, source.Version + 1} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.invalidateSourceOn(knowledgeContext(t, 2), tx, 1, source.ID, version)
			return err
		}))
	}
}
