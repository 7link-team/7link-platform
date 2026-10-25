package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestWorkItemSheetSnapshotsReplayHistoryAndStaleTarget(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	var work workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		work, e = m.createWorkOn(ctx, tx, 1, 99, 11, workCreateBody{Title: "Independent work", Type: "TASK", Objective: "Deliver", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Observable result", VerificationMethod: "Exercise it"}}})
		return e
	}))
	in := &sheetCreateInput{EntityID: "99", Key: "work-review", Body: RequirementReviewCreate{WorkItemID: &work.ID, WorkItemVersion: work.Version, ReviewType: "Work review", Reviewers: []guid.ID{11}, Minimum: 1}}
	opened, e := m.createSheetHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.createSheetHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, opened.Body.Data.ID, replay.Body.Data.ID)
	sheet := readSheetTest(t, m, db, ctx, opened.Body.Data.ID)
	require.Equal(t, "WORK_ITEM", sheet.Document.TargetKind)
	require.Equal(t, &work.ID, sheet.WorkItemID)
	require.Zero(t, sheet.RequirementID)
	require.Equal(t, work.Version, sheet.Document.WorkItemVersion)
	require.Equal(t, "Observable result", sheet.Document.Criteria[0].Text)
	duplicate := *in
	duplicate.Key = "work-review-duplicate"
	_, e = m.createSheetHTTP(ctx, &duplicate)
	require.Error(t, e, "one open sheet per work target")
	action := &sheetActionInput{RequirementReviewSheetPath: RequirementReviewSheetPath{EntityID: "99", SheetID: sheet.ID.String()}, Key: "work-opinion"}
	action.Body.Version = sheet.Version
	action.Body.Action = "OPINION"
	action.Body.Opinion = RequirementReviewOpinion{Decision: "PASS"}
	opinion, e := m.actionSheetHTTP(ctx, action)
	require.NoError(t, e)
	history, e := m.listReviewOpinionsHTTP(ctx, &requirementReviewHistoryInput{RequirementReviewSheetPath: action.RequirementReviewSheetPath, Limit: 50})
	require.NoError(t, e)
	require.Len(t, history.Body.Data, 1)
	// A changed target cannot inherit an approval of an older version.
	q := query.BindWorkItems(db, m.service.names)
	_, e = q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1)), q.ID.Eq(work.ID)).UpdateSimple(q.Version.Value(work.Version + 1))
	require.NoError(t, e)
	action.Key = "work-close-stale"
	action.Body.Version = opinion.Body.Data.Version
	action.Body.Action = "CLOSE"
	closed, e := m.actionSheetHTTP(ctx, action)
	require.NoError(t, e)
	require.Equal(t, "CANCELLED", closed.Body.Data.State) //nolint:misspell // Frozen persisted protocol state.
	stored := readSheetTest(t, m, db, ctx, sheet.ID)
	require.Equal(t, "TARGET_UPDATED", stored.Document.Reason)
	row, e := query.BindReviewSheets(db, m.service.names).WithContext(ctx).Where(query.BindReviewSheets(db, m.service.names).ID.Eq(sheet.ID)).First()
	require.NoError(t, e)
	require.Nil(t, row.OpenWorkItemID)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.listReviewOpinionsHTTP(ctx, &requirementReviewHistoryInput{RequirementReviewSheetPath: action.RequirementReviewSheetPath, Limit: 50})
	require.Error(t, e, "history uses current work visibility")
}

func TestReviewTargetRequirementSummaryTracksDecisionLifecycle(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	opened, e := m.createSheetHTTP(ctx, &sheetCreateInput{EntityID: "99", Key: "summary-open", Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1}})
	require.NoError(t, e)
	detail, e := m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, e)
	require.Equal(t, "DRAFT", detail.Status)
	require.NotNil(t, detail.Review)
	require.Equal(t, "OPEN", detail.Review.State)
	action := &sheetActionInput{RequirementReviewSheetPath: RequirementReviewSheetPath{EntityID: "99", SheetID: opened.Body.Data.ID.String()}, Key: "summary-opinion"}
	action.Body.Version = 1
	action.Body.Action = "OPINION"
	action.Body.Opinion = RequirementReviewOpinion{Decision: "REJECT", Criteria: map[string]string{detail.Criteria[0].ID.String(): "Clarify expected result"}}
	_, e = m.actionSheetHTTP(ctx, action)
	require.NoError(t, e)
	action.Key = "summary-close"
	action.Body.Version = 2
	action.Body.Action = "CLOSE"
	_, e = m.actionSheetHTTP(ctx, action)
	require.NoError(t, e)
	detail, e = m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, e)
	require.Equal(t, "DRAFT", detail.Status)
	require.Equal(t, "REJECTED", detail.Review.State)
	require.Equal(t, "NEEDS_WORK", detail.ReviewState)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.patchRequirementOn(ctx, tx, 1, 99, req.ID, 11, detail.Version, requirementPatchBody{Title: ptrSheetString("Changed")})
		return e
	}))
	detail, e = m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, e)
	require.Nil(t, detail.Review, "old verdict is not current after editing")
}
func ptrSheetString(s string) *string { return &s }

func TestReviewTargetOutboxFailureRollsBackWorkSheet(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	var work workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		work, e = m.createWorkOn(ctx, tx, 1, 99, 11, workCreateBody{Title: "Work", Type: "TASK", Objective: "Deliver", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Result", VerificationMethod: "Check"}}})
		return e
	}))
	failure := errors.New("outbox failed")
	m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{WorkItemID: &work.ID, WorkItemVersion: work.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		return e
	}), failure)
	n, e := query.BindReviewSheets(db, m.service.names).WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n)
}

func TestReviewTargetConfiguredWorkKindAndNodeGuards(t *testing.T) {
	m, db, ctx, _ := sheetHarness(t)
	m.reviewTypePolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	var work workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		work, e = m.createWorkOn(ctx, tx, 1, 99, 11, workCreateBody{Title: "Work", Type: "TASK", Objective: "Deliver", AccountablePrincipalID: 11, Criteria: []workCriterionInput{{Text: "Result", VerificationMethod: "Check"}}})
		return e
	}))
	settings := reviewTypeFixture()
	settings.AppliesTo.Requirement = false
	settings.AppliesTo.WorkItemTypes = []string{"BUG"}
	var typ RequirementReviewTypeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		typ, e = m.saveReviewTypeOn(ctx, tx, 1, 0, 0, settings)
		return e
	}))
	in := RequirementReviewCreate{WorkItemID: &work.ID, WorkItemVersion: work.Version, ReviewTypeID: &typ.ID}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createSheetOn(ctx, tx, 99, in); return e }), ErrRequirementStage)
	settings.AppliesTo.WorkItemTypes = []string{"TASK"}
	settings.ReviewNodes = []string{"NEEDS_HUMAN_REVIEW"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, 1, settings); return e }))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createSheetOn(ctx, tx, 99, in); return e }), ErrRequirementStage)
	settings.ReviewNodes = []string{"DRAFT"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveReviewTypeOn(ctx, tx, 1, typ.ID, 2, settings); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.createSheetOn(ctx, tx, 99, in); return e }))
}

func TestReviewTargetHTTPAcceptsWorkWithoutRequirementIdentity(t *testing.T) {
	_, api := humatest.New(t)
	path := "/entities/{entity_id}/review-sheets"
	called := false
	huma.Register(api, huma.Operation{OperationID: "review-work-target-http", Method: "POST", Path: path, DefaultStatus: 201}, func(_ context.Context, in *sheetCreateInput) (*struct{ Body string }, error) {
		called = true
		require.Zero(t, in.Body.RequirementID)
		require.Zero(t, in.Body.RequirementVersion)
		require.Equal(t, guid.ID(9007199254740993), *in.Body.WorkItemID)
		require.Equal(t, 7, in.Body.WorkItemVersion)
		return &struct{ Body string }{Body: "accepted"}, nil
	})
	response := api.Post("/entities/99/review-sheets", "Idempotency-Key: work-target-http", strings.NewReader(`{"work_item_id":"9007199254740993","work_item_version":7,"review_type":"Design","reviewers":["11"],"minimum":1}`))
	require.Equal(t, 201, response.Code, response.Body.String())
	require.True(t, called)
}
