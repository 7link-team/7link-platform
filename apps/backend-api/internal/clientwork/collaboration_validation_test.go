package clientwork

import (
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCollaborationRejectsInvalidAndUnauthorizedTransitions(t *testing.T) {
	m, db, _, _ := harness(t)
	internal, client := testContext(t, 2), testContext(t, 4)
	sheet := draftAndPublish(t, m, db)
	testError := func(f func(*gorm.DB) error) { t.Helper(); require.Error(t, db.Transaction(f)) }
	for _, body := range []ChangeBody{{}, {sheet.ID, 0, "Scope"}, {sheet.ID, 1, " "}, {sheet.ID, 1, "Scope"}} {
		testError(func(tx *gorm.DB) error { _, e := m.createChangeOn(client, tx, 3, body); return e })
	}
	testError(func(tx *gorm.DB) error {
		_, e := m.createChangeOn(internal, tx, 3, ChangeBody{sheet.ID, 1, "Scope"})
		return e
	})
	for _, body := range []IssueBody{{}, {Title: "Issue", Description: "Observed", Anchor: &ArtifactAnchor{Kind: "PAGE", Page: 1}}, {Title: "Issue", Description: "Observed", PreviewID: &sheet.ID}} {
		testError(func(tx *gorm.DB) error { _, e := m.createIssueOn(client, tx, 3, body); return e })
	}
	var issue Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		issue, e = m.createIssueOn(client, tx, 3, IssueBody{Title: "Issue", Description: "Observed"})
		return e
	}))
	badDefect := guid.ID(99)
	for _, action := range []IssueAction{{State: "UNKNOWN"}, {State: "CLOSED"}, {State: "RESPONDED"}, {State: "TRIAGED", InternalDefectID: &badDefect}, {State: "TRIAGED", Reply: strings.Repeat("x", 8001)}} {
		testError(func(tx *gorm.DB) error {
			_, e := m.actIssueOn(internal, tx, 3, issue.ID, issue.Version, action)
			return e
		})
	}
	testError(func(tx *gorm.DB) error {
		_, e := m.actIssueOn(internal, tx, 3, issue.ID, 99, IssueAction{State: "TRIAGED"})
		return e
	})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		issue, e = m.actIssueOn(internal, tx, 3, issue.ID, issue.Version, IssueAction{State: "TRIAGED"})
		return e
	}))
	testError(func(tx *gorm.DB) error {
		_, e := m.actIssueOn(internal, tx, 3, issue.ID, issue.Version, IssueAction{State: "TRIAGED"})
		return e
	})
	for _, body := range []PackageBody{{}, {Title: "Release", ReleaseNotes: "Notes", RecipientID: 99}, {Title: "Release", ReleaseNotes: "Notes", RecipientID: 4}} {
		testError(func(tx *gorm.DB) error { _, e := m.createPackageOn(internal, tx, 3, body); return e })
	}
	testError(func(tx *gorm.DB) error { _, e := m.createPreviewOn(internal, tx, 3, PreviewBody{}); return e })
	for _, anchor := range []ArtifactAnchor{{Kind: "UNKNOWN"}, {Kind: "TEXT", Start: -1, End: 2}, {Kind: "IMAGE", X: 1.1}, {Kind: "IMAGE", Y: -1}, {Kind: "PAGE", Page: 0}} {
		require.False(t, anchor.valid())
	}
	for _, anchor := range []ArtifactAnchor{{Kind: "IMAGE", X: 0, Y: 1}, {Kind: "PAGE", Page: 2}, {Kind: "TEXT", Start: 0, End: 2}} {
		require.True(t, anchor.valid())
	}
	for _, body := range []TestCaseBody{{}, {Title: "Test", Steps: "Exercise", Expected: "Success", WorkItemID: 99}} {
		testError(func(tx *gorm.DB) error { _, e := m.writeCaseOn(internal, tx, 3, 0, 0, body); return e })
	}
	var c Receipt
	caseBody := TestCaseBody{Title: "Test", Steps: "Exercise", Expected: "Success", WorkItemID: 11}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; c, e = m.writeCaseOn(internal, tx, 3, 0, 0, caseBody); return e }))
	testError(func(tx *gorm.DB) error { _, e := m.writeCaseOn(internal, tx, 3, c.ID, 99, caseBody); return e })
	for _, body := range []TestPlanBody{{}, {Title: "Plan", CaseIDs: []guid.ID{0}}, {Title: "Plan", CaseIDs: []guid.ID{c.ID, c.ID}}, {Title: "Plan", CaseIDs: []guid.ID{99}}} {
		testError(func(tx *gorm.DB) error { _, e := m.createPlanOn(internal, tx, 3, body); return e })
	}
	var plan Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		plan, e = m.createPlanOn(internal, tx, 3, TestPlanBody{Title: "Plan", CaseIDs: []guid.ID{c.ID}})
		return e
	}))
	for _, body := range []TestRunBody{{}, {Result: "UNKNOWN", Evidence: "Observed"}, {CaseID: 99, CaseVersion: 1, Result: "FAILED", Evidence: "Observed"}, {CaseID: c.ID, CaseVersion: 1, Result: "FAILED", Evidence: "Observed", DefectID: &badDefect}} {
		testError(func(tx *gorm.DB) error { _, e := m.recordRunOn(internal, tx, 3, plan.ID, plan.Version, body); return e })
	}
	testError(func(tx *gorm.DB) error {
		_, e := m.recordRunOn(internal, tx, 3, plan.ID, 99, TestRunBody{Result: "FAILED", Evidence: "Observed"})
		return e
	})
	// The same valid objects remain untouched after all rejected commands.
	visible, e := m.get_test_caseHTTP(internal, &ObjectInput{EntityID: "3", ID: c.ID.String()})
	require.NoError(t, e)
	require.Equal(t, 1, visible.Body.Data.Version)
}
