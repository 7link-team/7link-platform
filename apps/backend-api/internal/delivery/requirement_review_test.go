package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
)

func TestRequirementReviewDecisionLifecycleAndImmutableHistory(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "approve", true: "request_changes"}[rejected], func(t *testing.T) {
			m, db, ctx, req := sheetHarness(t)
			sheet, err := m.createSheetHTTP(ctx, &sheetCreateInput{EntityID: "99", Key: "open", Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Requirement", Reviewers: []guid.ID{11}, Minimum: 1}})
			require.NoError(t, err)
			detail, err := m.readRequirement(ctx, db, 1, 99, req.ID, 11)
			require.NoError(t, err)
			require.Equal(t, "REVIEW_PENDING", detail.ReviewState)
			input := &sheetActionInput{RequirementReviewSheetPath: RequirementReviewSheetPath{EntityID: "99", SheetID: sheet.Body.Data.ID.String()}, Key: "opinion"}
			input.Body.Version = 1
			input.Body.Action = "OPINION"
			input.Body.Opinion = RequirementReviewOpinion{Decision: "PASS", Criteria: map[string]string{}}
			if rejected {
				input.Body.Opinion.Decision = "REJECT"
				input.Body.Opinion.Criteria[detail.Criteria[0].ID.String()] = "Missing expected result"
			}
			_, err = m.actionSheetHTTP(ctx, input)
			require.NoError(t, err)
			input.Body.Version = 2
			input.Body.Action = "CLOSE"
			input.IfMatch = fmtETag(2)
			input.Key = "close"
			closed, err := m.actionSheetHTTP(ctx, input)
			require.NoError(t, err)
			replay, err := m.actionSheetHTTP(ctx, input)
			require.NoError(t, err)
			require.Equal(t, "true", replay.Replayed)
			require.Equal(t, closed.Body.Data, replay.Body.Data)
			m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
				return governance.AuthorityDecision{Allowed: false}, nil
			}
			_, err = m.actionSheetHTTP(ctx, input)
			require.Error(t, err, "current revoked decision authority denies an otherwise valid replay")

			detail, err = m.readRequirement(ctx, db, 1, 99, req.ID, 11)
			require.NoError(t, err)
			require.Len(t, detail.Decisions, 1)
			decision := detail.Decisions[0]
			require.Equal(t, sheet.Body.Data.ID, decision.SheetID)
			require.Equal(t, "ROLE:reviewer", decision.AuthoritySource)
			require.Equal(t, detail.Version, decision.RequirementVersion)
			if rejected {
				require.Equal(t, "REQUEST_CHANGES", decision.Action)
				require.Equal(t, "NEEDS_WORK", detail.ReviewState)
				title := "Revised requirement"
				_, err = m.patchRequirementHTTP(ctx, &requirementEditInput[requirementPatchBody]{EntityID: "99", RequirementID: req.ID.String(), IfMatch: fmtETag(detail.Version), Body: requirementPatchBody{Title: &title}})
				require.NoError(t, err)
			} else {
				require.Equal(t, "APPROVE", decision.Action)
				require.Equal(t, "APPROVED", detail.ReviewState)
				require.Equal(t, "BASELINED", detail.Status)
				require.Len(t, detail.Versions, 1)
				require.Len(t, decision.SuiteHash, 64)
				require.Equal(t, detail.Versions[0].SuiteHash, decision.SuiteHash)
			}
			input.Key = "closed-again"
			input.Body.Version = 3
			input.IfMatch = fmtETag(3)
			_, err = m.actionSheetHTTP(ctx, input)
			require.Error(t, err)
			q := query.BindReviewSheets(db, m.service.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(sheet.Body.Data.ID)).First()
			require.NoError(t, err)
			stored, err := sheetView(row)
			require.NoError(t, err)
			require.Equal(t, decision, *stored.Document.Decision)
		})
	}
}
func TestRequirementReviewDecisionAuthorityAndAuditRollback(t *testing.T) {
	for _, stage := range []string{"authority", "audit", "outbox"} {
		t.Run(stage, func(t *testing.T) {
			m, db, ctx, req := sheetHarness(t)
			var sheet RequirementReviewSheetView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Requirement", Reviewers: []guid.ID{11}, Minimum: 1})
				return err
			}))
			in := &sheetActionInput{}
			in.Body.Version = 1
			in.Body.Action = "OPINION"
			in.Body.Opinion = RequirementReviewOpinion{Decision: "PASS"}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				sheet, err = m.actionSheetOn(ctx, tx, 99, sheet.ID, in)
				return err
			}))
			failure := errors.New("dependency failed")
			if stage == "authority" {
				m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{}, failure
				}
			} else if stage == "outbox" {
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			} else {
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			}
			in.Body.Version = 2
			in.Body.Action = "CLOSE"
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.actionSheetOn(ctx, tx, 99, sheet.ID, in); return err }), failure)
			detail, err := m.readRequirement(ctx, db, 1, 99, req.ID, 11)
			require.NoError(t, err)
			require.Equal(t, "REVIEW_PENDING", detail.ReviewState)
			require.Empty(t, detail.Decisions)
			require.Empty(t, detail.Versions)
		})
	}
}

func TestRequirementFrozenCasesSurviveEditsAndNewScopeVersion(t *testing.T) {
	m, db, ctx, req, change := scopeHarness(t)
	work, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: workCreateBody{Title: "Implement", Type: "TASK", Objective: "Implement requirement", AccountablePrincipalID: 11, RequirementID: &req.ID, BaselineVersionNo: &req.CurrentVersionNo, Criteria: []workCriterionInput{{Text: "Output", VerificationMethod: "Inspect output"}}}})
	require.NoError(t, err)
	caseVersion := 1
	m.requirementCases = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, ids []guid.ID) ([]RequirementCaseSnapshot, error) {
		require.Equal(t, []guid.ID{work.Body.Data.ID}, ids)
		return []RequirementCaseSnapshot{{ID: 700, Version: caseVersion, WorkItemID: ids[0], Title: "Verify", Steps: "Perform checks", Expected: "Expected output"}}, nil
	}
	var v2 scopeChangeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		v2, err = m.scopeChangeOn(ctx, tx, 1, 99, req.ID, 11, req.Version, "", change)
		return err
	}))
	q := query.BindRequirementVersions(db, m.service.names)
	before, err := q.WithContext(ctx).Where(q.RequirementID.Eq(req.ID), q.VersionNo.Eq(2)).First()
	require.NoError(t, err)
	require.Contains(t, before.AcceptanceSuite, `"steps":"Perform checks"`)
	require.Contains(t, before.AcceptanceSuite, `"version":1`)
	count, err := frozenSuiteCaseCount(before)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	caseVersion = 2
	newTitle := "Second revised requirement"
	change.Title = &newTitle
	change.BaseVersionNo = 2
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.scopeChangeOn(ctx, tx, 1, 99, req.ID, 11, v2.RequirementVersion, "", change)
		return err
	}))
	after, err := q.WithContext(ctx).Where(q.RequirementID.Eq(req.ID), q.VersionNo.Eq(2)).First()
	require.NoError(t, err)
	require.Equal(t, before.AcceptanceSuite, after.AcceptanceSuite)
	require.Equal(t, before.SuiteHash, after.SuiteHash)
	latest, err := q.WithContext(ctx).Where(q.RequirementID.Eq(req.ID), q.VersionNo.Eq(3)).First()
	require.NoError(t, err)
	require.Contains(t, latest.AcceptanceSuite, `"version":2`)
	require.NotEqual(t, before.SuiteHash, latest.SuiteHash)
	tampered := *latest
	tampered.AcceptanceSuite += " "
	_, err = frozenSuiteCaseCount(&tampered)
	require.Error(t, err)
	legacy := model.RequirementVersion{AcceptanceSuite: "{}"}
	count, err = frozenSuiteCaseCount(&legacy)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestRequirementReviewCloseRechecksTargetWriteScope(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	var sheet RequirementReviewSheetView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
		return err
	}))
	in := &sheetActionInput{}
	in.Body.Version = 1
	in.Body.Action = "OPINION"
	in.Body.Opinion = RequirementReviewOpinion{Decision: "PASS"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.actionSheetOn(ctx, tx, 99, sheet.ID, in)
		return err
	}))
	m.workPolicy = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string, _ guid.ID) (authz.DataConstraint, error) {
		if permission == "requirement_write" {
			return authz.DataConstraint{OwnerIDs: []string{"12"}}, nil
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	in.Body.Version = 2
	in.Body.Action = "CLOSE"
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.actionSheetOn(ctx, tx, 99, sheet.ID, in); return err }), ErrWorkForbidden)
	detail, err := m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, err)
	require.Equal(t, "REVIEW_PENDING", detail.ReviewState)
	require.Empty(t, detail.Decisions)
	require.Empty(t, detail.Versions)
}
func TestRequirementReviewOpeningAndCancellationChangeETag(t *testing.T) {
	m, db, ctx, req := sheetHarness(t)
	before, err := m.getRequirementHTTP(ctx, &RequirementGetInput{EntityID: "99", RequirementID: req.ID.String()})
	require.NoError(t, err)
	opened, err := m.createSheetHTTP(ctx, &sheetCreateInput{EntityID: "99", Key: "etag-open", Body: RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: req.Version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1}})
	require.NoError(t, err)
	current, err := m.getRequirementHTTP(ctx, &RequirementGetInput{EntityID: "99", RequirementID: req.ID.String()})
	require.NoError(t, err)
	require.NotEqual(t, before.ETag, current.ETag)
	title := "Stale edit"
	_, err = m.patchRequirementHTTP(ctx, &requirementEditInput[requirementPatchBody]{EntityID: "99", RequirementID: req.ID.String(), IfMatch: before.ETag, Body: requirementPatchBody{Title: &title}})
	require.Error(t, err)
	in := &sheetActionInput{RequirementReviewSheetPath: RequirementReviewSheetPath{EntityID: "99", SheetID: opened.Body.Data.ID.String()}, Key: "etag-cancel", IfMatch: fmtETag(1)}
	in.Body.Version = 1
	in.Body.Action = "CANCEL"
	in.Body.Reason = "Need another reviewer"
	_, err = m.actionSheetHTTP(ctx, in)
	require.NoError(t, err)
	after, err := m.getRequirementHTTP(ctx, &RequirementGetInput{EntityID: "99", RequirementID: req.ID.String()})
	require.NoError(t, err)
	require.NotEqual(t, current.ETag, after.ETag)
	require.Equal(t, "NEEDS_WORK", after.Body.Data.ReviewState)
	_ = db
}

func TestRequirementLegacyBaselineRejectEditResubmitApprove(t *testing.T) {
	m, db, ctx, req, change := scopeHarness(t)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	q := query.BindRequirementVersions(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.RequirementID.Eq(req.ID)).UpdateSimple(q.SuiteHash.Value(""), q.AcceptanceSuite.Value("{}"))
	require.NoError(t, err)
	review := func(version int, decision string) (RequirementReviewSheetView, error) {
		t.Helper()
		var sheet RequirementReviewSheetView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			sheet, err = m.createSheetOn(ctx, tx, 99, RequirementReviewCreate{RequirementID: req.ID, RequirementVersion: version, ReviewType: "Review", Reviewers: []guid.ID{11}, Minimum: 1})
			return err
		})
		if err != nil {
			return sheet, err
		}
		in := &sheetActionInput{}
		in.Body.Version = 1
		in.Body.Action = "OPINION"
		in.Body.Opinion = RequirementReviewOpinion{Decision: decision, Criteria: map[string]string{}}
		if decision == "REJECT" {
			in.Body.Opinion.Criteria[sheet.Document.Criteria[0].ID.String()] = "Clarify expected output"
		}
		err = db.Transaction(func(tx *gorm.DB) error {
			var err error
			sheet, err = m.actionSheetOn(ctx, tx, 99, sheet.ID, in)
			return err
		})
		if err != nil {
			return sheet, err
		}
		in.Body.Version = 2
		in.Body.Action = "CLOSE"
		err = db.Transaction(func(tx *gorm.DB) error {
			next, err := m.actionSheetOn(ctx, tx, 99, sheet.ID, in)
			if err == nil {
				sheet = next
			}
			return err
		})
		return sheet, err
	}
	sheet, err := review(req.Version, "PASS")
	require.ErrorIs(t, err, ErrRequirementSuiteMissing)
	current, err := m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, err)
	require.Empty(t, current.Decisions)
	require.Equal(t, "REVIEW_PENDING", current.ReviewState)
	// The failed approval kept the open review intact, so a reviewer can reject
	// the same sheet without silently fabricating an old suite.
	in := &sheetActionInput{}
	in.Body.Version = sheet.Version
	in.Body.Action = "OPINION"
	in.Body.Opinion = RequirementReviewOpinion{Decision: "REJECT", Criteria: map[string]string{sheet.Document.Criteria[0].ID.String(): "Revise scope"}}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.actionSheetOn(ctx, tx, 99, sheet.ID, in)
		return err
	}))
	in.Body.Version = sheet.Version
	in.Body.Action = "CLOSE"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		sheet, err = m.actionSheetOn(ctx, tx, 99, sheet.ID, in)
		return err
	}))
	current, err = m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, err)
	require.Equal(t, "NEEDS_WORK", current.ReviewState)
	var changed scopeChangeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		changed, err = m.scopeChangeOn(ctx, tx, 1, 99, req.ID, 11, current.Version, "", change)
		return err
	}))
	sheet, err = review(changed.RequirementVersion, "PASS")
	require.NoError(t, err)
	require.Equal(t, "APPROVE", sheet.Document.Decision.Action)
	require.Equal(t, 2, sheet.Document.Decision.VersionNo)
	require.Len(t, sheet.Document.Decision.SuiteHash, 64)
	current, err = m.readRequirement(ctx, db, 1, 99, req.ID, 11)
	require.NoError(t, err)
	require.Equal(t, "APPROVED", current.ReviewState)
	require.Len(t, current.Decisions, 2)
	require.Equal(t, "REQUEST_CHANGES", current.Decisions[1].Action)
}
func TestRequirementFrozenSuiteRejectsEncodedStorageOverflow(t *testing.T) {
	m, db, ctx, req, _ := scopeHarness(t)
	_, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: workCreateBody{Title: "Implement", Type: "TASK", Objective: "Implement requirement", AccountablePrincipalID: 11, RequirementID: &req.ID, BaselineVersionNo: &req.CurrentVersionNo, Criteria: []workCriterionInput{{Text: "Output", VerificationMethod: "Inspect"}}}})
	require.NoError(t, err)
	for _, scenario := range []struct {
		name, unit string
		cases      int
		fails      bool
	}{{"small_ascii", "a", 1, false}, {"ascii", "a", 9, true}, {"utf8", "\u754c", 3, true}, {"escaped", "\x00", 2, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			m.requirementCases = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) ([]RequirementCaseSnapshot, error) {
				cases := make([]RequirementCaseSnapshot, scenario.cases)
				for i := range cases {
					cases[i] = RequirementCaseSnapshot{ID: guid.ID(i + 1), Version: 1, WorkItemID: ids[0], Title: "Case", Steps: strings.Repeat(scenario.unit, 8000), Expected: "Result"}
				}
				return cases, nil
			}
			version := &model.RequirementVersion{RequirementID: req.ID, VersionNo: 2}
			err := db.Transaction(func(tx *gorm.DB) error {
				return m.freezeRequirementSuiteOn(ctx, tx, &model.Requirement{TenantID: 1, ProjectID: 99, ID: req.ID}, version)
			})
			if scenario.fails {
				require.ErrorIs(t, err, ErrInvalidArgument)
				require.Empty(t, version.SuiteHash)
				require.Empty(t, version.AcceptanceSuite)
			} else {
				require.NoError(t, err)
				require.LessOrEqual(t, len(version.AcceptanceSuite), 65535)
			}
		})
	}
}
