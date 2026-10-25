package templates

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/query"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestStudioRejectedDraftsPersistNoRecords(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	zero := guid.ID(0)
	for _, in := range []StudioDraft{
		{Domain: "VIDEO", Kind: "CANDIDATE", Content: StudioContent{Title: "Candidate"}},
		{Domain: "VIDEO", Kind: "CANDIDATE", ParentID: &zero, Content: StudioContent{Title: "Candidate", Source: "source"}},
		{Domain: "VIDEO", Kind: "SHOT", Content: StudioContent{Title: "Shot", DurationMS: -1}},
		{Domain: "VIDEO", Kind: "SHOT", Content: StudioContent{Title: strings.Repeat("a", 201)}},
		{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Draft", Text: "Suggestion"}},
		{Domain: "NOVEL", Kind: "FORESHADOW", Content: StudioContent{Title: "Issue"}},
		{Domain: "TEAM_TASKS", Kind: "TASK", Content: StudioContent{Title: "Bypass"}},
	} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createStudioOn(ctx, tx, 99, in); return e }), ErrInvalid)
	}
	q := query.BindStudioRecords(db, m.names)
	n, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestStudioCurrentHumanAndParentChecksPreventCreation(t *testing.T) {
	m, db, ctx := bindStudio(t, "VIDEO")
	shot := createStudio(t, m, db, ctx, StudioDraft{Domain: "VIDEO", Kind: "SHOT", Content: StudioContent{Title: "Opening"}})
	candidate := createStudio(t, m, db, ctx, StudioDraft{Domain: "VIDEO", Kind: "CANDIDATE", ParentID: &shot.ID, Content: StudioContent{Title: "Candidate", Source: "reported-source"}})
	for _, parent := range []guid.ID{999, candidate.ID} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.createStudioOn(ctx, tx, 99, StudioDraft{Domain: "VIDEO", Kind: "CANDIDATE", ParentID: &parent, Content: StudioContent{Title: "Invalid parent", Source: "reported-source"}})
			return err
		}))
	}
	original := m.principals.ActiveHumanByID
	failure := errors.New("principal source failed")
	for _, source := range []func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error){
		nil,
		func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{}, false, nil
		},
		func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{}, false, failure
		},
	} {
		m.principals.ActiveHumanByID = source
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, err := m.createStudioOn(ctx, tx, 99, StudioDraft{Domain: "VIDEO", Kind: "SHOT", Content: StudioContent{Title: "Denied"}})
			return err
		}))
	}
	m.principals.ActiveHumanByID = original
	q := query.BindStudioRecords(db, m.names)
	n, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}

func TestStudioDecisionFailuresRetainOpenDraft(t *testing.T) {
	m, db, ctx := bindStudio(t, "TEAM_TASKS")
	owner := guid.ID(11)
	due := time.Now().UTC().Add(time.Hour)
	draft := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Action", Text: "Suggestion", Source: "message:1", OwnerID: &owner, DueAt: &due}})
	decide := func(action, reason string, version int) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.decideStudioOn(ctx, tx, 99, draft.ID, StudioDecision{Version: version, Action: action, Reason: reason})
			return e
		})
	}
	for _, action := range []string{"SELECT", "RESOLVE", "UNKNOWN"} {
		require.ErrorIs(t, decide(action, "Review", 1), ErrInvalid)
	}
	require.ErrorIs(t, decide("REJECT", "", 1), ErrInvalid)
	require.ErrorIs(t, decide("REJECT", "Review", 0), ErrInvalid)
	failure := errors.New("audit unavailable")
	originalAudit := m.audit
	m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, decide("REJECT", "Review", 1), failure)
	m.audit = originalAudit
	originalPublisher := m.events
	m.events = testPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
	require.ErrorIs(t, decide("REJECT", "Review", 1), failure)
	m.events = originalPublisher
	row, err := m.studioRecordOn(ctx, db, draft.BindingID, draft.ID)
	require.NoError(t, err)
	require.Equal(t, "OPEN", row.State)
	require.Equal(t, 1, row.Version)
	require.NoError(t, decide("REJECT", "Human rejected suggestion", 1))
	require.ErrorIs(t, decide("CONFIRM", "Cannot adopt rejected suggestion", 2), ErrConflict)
	row, err = m.studioRecordOn(ctx, db, draft.BindingID, draft.ID)
	require.NoError(t, err)
	require.Equal(t, "REJECTED", row.State)
	require.Equal(t, 2, row.Version)
	_, err = m.studioRecordOn(ctx, db, draft.BindingID, 999)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStudioPersistenceFailuresKeepRecordHistory(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			m, db, ctx := bindStudio(t, "TEAM_TASKS")
			draft := StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Action", Text: "Suggestion", Source: "message:1"}}
			created := createStudio(t, m, db, ctx, draft)
			injected := errors.New("studio persistence unavailable")
			callback := func(tx *gorm.DB) {
				if tx.Statement.Table == m.names.StudioRecords.String() {
					tx.AddError(injected)
				}
			}
			if operation == "create" {
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("studio-failure", callback))
				defer db.Callback().Create().Remove("studio-failure")
			} else {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("studio-failure", callback))
				defer db.Callback().Update().Remove("studio-failure")
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				if operation == "create" {
					_, e := m.createStudioOn(ctx, tx, 99, draft)
					return e
				}
				_, e := m.decideStudioOn(ctx, tx, 99, created.ID, StudioDecision{Action: "REJECT", Version: 1, Reason: "Human rejects"})
				return e
			}), injected)
			row, e := m.studioRecordOn(ctx, db, created.BindingID, created.ID)
			require.NoError(t, e)
			require.Equal(t, 1, row.Version)
			require.Equal(t, "OPEN", row.State)
		})
	}
}
