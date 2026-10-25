package templates

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func novelBible() NovelEdit {
	return NovelEdit{Action: "SAVE", Kind: "STORY_BIBLE", Reason: "Record authored bible", Content: StudioContent{Title: "Tides", Novel: &NovelData{Entries: []NovelEntry{{Key: "gate", Category: "WORLD", Entity: "gate", Attribute: "moon", Value: "full", Text: "The gate opens only at the full moon.", Invariant: true}}, Characters: []NovelCharacter{{Name: "Lin", Motivation: "Find father", Arc: "Learns trust"}}, Beats: []NovelBeat{}}}}
}
func novelChapter(number int, value string, tick int) NovelEdit {
	text := "她🙂 opens the gate at " + value + " moon."
	return NovelEdit{Action: "SAVE", Kind: "CHAPTER", Reason: "Record authored chapter", Content: StudioContent{Title: "Chapter " + strconv.Itoa(number), Text: text, Novel: &NovelData{Entries: []NovelEntry{}, Characters: []NovelCharacter{}, Beats: []NovelBeat{}, Chapter: &NovelChapter{Number: number, TargetWords: 20, POV: "Lin", Plan: "Open gate", Facts: []NovelFact{{Entity: "gate", Attribute: "moon", Value: value, Tick: tick, Start: 3, Quote: "opens the gate"}}}}}}
}
func saveNovel(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, id guid.ID, in NovelEdit) StudioRecordView {
	t.Helper()
	var out StudioRecordView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; out, e = m.novelEditOn(ctx, tx, 99, id, in); return e }))
	return out
}
func TestNovelWholeBookThirtyChaptersAndOriginalEvidence(t *testing.T) {
	m, db, ctx := bindStudio(t, "NOVEL")
	bible := saveNovel(t, m, db, ctx, 0, novelBible())
	ids := []guid.ID{}
	for i := 1; i <= 30; i++ {
		value := "full"
		if i == 30 {
			value = "new"
		}
		ch := saveNovel(t, m, db, ctx, 0, novelChapter(i, value, 1))
		ids = append(ids, ch.ID)
	}
	run := saveNovel(t, m, db, ctx, 0, NovelEdit{Action: "RUN", Reason: "Check every recorded chapter"})
	r := run.Content.Novel.Report
	require.Equal(t, "structured-facts-v1", r.Engine)
	require.Equal(t, "UNAVAILABLE", r.SemanticAvailability)
	require.Len(t, r.Coverage, 30)
	require.Equal(t, 30, r.Facts)
	require.Empty(t, r.Incomplete)
	for i, c := range r.Coverage {
		require.Equal(t, ids[i], c.ID)
		require.Equal(t, 1, c.Version)
		require.Len(t, c.SHA256, 64)
		require.Positive(t, c.Words)
	}
	require.Len(t, r.Issues, 2)
	require.NotEqual(t, "0.0000", r.CED)
	require.Equal(t, "BIBLE_INVARIANT", r.Issues[0].Code)
	require.Equal(t, bible.ID, r.Issues[0].A.RecordID)
	require.Equal(t, "gate", r.Issues[0].A.EntryKey)
	require.Equal(t, "SAME_TIME_CONTRADICTION", r.Issues[1].Code)
	require.Equal(t, ids[29], r.Issues[1].B.RecordID)
	require.Equal(t, "opens the gate", r.Issues[1].B.Quote)
	require.Equal(t, 3, r.Issues[1].B.Start)
	var board NovelWorkbench
	e := db.Transaction(func(tx *gorm.DB) error { var err error; board, err = m.novelWorkbenchOn(ctx, tx, 99); return err })
	require.NoError(t, e)
	require.False(t, board.Stale)
	require.Equal(t, run.ID, board.Report.ID)
	edit := novelChapter(30, "full", 2)
	edit.Version = 1
	saveNovel(t, m, db, ctx, ids[29], edit)
	e = db.Transaction(func(tx *gorm.DB) error { var err error; board, err = m.novelWorkbenchOn(ctx, tx, 99); return err })
	require.NoError(t, e)
	require.True(t, board.Stale)
	recheck := saveNovel(t, m, db, ctx, 0, NovelEdit{Action: "RUN", Reason: "Recheck corrected manuscript"})
	require.Empty(t, recheck.Content.Novel.Report.Issues)
	require.NotEqual(t, r.InputSHA256, recheck.Content.Novel.Report.InputSHA256)
	// The previous report and its frozen quotation are immutable.
	old, e := m.studioRecordOn(ctx, db, bible.BindingID, run.ID)
	require.NoError(t, e)
	view, e := studioView(old)
	require.NoError(t, e)
	require.Equal(t, r, view.Content.Novel.Report)
}
func TestNovelRejectsForgedQuotesDuplicatesForeignLinksAndStaleWrites(t *testing.T) {
	m, db, ctx := bindStudio(t, "NOVEL")
	bible := saveNovel(t, m, db, ctx, 0, novelBible())
	ch := saveNovel(t, m, db, ctx, 0, novelChapter(1, "full", 2))
	invalid := []NovelEdit{novelBible(), novelChapter(1, "full", 2), novelChapter(2, "full", 2)}
	invalid[2].Content.Novel.Chapter.Facts[0].Quote = "fabricated"
	for _, in := range invalid {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.novelEditOn(ctx, tx, 99, 0, in); return e }))
	}
	in := novelChapter(1, "full", 2)
	in.Version = 3
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.novelEditOn(ctx, tx, 99, ch.ID, in); return e }), ErrConflict)
	in = NovelEdit{Action: "SAVE", Kind: "FORESHADOW", Reason: "track", Content: StudioContent{Title: "Letter", Novel: &NovelData{Foreshadow: &NovelForeshadow{Planted: ch.ID, Planned: 999999}}}}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.novelEditOn(ctx, tx, 99, 0, in); return e }), ErrInvalid)
	in.Content.Novel.Foreshadow.Planned = ch.ID
	f := saveNovel(t, m, db, ctx, 0, in)
	legacy := createStudio(t, m, db, ctx, StudioDraft{Domain: "NOVEL", Kind: "CHAPTER", Content: StudioContent{Title: "Unannotated legacy", Text: "Actual manuscript"}})
	r := saveNovel(t, m, db, ctx, 0, NovelEdit{Action: "RUN", Reason: "Report incompleteness"}).Content.Novel.Report
	require.Contains(t, r.Incomplete, legacy.ID)
	require.Len(t, r.Coverage, 2)
	require.Equal(t, []guid.ID{f.ID}, r.OpenForeshadows)
	for _, offset := range []int{-1, 2, 1000} {
		bad := novelChapter(3, "full", 3)
		bad.Content.Novel.Chapter.Facts[0].Start = offset
		require.ErrorIs(t, validateNovel(bad), ErrInvalid)
	}
	for _, bad := range []NovelEdit{{Kind: "STORY_BIBLE", Action: "SAVE", Reason: "x", Content: StudioContent{Title: "x", Novel: &NovelData{Report: &NovelReport{}}}}, {Kind: "CHAPTER", Action: "SAVE", Reason: "x", Content: StudioContent{Title: "x", Novel: &NovelData{}}}} {
		require.ErrorIs(t, validateNovel(bad), ErrInvalid)
	}
	_ = bible
}
func TestNovelChronologyClosureAndAtomicOutbox(t *testing.T) {
	m, db, ctx := bindStudio(t, "NOVEL")
	saveNovel(t, m, db, ctx, 0, novelBible())
	ch := saveNovel(t, m, db, ctx, 0, novelChapter(1, "full", 5))
	saveNovel(t, m, db, ctx, 0, novelChapter(2, "full", 4))
	in := NovelEdit{Action: "SAVE", Kind: "FORESHADOW", Reason: "Deliberate sequel hook", Content: StudioContent{Title: "Letter", Novel: &NovelData{Foreshadow: &NovelForeshadow{Planted: ch.ID, Planned: ch.ID, Retained: true, Reason: "Sequel"}}}}
	saveNovel(t, m, db, ctx, 0, in)
	r := saveNovel(t, m, db, ctx, 0, NovelEdit{Action: "RUN", Reason: "Run"}).Content.Novel.Report
	require.Len(t, r.Issues, 1)
	require.Equal(t, "CHRONOLOGY_REVERSAL", r.Issues[0].Code)
	require.Empty(t, r.OpenForeshadows)
	q := query.BindStudioRecords(db, m.names)
	before, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
		return 0, errors.New("outbox unavailable")
	})
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.novelEditOn(ctx, tx, 99, 0, NovelEdit{Action: "RUN", Reason: "Rollback"})
		return e
	}))
	after, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Equal(t, before, after)
}

func TestNovelWorkbenchAndWritesRejectCorruptStorage(t *testing.T) {
	for _, failure := range []string{"read", "create", "update", "corrupt"} {
		t.Run(failure, func(t *testing.T) {
			m, db, ctx := bindStudio(t, "NOVEL")
			bible := saveNovel(t, m, db, ctx, 0, novelBible())
			injected := errors.New("novel storage unavailable")
			switch failure {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("novel-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.StudioRecords.String() {
						tx.AddError(injected)
					}
				}))
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("novel-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.StudioRecords.String() {
						tx.AddError(injected)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("novel-failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.StudioRecords.String() {
						tx.AddError(injected)
					}
				}))
			case "corrupt":
				require.NoError(t, db.Table(m.names.StudioRecords.String()).Where("id=?", bible.ID).Update("payload", "invalid").Error)
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				if failure == "read" || failure == "corrupt" {
					_, e := m.novelWorkbenchOn(ctx, tx, 99)
					return e
				}
				in := novelChapter(1, "full", 1)
				id := guid.ID(0)
				if failure == "update" {
					id = bible.ID
					in = novelBible()
					in.Version = 1
				}
				_, e := m.novelEditOn(ctx, tx, 99, id, in)
				return e
			})
			require.Error(t, e)
			if failure != "corrupt" {
				require.ErrorIs(t, e, injected)
			}
			require.NoError(t, db.Callback().Query().Remove("novel-failure"))
			require.NoError(t, db.Callback().Create().Remove("novel-failure"))
			require.NoError(t, db.Callback().Update().Remove("novel-failure"))
			var n int64
			require.NoError(t, db.Table(m.names.StudioRecords.String()).Count(&n).Error)
			require.EqualValues(t, 1, n)
		})
	}
	m, db, ctx := bindStudio(t, "NOVEL")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.novelEditOn(ctx, tx, 99, 0, NovelEdit{Action: "RUN", Reason: "No bible"})
		return e
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		in := novelBible()
		in.Reason = ""
		_, e := m.novelEditOn(ctx, tx, 99, 0, in)
		return e
	}), ErrInvalid)
}
