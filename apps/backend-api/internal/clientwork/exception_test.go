package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestCancellationArchivesOnlyDraftPackagesAndPreservesPublishedReceipts(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[fail], func(t *testing.T) {
			m, db, _, _ := harness(t)
			ctx := testContext(t, 2)
			body := PackageBody{Title: "Release", ReleaseNotes: "Verified notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}
			var draft, sent Receipt
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				draft, e = m.createPackageOn(ctx, tx, 3, body)
				if e != nil {
					return e
				}
				sent, e = m.createPackageOn(ctx, tx, 3, body)
				if e != nil {
					return e
				}
				sent, e = publishBuiltPackage(t, m, ctx, tx, 3, sent.ID, 1)
				return e
			}))
			if fail {
				m.events = failPublisher{}
			}
			e := db.Transaction(func(tx *gorm.DB) error { return m.archiveProjectDraftPackagesOn(ctx, tx, 1, 3) })
			q := query.BindDeliveryPackage(db, m.names)
			d, readErr := q.WithContext(ctx).Where(q.ID.Eq(draft.ID)).First()
			require.NoError(t, readErr)
			s, readErr := q.WithContext(ctx).Where(q.ID.Eq(sent.ID)).First()
			require.NoError(t, readErr)
			require.Nil(t, s.ArchivedAt)
			require.Equal(t, "PUBLISHED", s.State)
			if fail {
				require.Error(t, e)
				require.Nil(t, d.ArchivedAt)
				require.Equal(t, 1, d.Version)
				return
			}
			require.NoError(t, e)
			require.NotNil(t, d.ArchivedAt)
			require.Equal(t, 2, d.Version)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.archiveProjectDraftPackagesOn(ctx, tx, 1, 3) }))
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.editPackageOn(ctx, tx, 3, d.ID, 2, body); return e }), ErrConflict)
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := publishBuiltPackage(t, m, ctx, tx, 3, d.ID, 2); return e }), ErrConflict)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.decidePackageOn(testContext(t, 4), tx, 3, s.ID, s.Version, PackageDecision{SignerName: "Client signer", Confirmed: true, ContentHash: s.ContentHash, Decision: "ACCEPT", Reason: "Verified published content"})
				return e
			}))
			h := query.BindPackageTimeline(db, m.names)
			n, e := h.WithContext(ctx).Where(h.PackageID.Eq(d.ID), h.Kind.Eq("package_archived")).Count()
			require.NoError(t, e)
			require.EqualValues(t, 1, n)
		})
	}
}

func TestCancellationDraftArchivalDatabaseFailuresRollback(t *testing.T) {
	for _, operation := range []string{"query", "update"} {
		t.Run(operation, func(t *testing.T) {
			m, db, _, _ := harness(t)
			ctx := testContext(t, 2)
			_, e := m.create_packageHTTP(ctx, &CreateInput[PackageBody]{EntityID: "3", Key: "draft", Body: PackageBody{Title: "Release", ReleaseNotes: "Notes", RecipientID: 4, Artifacts: []delivery.ClientArtifactRef{{WorkItemID: 11, ResultID: 20, EvidenceID: 30}}}})
			require.NoError(t, e)
			failure := errors.New("archival database failure")
			inject := func(tx *gorm.DB) {
				if tx.Statement.Table == m.names.DeliveryPackage.String() {
					tx.AddError(failure)
				}
			}
			if operation == "query" {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("archive_failure", inject))
			} else {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("archive_failure", inject))
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.archiveProjectDraftPackagesOn(ctx, tx, 1, 3) }), failure)
		})
	}
}

func TestExceptionRequiresReportAndFreezesHumanDecision(t *testing.T) {
	for _, decision := range []string{"CONTINUE", "HOLD", "KILL", "RESHAPE"} {
		t.Run(decision, func(t *testing.T) {
			m, db, _, _ := harness(t)
			tol := toleranceFixture(t, m, db, "REQUIREMENT", 10)
			ex := raisedException(t, m, db, tol)
			cost := int64(300)
			budget := &ToleranceBudget{AppetiteEndAt: now().Add(40 * 24 * time.Hour), AppetiteCostMicros: &cost, ToleranceTimePct: 20, ToleranceCostPct: 15, ToleranceScopePct: 10}
			in := ExceptionDecisionBody{Decision: decision, Reason: "Human reviewed", ResumeCondition: "Wait for input", ReauthProof: "secret-proof"}
			if decision == "CONTINUE" || decision == "RESHAPE" {
				in.Budget = budget
			}
			if decision == "RESHAPE" {
				title := "Reshaped"
				in.Change = &ClientChangeProposal{RequirementID: 10, RequirementVersion: 1, Change: ClientChangeDraft{Title: &title, BaseVersionNo: 1, Summary: "Reshape scope"}}
			}
			run := func(v int) error {
				return db.Transaction(func(tx *gorm.DB) error {
					_, e := m.decideExceptionOn(testContext(t, 2), tx, 3, ex.ID, v, "cookie", in)
					return e
				})
			}
			require.ErrorIs(t, run(1), ErrConflict)
			report := ExceptionReport{Situation: "Forecast exceeds commitment", Options: []ExceptionOption{{Decision: decision, Impact: "Evaluated"}}, Recommendation: decision}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.reportExceptionOn(testContext(t, 2), tx, 3, ex.ID, 1, report)
				return e
			}))
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.reportExceptionOn(testContext(t, 2), tx, 3, ex.ID, 2, report)
				return e
			}))
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.decideExceptionOn(testContext(t, 4), tx, 3, ex.ID, 2, "cookie", in)
				return e
			}))
			require.NoError(t, run(2))
			require.ErrorIs(t, run(2), ErrConflict)
			view, e := m.getExceptionHTTP(testContext(t, 2), &ObjectInput{EntityID: "3", ID: ex.ID.String()})
			require.NoError(t, e)
			out := view.Body.Data
			require.Equal(t, "DECIDED", out.State)
			require.Equal(t, decision, *out.Decision)
			require.Equal(t, report, *out.Report)
			require.NotContains(t, string(out.DecisionPayload), "secret-proof")
			require.NotContains(t, string(out.DecisionPayload), "cookie")
			tolView, e := m.listTolerancesHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 50})
			require.NoError(t, e)
			require.Nil(t, tolView.Body.Data.Items[0].OpenExceptionID)
			if decision == "CONTINUE" || decision == "RESHAPE" {
				require.Equal(t, 2, tolView.Body.Data.Items[0].BaselineNo)
				require.Equal(t, "OK", tolView.Body.Data.Items[0].State)
			}
		})
	}
}

func TestCancellationHTTPReplayKeepsOneDecisionAndChecksCurrentAuthority(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	ex := raisedException(t, m, db, tol)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reportExceptionOn(ctx, tx, 3, ex.ID, 1, ExceptionReport{Situation: "Confirmed project breach", Options: []ExceptionOption{{Decision: "KILL", Impact: "Cancel active scope"}}, Recommendation: "KILL"})
		return e
	}))
	in := &ExceptionDecisionInput{EditInput: EditInput[ExceptionDecisionBody]{EntityID: "3", ID: ex.ID.String(), Key: "cancel-project", IfMatch: "\"2\"", Body: ExceptionDecisionBody{Decision: "KILL", Reason: "Human cancels active project", ReauthProof: "verified-proof"}}, Cookie: "original-session"}
	first, e := m.decideExceptionHTTP(ctx, in)
	require.NoError(t, e)
	in.Body.ReauthProof, in.Cookie = "new-proof", "renewed-session"
	replay, e := m.decideExceptionHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	q := query.BindCommandDecision(db, m.names)
	rows, e := q.WithContext(ctx).Find()
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, "CANCEL", rows[0].Action)
	require.Equal(t, ex.ID, rows[0].SourceID)
	require.Equal(t, 2, rows[0].SourceVersion)
	require.Equal(t, "ROLE:test", rows[0].AuthoritySource)
	in.Body.Reason = "Different cancellation reason"
	_, e = m.decideExceptionHTTP(ctx, in)
	require.Error(t, e, "same key cannot hide a different business decision")
	in.Body.Reason = "Human cancels active project"
	projects.deny = true
	_, e = m.decideExceptionHTTP(ctx, in)
	require.Error(t, e, "receipt replay must recheck current permission")
}

func TestExceptionReportAndDecisionValidationAndRollback(t *testing.T) {
	m, db, _, _ := harness(t)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	ex := raisedException(t, m, db, tol)
	for _, report := range []ExceptionReport{{}, {Situation: "Observed", Options: []ExceptionOption{{Decision: "HOLD", Impact: "Wait"}}, Recommendation: "KILL"}, {Situation: "Observed", Options: []ExceptionOption{{Decision: "HOLD", Impact: "Wait"}, {Decision: "HOLD", Impact: "Duplicate"}}, Recommendation: "HOLD"}} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.reportExceptionOn(testContext(t, 2), tx, 3, ex.ID, 1, report)
			return e
		}), ErrInvalid)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.reportExceptionOn(testContext(t, 2), tx, 3, ex.ID, 1, ExceptionReport{Situation: "Observed", Options: []ExceptionOption{{Decision: "CONTINUE", Impact: "Extend"}}, Recommendation: "CONTINUE"})
		return e
	}))
	before := collaborationSnapshot(t, m, db)
	// New commitment still below the forecast must not close the exception.
	in := ExceptionDecisionBody{Decision: "CONTINUE", Reason: "Reviewed", Budget: &ToleranceBudget{AppetiteEndAt: now().Add(time.Hour)}}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideExceptionOn(testContext(t, 2), tx, 3, ex.ID, 2, "", in)
		return e
	}), ErrInvalid)
	require.Equal(t, before, collaborationSnapshot(t, m, db))
	q := query.BindToleranceException(db, m.names)
	r, e := q.WithContext(t.Context()).Where(q.ID.Eq(ex.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "REPORTED", r.State)
}
