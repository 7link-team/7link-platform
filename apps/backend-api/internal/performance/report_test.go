package performance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportGenerationRejectsInvalidWindowScopeAndUnavailableDependencies(t *testing.T) {
	for _, fault := range []string{"window", "reason", "future", "subject", "ids", "subject_capability", "disabled", "principal_capability"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, scheme, window := reportFixture(t)
			subject := guid.ID(21)
			reason := "Generate validated report"
			want := ErrInvalid
			switch fault {
			case "window":
				window.PeriodEnd = "2024-02-20"
			case "reason":
				reason = " "
			case "future":
				var err error
				window, err = periodWindow("WEEK", "UTC", time.Now().AddDate(1, 0, 0))
				require.NoError(t, err)
			case "subject":
				subject = 22
			case "ids":
				m.ids = nil
				want = ErrUnavailable
			case "subject_capability":
				m.evidence.SubjectOn = nil
				want = ErrUnavailable
			case "principal_capability":
				m.principals.Lock = nil
				want = ErrUnavailable
			case "disabled":
				d := scheme.Definition
				d.Enabled = false
				_, err := m.reviseHTTP(ctx, &reviseInput{SchemeID: scheme.SchemeID.String(), IdempotencyKey: "disabled-generation", Body: schemeBody{BaseVersion: 1, Definition: d, Reason: "Disable report generation"}})
				require.NoError(t, err)
			}
			err := m.transaction(ctx, func(tx *gorm.DB) error {
				_, err := m.generateReportOn(ctx, tx, scheme.SchemeID, subject, window, reason)
				return err
			})
			require.ErrorIs(t, err, want)
			var count int64
			require.NoError(t, db.Table(m.names.Reports.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestReportGenerationRequiresCallerTransaction(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	_, err := m.generateReportOn(ctx, nil, scheme.SchemeID, 21, window, "Report without transaction")
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.generateReportCoreOn(ctx, db, scheme.SchemeID, 21, window, "Report without transaction")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = m.generateReportOn(ctx, db, scheme.SchemeID, 21, window, "Report without transaction")
	require.ErrorIs(t, err, ErrInvalid)
}

type reportIDsFailure struct{ err error }

func (s reportIDsFailure) Next() (int64, error) { return 0, s.err }

type reportRevisionIDs struct {
	next int
	err  error
}

func (s *reportRevisionIDs) Next() (int64, error) {
	s.next++
	if s.next > 1 {
		return 0, s.err
	}
	return 999999, nil
}

func TestReportEvidenceRequiresSelectedSubjectAndValidProviderFacts(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	read := func(subject guid.ID) (EvidenceSet, error) {
		var out EvidenceSet
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.reportEvidenceOn(ctx, tx, scheme, subject, window)
			return err
		})
		return out, err
	}
	_, err := read(22)
	require.ErrorIs(t, err, ErrInvalid)
	provider := m.evidence
	m.evidence = EvidenceReader{}
	_, err = read(21)
	require.ErrorIs(t, err, ErrUnavailable)
	failure := errors.New("subject permission revoked")
	m.evidence = provider
	m.evidence.SubjectOn = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) error { return failure }
	_, err = read(21)
	require.ErrorIs(t, err, failure)
	m.evidence = provider
	m.evidence.ReadOn = func(context.Context, *gorm.DB, EvidenceRequest) (EvidenceSet, error) {
		return EvidenceSet{Metrics: []MetricValue{{Metric: "QUALITY_SCORE", Value: 9000, EvidenceRefs: []string{"missing"}}}}, nil
	}
	_, err = read(21)
	require.ErrorIs(t, err, ErrUnavailable)
	m.evidence.ReadOn = func(context.Context, *gorm.DB, EvidenceRequest) (EvidenceSet, error) { return EvidenceSet{}, nil }
	out, err := read(21)
	require.NoError(t, err)
	require.NotNil(t, out.Facts)
	require.NotNil(t, out.Metrics)
	require.NotNil(t, out.Gaps)
	require.NotNil(t, out.Work)
	require.Empty(t, out.Facts)
	require.Empty(t, out.Metrics)
}

func TestReportRecalculationReadAndLockFailuresPreserveFrozenRevision(t *testing.T) {
	for _, fault := range []string{"missing", "head", "revision", "scheme", "principal", "evidence"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, scheme, window := reportFixture(t)
			first := generateFixtureReport(t, m, db, ctx, scheme, window)
			id := first.ReportID
			failure := errors.New("recalculation dependency failed")
			want := failure
			switch fault {
			case "missing":
				id++
				want = ErrNotFound
			case "head", "revision", "scheme":
				table := m.names.Reports.String()
				if fault == "revision" {
					table = m.names.ReportRevisions.String()
				}
				if fault == "scheme" {
					table = m.names.Schemes.String()
				}
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("recalculation_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(failure)
					}
				}))
			case "principal":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			case "evidence":
				m.evidence.ReadOn = func(context.Context, *gorm.DB, EvidenceRequest) (EvidenceSet, error) { return EvidenceSet{}, failure }
			}
			err := m.transaction(ctx, func(tx *gorm.DB) error {
				_, err := m.recalculateReportOn(ctx, tx, id, 1, 1, "Preserve report on failure", false)
				return err
			})
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("recalculation_failure"))
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, current, err := m.reportOn(ctx, tx, first.ReportID, 0)
				require.NoError(t, err)
				require.Equal(t, first, current)
				return nil
			}))
			var count int64
			require.NoError(t, db.Table(m.names.ReportRevisions.String()).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestReportCalibrationRejectsInvalidReviewReferencesWithoutNewRevision(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	first := generateFixtureReport(t, m, db, ctx, scheme, window)
	for _, refs := range [][]string{nil, {"https://example.test/review", "https://example.test/review"}, {" "}, {"ftp://example.test/review"}, {"https://user:password@example.test/review"}} {
		err := m.transaction(ctx, func(tx *gorm.DB) error {
			_, err := m.calibrateReportOn(ctx, tx, first.ReportID, CalibrationBody{BaseVersion: 1, DimensionKey: "quality", RuleKey: "review", Value: 8000, EvidenceRefs: refs, Reason: "Reject invalid review references"})
			return err
		})
		require.ErrorIs(t, err, ErrInvalid)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, current, err := m.reportOn(ctx, tx, first.ReportID, 0)
		require.NoError(t, err)
		require.Equal(t, first, current)
		return nil
	}))
	var count int64
	require.NoError(t, db.Table(m.names.ReportRevisions.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestReportGenerationStorageFailuresLeaveNoPartialHeadOrRevision(t *testing.T) {
	for _, fault := range []string{"head_read", "head_create", "revision_create", "update", "cas", "ids", "revision_ids", "audit_missing", "principal"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx, scheme, window := reportFixture(t)
			failure := errors.New("report persistence failed")
			want := failure
			switch fault {
			case "head_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("report_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Reports.String() {
						tx.AddError(failure)
					}
				}))
			case "head_create", "revision_create":
				table := m.names.Reports.String()
				if fault == "revision_create" {
					table = m.names.ReportRevisions.String()
				}
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("report_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(failure)
					}
				}))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("report_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Reports.String() {
						tx.AddError(failure)
					}
				}))
			case "cas":
				want = ErrConflict
				require.NoError(t, db.Callback().Update().After("gorm:update").Register("report_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Reports.String() {
						tx.RowsAffected = 0
					}
				}))
			case "ids":
				m.ids = reportIDsFailure{err: failure}
			case "revision_ids":
				m.ids = &reportRevisionIDs{err: failure}
			case "audit_missing":
				m.audit = nil
				want = ErrUnavailable
			case "principal":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			}
			err := m.transaction(ctx, func(tx *gorm.DB) error {
				_, err := m.generateReportOn(ctx, tx, scheme.SchemeID, 21, window, "Generate with storage failure")
				return err
			})
			require.ErrorIs(t, err, want)
			require.NoError(t, db.Callback().Query().Remove("report_failure"))
			require.NoError(t, db.Callback().Create().Remove("report_failure"))
			require.NoError(t, db.Callback().Update().Remove("report_failure"))
			for _, table := range []string{m.names.Reports.String(), m.names.ReportRevisions.String()} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func reportFixture(t *testing.T) (*Module, *gorm.DB, context.Context, RevisionView, PeriodWindow) {
	t.Helper()
	m, db, ctx := harness(t)
	d := definition()
	d.SubjectIDs = []guid.ID{21}
	d.Dimensions = []Dimension{{Key: "quality", Title: "Quality", WeightBPS: 10000, MaximumPoints: 100, Rules: []Rule{
		{Key: "automatic", Description: "Frozen quality receipt", Condition: Condition{Metric: "QUALITY_SCORE", Operator: "GTE", Threshold: 8000}, Effect: "ADD", Points: 80, EvidenceKinds: []string{"RESULT"}},
		{Key: "review", Description: "Professional assessment", Condition: Condition{Metric: "REVIEW_SCORE", Operator: "GTE", Threshold: 7000}, Effect: "ADD", Points: 20, EvidenceKinds: []string{"HUMAN_REVIEW"}},
	}}}
	d.Template.DimensionOrder = []string{"quality"}
	created, err := m.createHTTP(ctx, &createInput{IdempotencyKey: "report-scheme", Body: schemeBody{Definition: d, Reason: "Evidence scoring fixture"}})
	require.NoError(t, err)
	window, err := reportWindowForDate("WEEK", "America/New_York", "2024-02-14")
	require.NoError(t, err)
	m.evidence = EvidenceReader{
		SubjectOn: func(c context.Context, _ *gorm.DB, tenant guid.ID, kind string, id guid.ID) error {
			if authn.TenantIDFromContext(c) != tenant || tenant != 1 || kind != "HUMAN" || id != 21 {
				return ErrForbidden
			}
			return nil
		},
		ReadOn: func(_ context.Context, _ *gorm.DB, request EvidenceRequest) (EvidenceSet, error) {
			return EvidenceSet{Facts: []PerformanceEvidenceFact{{ID: "accepted-result:101", Kind: "RESULT", Version: 2, SHA256: strings.Repeat("a", 64), ProjectID: 3, OccurredAt: request.Period.StartAt.Add(time.Hour), SubjectID: request.SubjectID, SubjectKind: request.SubjectKind, Role: "EXECUTOR", Reference: "/entities/3/work-items/41?result=101"}}, Metrics: []MetricValue{{Metric: "QUALITY_SCORE", Value: 9000, EvidenceRefs: []string{"accepted-result:101"}}}, Gaps: []string{}, Work: []WorkSummaryItem{}}, nil
		},
	}
	return m, db, ctx, created.Body.Data, window
}

func generateFixtureReport(t *testing.T, m *Module, db *gorm.DB, ctx context.Context, scheme RevisionView, window PeriodWindow) ReportView {
	t.Helper()
	var out ReportView
	err := m.transaction(ctx, func(tx *gorm.DB) error {
		var err error
		out, err = m.generateReportOn(ctx, tx, scheme.SchemeID, 21, window, "Generate fixed period evidence")
		return err
	})
	require.NoError(t, err)
	return out
}

func TestReportDuplicateCalibrationConfirmationAndHistoryRemainImmutable(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	first := generateFixtureReport(t, m, db, ctx, scheme, window)
	require.Nil(t, first.Snapshot.TotalScoreBPS, "missing professional review must not become zero or full points")
	duplicate := generateFixtureReport(t, m, db, ctx, scheme, window)
	require.Equal(t, first, duplicate)
	var count int64
	require.NoError(t, db.Table(m.names.Reports.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	_, err := m.confirmReportHTTP(ctx, &reportConfirmInput{ReportID: first.ReportID.String(), IdempotencyKey: "unknown-confirm", Body: ConfirmationBody{BaseVersion: 1, SHA256: first.SHA256, Reason: "Cannot confirm missing evidence"}})
	status(t, err, 422)
	calibration := CalibrationBody{BaseVersion: 1, DimensionKey: "quality", RuleKey: "review", Value: 8000, EvidenceRefs: []string{"https://example.invalid/review/1"}, Reason: "Professional review with cited material"}
	calibrated, err := m.calibrateReportHTTP(ctx, &reportCalibrateInput{ReportID: first.ReportID.String(), IdempotencyKey: "calibration", Body: calibration})
	require.NoError(t, err)
	require.EqualValues(t, 1000000, *calibrated.Body.Data.Snapshot.TotalScoreBPS, "100 exact weighted points, expressed in 1/10000 point units")
	confirmed, err := m.confirmReportHTTP(ctx, &reportConfirmInput{ReportID: first.ReportID.String(), IdempotencyKey: "confirm", Body: ConfirmationBody{BaseVersion: 2, SHA256: calibrated.Body.Data.SHA256, Reason: "Confirm exact reviewed snapshot"}})
	require.NoError(t, err)
	require.Equal(t, "CONFIRMED", confirmed.Body.Data.Snapshot.Status)
	require.Equal(t, 2, confirmed.Body.Data.Snapshot.Confirmation.Revision)
	require.Equal(t, calibrated.Body.Data.SHA256, confirmed.Body.Data.Snapshot.Confirmation.SHA256)
	calibration.BaseVersion = 3
	calibration.Value = 5000
	changed, err := m.calibrateReportHTTP(ctx, &reportCalibrateInput{ReportID: first.ReportID.String(), IdempotencyKey: "second-calibration", Body: calibration})
	require.NoError(t, err)
	require.Equal(t, "PENDING", changed.Body.Data.Snapshot.Status)
	require.Nil(t, changed.Body.Data.Snapshot.Confirmation)
	old, err := m.getReportHTTP(ctx, &reportReadInput{ReportID: first.ReportID.String(), Version: 3})
	require.NoError(t, err)
	require.Equal(t, confirmed.Body.Data, old.Body.Data, "calibration cannot overwrite the prior confirmation")
	firstAgain, err := m.getReportHTTP(ctx, &reportReadInput{ReportID: first.ReportID.String(), Version: 1})
	require.NoError(t, err)
	require.Equal(t, first, firstAgain.Body.Data)
	history, err := m.reportHistoryHTTP(ctx, &reportHistoryInput{ReportID: first.ReportID.String(), Limit: 2})
	require.NoError(t, err)
	require.True(t, history.Body.Data.HasMore)
	require.Equal(t, 4, history.Body.Data.Items[0].Version)
	require.Equal(t, 2, history.Body.Data.NextOffset)
}

func TestReportRecalculationPreservesCutoffAndFreezesRulesAndFacts(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	first := generateFixtureReport(t, m, db, ctx, scheme, window)
	d := scheme.Definition
	d.Template.Title = "New template"
	d.Dimensions[0].Rules[0].Points = 50
	revised, err := m.reviseHTTP(ctx, &reviseInput{SchemeID: scheme.SchemeID.String(), IdempotencyKey: "new-score-rules", Body: schemeBody{BaseVersion: 1, Definition: d, Reason: "Explicit new rule revision"}})
	require.NoError(t, err)
	duplicate := generateFixtureReport(t, m, db, ctx, revised.Body.Data, window)
	require.Equal(t, first, duplicate, "rule revision must not create another logical period report")
	recalculated, err := m.recalculateReportHTTP(ctx, &reportRecalculateInput{ReportID: first.ReportID.String(), IdempotencyKey: "recalculate", Body: RecalculateBody{BaseVersion: 1, SchemeVersion: 2, Reason: "Supplement fixed-cutoff evidence with selected rules"}})
	require.NoError(t, err)
	require.Equal(t, window, recalculated.Body.Data.Snapshot.Period)
	require.Equal(t, 2, recalculated.Body.Data.Snapshot.Scheme.Version)
	require.Equal(t, "New template", recalculated.Body.Data.Snapshot.Scheme.Definition.Template.Title)
	old, err := m.getReportHTTP(ctx, &reportReadInput{ReportID: first.ReportID.String(), Version: 1})
	require.NoError(t, err)
	require.Equal(t, first, old.Body.Data)
	_, err = m.recalculateReportHTTP(ctx, &reportRecalculateInput{ReportID: first.ReportID.String(), IdempotencyKey: "stale-recalculate", Body: RecalculateBody{BaseVersion: 1, SchemeVersion: 2, Reason: "Stale snapshot"}})
	status(t, err, 409)
	_, err = m.confirmReportHTTP(ctx, &reportConfirmInput{ReportID: first.ReportID.String(), IdempotencyKey: "wrong-digest", Body: ConfirmationBody{BaseVersion: 2, SHA256: strings.Repeat("f", 64), Reason: "Wrong hash"}})
	status(t, err, 409)
}

func TestReportFinalDaySupplementIsExplicitAndPreservesOriginalRevision(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	first := generateFixtureReport(t, m, db, ctx, scheme, window)
	reader := m.evidence.ReadOn
	var observed PeriodWindow
	m.evidence.ReadOn = func(ctx context.Context, tx *gorm.DB, request EvidenceRequest) (EvidenceSet, error) {
		observed = request.Period
		return reader(ctx, tx, request)
	}
	updated, err := m.recalculateReportHTTP(ctx, &reportRecalculateInput{ReportID: first.ReportID.String(), IdempotencyKey: "final-day-supplement", Body: RecalculateBody{BaseVersion: 1, SchemeVersion: 1, Reason: "Supplement the previously uncovered final day after period end", IncludeUncovered: true}})
	require.NoError(t, err)
	require.Equal(t, window.EndAt, observed.CutoffAt)
	require.Equal(t, window.EndAt, updated.Body.Data.Snapshot.Period.UncoveredStartAt)
	require.Equal(t, window.TriggerAt, updated.Body.Data.Snapshot.Period.TriggerAt)
	require.Equal(t, "PENDING", updated.Body.Data.Snapshot.Status)
	old, err := m.getReportHTTP(ctx, &reportReadInput{ReportID: first.ReportID.String(), Version: 1})
	require.NoError(t, err)
	require.Equal(t, first, old.Body.Data, "supplement cannot rewrite the original cutoff, score or evidence")

	_, err = reportRecalculationWindow(window, true, window.EndAt.Add(-time.Nanosecond))
	require.ErrorIs(t, err, ErrInvalid, "cannot claim the final day before period end")
	unchanged, err := reportRecalculationWindow(window, false, window.EndAt.Add(-time.Nanosecond))
	require.NoError(t, err)
	require.Equal(t, window, unchanged)
	complete, err := reportRecalculationWindow(window, true, window.EndAt)
	require.NoError(t, err)
	require.Equal(t, window.EndAt, complete.CutoffAt)

}

func TestReportMissingProviderScopeCurrentAuthorityAndAtomicFailures(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	reader := m.evidence
	m.evidence = EvidenceReader{}
	err := m.transaction(ctx, func(tx *gorm.DB) error {
		_, err := m.generateReportOn(ctx, tx, scheme.SchemeID, 21, window, "Missing provider")
		return err
	})
	require.ErrorIs(t, err, ErrUnavailable)
	m.evidence = reader
	for _, subject := range []guid.ID{0, 22} {
		err = m.transaction(ctx, func(tx *gorm.DB) error {
			_, err := m.generateReportOn(ctx, tx, scheme.SchemeID, subject, window, "Wrong actual subject")
			return err
		})
		require.Error(t, err)
	}
	originalAudit, originalEvents := m.audit, m.events
	for _, sink := range []string{"audit", "outbox"} {
		m.audit, m.events = originalAudit, originalEvents
		if sink == "audit" {
			m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
		} else {
			m.events = publisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
				return 0, errors.New("outbox unavailable")
			})
		}
		err = m.transaction(ctx, func(tx *gorm.DB) error {
			_, err := m.generateReportOn(ctx, tx, scheme.SchemeID, 21, window, "Atomic rollback")
			return err
		})
		require.Error(t, err)
		var heads, revisions int64
		require.NoError(t, db.Table(m.names.Reports.String()).Count(&heads).Error)
		require.NoError(t, db.Table(m.names.ReportRevisions.String()).Count(&revisions).Error)
		require.Zero(t, heads)
		require.Zero(t, revisions)
	}
	m.audit, m.events = originalAudit, originalEvents
	body := GenerateReportBody{SchemeID: scheme.SchemeID, SubjectID: 21, Cadence: "WEEK", Timezone: window.Timezone, AnchorDate: "2024-02-14", Reason: "Generate with stable retry"}
	in := &reportGenerateInput{IdempotencyKey: "generate-once", Body: body}
	first, err := m.generateReportHTTP(ctx, in)
	require.NoError(t, err)
	replayed, err := m.generateReportHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data, replayed.Body.Data)
	policy := m.policy
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.generateReportHTTP(ctx, in)
	status(t, err, 403)
	m.policy = policy
	_, err = m.getReportHTTP(authn.WithClaims(ctx, &authn.Claims{TenantID: 2, PrincipalID: 11}), &reportReadInput{ReportID: first.Body.Data.ReportID.String()})
	status(t, err, 404)
}

func TestScoreReportExactWeightClampUnknownAndSummary(t *testing.T) {
	snapshot := ReportSnapshot{SubjectKind: "HUMAN", Scheme: RevisionView{Definition: SchemeDefinition{Dimensions: []Dimension{
		{Key: "a", WeightBPS: 3333, MaximumPoints: 2, Rules: []Rule{{Key: "plus", Condition: Condition{Metric: "QUALITY_SCORE", Operator: "EQ", Threshold: 9000}, Points: 3, Effect: "ADD", EvidenceKinds: []string{"RESULT"}}}},
		{Key: "b", WeightBPS: 6667, MaximumPoints: 2, Rules: []Rule{{Key: "minus", Condition: Condition{Metric: "QUALITY_SCORE", Operator: "LTE", Threshold: 9000}, Points: 3, Effect: "SUBTRACT", EvidenceKinds: []string{"RESULT"}}}},
	}}}, Evidence: EvidenceSet{Facts: []PerformanceEvidenceFact{{ID: "one", Kind: "RESULT"}}, Metrics: []MetricValue{{Metric: "QUALITY_SCORE", Value: 9000, EvidenceRefs: []string{"one"}}}}}
	scoreReport(&snapshot)
	require.EqualValues(t, 6666, *snapshot.TotalScoreBPS, "2*3333 + 0*6667 retains exact fractional weighted points")
	snapshot.Evidence.Gaps = []string{"Historical attribution unavailable"}
	scoreReport(&snapshot)
	require.EqualValues(t, 6666, *snapshot.TotalScoreBPS, "an unrelated work-summary gap cannot erase independently measured quality")
	snapshot.Scheme.Definition.Dimensions[0].Rules = append(snapshot.Scheme.Definition.Dimensions[0].Rules, Rule{Key: "unsupported", Condition: Condition{Metric: "ON_TIME_RATIO", Operator: "GTE", Threshold: 8000}, Points: 1, Effect: "ADD", EvidenceKinds: []string{"PLAN"}})
	scoreReport(&snapshot)
	require.Nil(t, snapshot.TotalScoreBPS)
	require.NotNil(t, snapshot.Dimensions[0].Rules[0].Value)
	require.Nil(t, snapshot.Dimensions[0].Rules[1].Value, "a missing metric still stays unassessed")
	for _, kind := range []string{"PROJECT", "TEAM"} {
		snapshot.SubjectKind = kind
		scoreReport(&snapshot)
		require.Equal(t, "SUMMARY", snapshot.Status)
		require.Nil(t, snapshot.TotalScoreBPS)
		require.Empty(t, snapshot.Dimensions)
	}
	require.NotEqual(t, assessmentKey("a/b", "c"), assessmentKey("a", "b/c"))
}

func TestReportSnapshotTamperingIsRejected(t *testing.T) {
	m, db, ctx, scheme, window := reportFixture(t)
	first := generateFixtureReport(t, m, db, ctx, scheme, window)
	require.NoError(t, db.Table(m.names.ReportRevisions.String()).Where("id=?", first.ID).Update("snapshot", "{}").Error)
	_, err := m.getReportHTTP(ctx, &reportReadInput{ReportID: first.ReportID.String()})
	status(t, err, 503)
}

func TestReportCommandLocksSubjectAndActorInCanonicalOrder(t *testing.T) {
	m, _, ctx, scheme, window := reportFixture(t)
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 31, SubjectType: authn.SubjectTypePrincipal})
	locked := []guid.ID{}
	m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locked = append(locked, id); return nil }
	_, err := m.generateReportHTTP(ctx, &reportGenerateInput{IdempotencyKey: "ordered-principals", Body: GenerateReportBody{SchemeID: scheme.SchemeID, SubjectID: 21, Cadence: window.Cadence, Timezone: window.Timezone, AnchorDate: "2024-02-14", Reason: "Lock all principals before scheme"}})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(locked), 2)
	require.Equal(t, []guid.ID{21, 31}, locked[:2], "lower subject ID must be locked before the current actor")
}
