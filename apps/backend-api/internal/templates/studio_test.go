package templates

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func bindStudio(t *testing.T, domain string) (*Module, *gorm.DB, context.Context) {
	t.Helper()
	m, db, ctx := templateHarness(t)
	d := testDefinition()
	d.Domain = domain
	v := publishRuntime(t, m, db, ctx, "studio", d)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, v.ID, 99, 11); return e }))
	return m, db, ctx
}
func createStudio(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, in StudioDraft) StudioRecordView {
	t.Helper()
	var out StudioRecordView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.createStudioOn(ctx, tx, 99, in); return e }))
	return out
}
func TestStudioVideoSelectionIsSingleAndRejectsCrossShotNotes(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	shot := createStudio(t, m, db, ctx, StudioDraft{Domain: "VIDEO", Kind: "SHOT", Content: StudioContent{Title: "Opening", DurationMS: 3000}})
	candidate := createStudio(t, m, db, ctx, StudioDraft{Domain: "VIDEO", Kind: "CANDIDATE", ParentID: &shot.ID, Content: StudioContent{Title: "A", Source: "https://example.invalid/a.mp4", Provider: "reported provider", Model: "reported version", Seed: "1"}})
	second := createStudio(t, m, db, ctx, StudioDraft{Domain: "VIDEO", Kind: "CANDIDATE", ParentID: &shot.ID, Content: StudioContent{Title: "B", Source: "https://example.invalid/b.mp4"}})
	for _, c := range []StudioRecordView{candidate, second} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.decideStudioOn(ctx, tx, 99, c.ID, StudioDecision{Version: 1, Action: "SELECT", Reason: "Human review"})
			return e
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, e := m.studioRecordOn(ctx, tx, shot.BindingID, shot.ID)
		require.Equal(t, &second.ID, row.SelectedID)
		require.Equal(t, 3, row.Version)
		return e
	}))
	note := createStudio(t, m, db, ctx, StudioDraft{Domain: "VIDEO", Kind: "QC_NOTE", ParentID: &second.ID, Content: StudioContent{Title: "Manual report", Text: "Frame 12 appears dark; advisory observation"}})
	require.Equal(t, "OPEN", note.State)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideStudioOn(ctx, tx, 99, note.ID, StudioDecision{Version: 1, Action: "CONFIRM", Reason: "Cannot turn notes into platform verification"})
		return e
	}), ErrInvalid)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideStudioOn(ctx, tx, 99, second.ID, StudioDecision{Version: 1, Action: "SELECT", Reason: "Stale"})
		return e
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createStudioOn(ctx, tx, 99, StudioDraft{Domain: "NOVEL", Kind: "CHAPTER", Content: StudioContent{Title: "Wrong template"}})
		return e
	}), ErrConflict)
}
func TestStudioNovelIssueAndForeshadowResolution(t *testing.T) {
	m, db, ctx := bindStudio(t, "NOVEL")
	chapter := createStudio(t, m, db, ctx, StudioDraft{Domain: "NOVEL", Kind: "CHAPTER", Content: StudioContent{Title: "Chapter one", Text: "At noon the gate opened."}})
	for _, kind := range []string{"CONTINUITY", "FORESHADOW"} {
		row := createStudio(t, m, db, ctx, StudioDraft{Domain: "NOVEL", Kind: kind, ParentID: &chapter.ID, Content: StudioContent{Title: "Gate", Text: "Check against chapter two", Source: "chapter:1#paragraph-1"}})
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			out, e := m.decideStudioOn(ctx, tx, 99, row.ID, StudioDecision{Version: 1, Action: "RESOLVE", Reason: "Compared original passages"})
			require.Equal(t, "RESOLVED", out.State)
			return e
		}))
	}
}
func TestStudioDraftRequiresHumanConfirmationAndTransactionRollback(t *testing.T) {
	m, db, ctx := bindStudio(t, "TEAM_TASKS")
	owner := guid.ID(11)
	due := time.Now().UTC().Add(time.Hour)
	incomplete := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Action", Text: "Suggestion", Source: "message:123"}})
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideStudioOn(ctx, tx, 99, incomplete.ID, StudioDecision{Version: 1, Action: "CONFIRM", Reason: "Adopt"})
		return e
	}), ErrInvalid)
	draft := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Action", Text: "Suggestion", Source: "message:123", OwnerID: &owner, DueAt: &due}})
	require.Equal(t, "ASSISTANT_DRAFT", draft.Kind)
	original := m.events
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
		return 0, errors.New("outbox unavailable")
	})
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideStudioOn(ctx, tx, 99, draft.ID, StudioDecision{Version: 1, Action: "CONFIRM", Reason: "Human adopted"})
		return e
	}))
	m.events = original
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		row, e := m.studioRecordOn(ctx, tx, draft.BindingID, draft.ID)
		require.Equal(t, "ASSISTANT_DRAFT", row.Kind)
		require.Equal(t, 1, row.Version)
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		out, e := m.decideStudioOn(ctx, tx, 99, draft.ID, StudioDecision{Version: 1, Action: "CONFIRM", Reason: "Human adopted"})
		require.Equal(t, "TASK", out.Kind)
		require.Equal(t, "CONFIRMED", out.State)
		return e
	}))
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	q := query.BindStudioRecords(db, m.names)
	before, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createStudioOn(ctx, tx, 99, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Rollback", Text: "No persisted note", Source: "message:rollback"}})
		return e
	}))
	after, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Equal(t, before, after)
}
