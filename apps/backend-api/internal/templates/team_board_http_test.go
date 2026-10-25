package templates

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTeamBoardHTTPReplayPagingAndCurrentAccess(t *testing.T) {
	m, _, ctx, c, _ := teamHarness(t)
	in := &teamCreateInput{ProjectID: "99", IdempotencyKey: "team-once", Body: TeamEdit{Action: "SAVE", Reason: "Plan", Content: c}}
	first, e := m.teamCreateHTTP(ctx, in)
	require.NoError(t, e)
	again, e := m.teamCreateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.Record.ID, again.Body.Data.Record.ID)
	in.Body.Reason = "Changed"
	_, e = m.teamCreateHTTP(ctx, in)
	assertStatus(t, e, 409)
	in.IdempotencyKey = "team-two"
	_, e = m.teamCreateHTTP(ctx, in)
	require.NoError(t, e)
	page, e := m.teamBoardHTTP(ctx, &teamBoardInput{ProjectID: "99", Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.True(t, page.Body.Data.HasMore)
	next, e := m.teamBoardHTTP(ctx, &teamBoardInput{ProjectID: "99", Limit: 1, Sort: "id", Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.NotEqual(t, page.Body.Data.Items[0].Record.ID, next.Body.Data.Items[0].Record.ID)
	other := authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11, SubjectType: authn.SubjectTypePrincipal})
	_, e = m.teamBoardHTTP(other, &teamBoardInput{ProjectID: "99", Limit: 50, Sort: "id"})
	assertStatus(t, e, 404)
	in.Body.Reason = "Plan"
	in.IdempotencyKey = "team-once"
	m.teamWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.TeamWorkFact, error) {
		return delivery.TeamWorkFact{}, ErrForbidden
	}
	_, e = m.teamCreateHTTP(ctx, in)
	assertStatus(t, e, 403)
}
func TestTeamBoardHTTPHumanDraftEditAdoptAndReject(t *testing.T) {
	m, db, ctx, c, _ := teamHarness(t)
	draft := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "AI suggestion", Text: "Review suggested action", Source: "https://example.invalid/minutes"}})
	edited := &teamEditInput{ProjectID: "99", ID: draft.ID.String(), IdempotencyKey: "edit-draft", Body: TeamEdit{Version: 1, Action: "EDIT_DRAFT", Reason: "Correct source facts", Content: StudioContent{Title: "Edited suggestion", Text: "Corrected draft", Source: draft.Content.Source}}}
	edit, e := m.teamEditHTTP(ctx, edited)
	require.NoError(t, e)
	require.Equal(t, "ASSISTANT_DRAFT", edit.Body.Data.Record.Kind)
	adopt := &teamEditInput{ProjectID: "99", ID: draft.ID.String(), IdempotencyKey: "adopt-draft", Body: TeamEdit{Version: 2, Action: "ADOPT_DRAFT", Reason: "Human adopts", Content: c}}
	first, e := m.teamEditHTTP(ctx, adopt)
	require.NoError(t, e)
	require.Equal(t, "TASK", first.Body.Data.Record.Kind)
	require.Equal(t, draft.Content.Source, first.Body.Data.Record.Content.Source)
	replay, e := m.teamEditHTTP(ctx, adopt)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data.Record.ID, replay.Body.Data.Record.ID)
	nonhuman := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 11, SubjectType: authn.SubjectTypeServiceAccount})
	_, e = m.teamEditHTTP(nonhuman, adopt)
	assertStatus(t, e, 403)
}

func TestTeamBoardStatusDraftCannotAdoptInventedCompletion(t *testing.T) {
	m, db, ctx, c, _ := teamHarness(t)
	c.DraftType = "STATUS"
	c.ReportedState = "ACCEPTED"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, 0, TeamEdit{Action: "SAVE", Reason: "Status reconciliation", Content: c})
		return e
	}), ErrTeamStatus)
	c.ReportedState = "IN_PROGRESS"
	out := editTeam(t, m, db, ctx, 0, TeamEdit{Action: "SAVE", Reason: "Current system status", Content: c})
	require.Equal(t, "IN_PROGRESS", out.Work.State)
	status := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Status draft", Text: "Reported complete", Source: "https://example.invalid/state", DraftType: "STATUS", ReportedState: "ACCEPTED"}})
	c.DraftType = ""
	c.ReportedState = "ACCEPTED"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.teamEditOn(ctx, tx, 99, status.ID, TeamEdit{Version: 1, Action: "ADOPT_DRAFT", Reason: "Cannot strip recorded type", Content: c})
		return e
	}), ErrTeamStatus)
	c.ReportedState = "IN_PROGRESS"
	adopted := editTeam(t, m, db, ctx, status.ID, TeamEdit{Version: 1, Action: "ADOPT_DRAFT", Reason: "Correct source status before adoption", Content: c})
	require.Equal(t, "STATUS", adopted.Record.Content.DraftType)
	require.Equal(t, "IN_PROGRESS", adopted.Record.Content.ReportedState)
}

func TestTeamBoardHTTPDraftInventoryAndSourceFailures(t *testing.T) {
	m, db, ctx, _, _ := teamHarness(t)
	draft := createStudio(t, m, db, ctx, StudioDraft{Domain: "TEAM_TASKS", Kind: "ASSISTANT_DRAFT", Content: StudioContent{Title: "Suggestion", Text: "Proposed action", Source: "source:1"}})
	page, e := m.teamBoardHTTP(ctx, &teamBoardInput{ProjectID: "99", Limit: 10, Sort: "id"})
	require.NoError(t, e)
	require.Empty(t, page.Body.Data.Items)
	require.Len(t, page.Body.Data.Drafts, 1)
	require.Equal(t, draft.ID, page.Body.Data.Drafts[0].ID)
	for _, in := range []teamBoardInput{{ProjectID: "bad", Limit: 1, Sort: "id"}, {ProjectID: "99", Limit: 1, Sort: "id", Cursor: "bad"}} {
		_, e := m.teamBoardHTTP(ctx, &in)
		assertStatus(t, e, 422)
	}
	_, e = m.teamCreateHTTP(ctx, &teamCreateInput{ProjectID: "bad"})
	assertStatus(t, e, 422)
	_, e = m.teamEditHTTP(ctx, &teamEditInput{ProjectID: "bad", ID: "1"})
	assertStatus(t, e, 422)
	_, e = m.teamEditHTTP(ctx, &teamEditInput{ProjectID: "99", ID: "bad"})
	assertStatus(t, e, 422)
	injected := errors.New("board query unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("team-board-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.StudioRecords.String() {
			tx.AddError(injected)
		}
	}))
	_, e = m.teamBoardHTTP(ctx, &teamBoardInput{ProjectID: "99", Limit: 10, Sort: "id"})
	assertStatus(t, e, 500)
	require.NoError(t, db.Callback().Query().Remove("team-board-failure"))
	require.NoError(t, db.Table(m.names.StudioRecords.String()).Where("id=?", draft.ID).Update("payload", "invalid").Error)
	_, e = m.teamBoardHTTP(ctx, &teamBoardInput{ProjectID: "99", Limit: 10, Sort: "id"})
	assertStatus(t, e, 500)
}
