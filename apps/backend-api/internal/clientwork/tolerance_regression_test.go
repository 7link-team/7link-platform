package clientwork

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToleranceRejectedMutationsPreserveCommitmentAndException(t *testing.T) {
	for _, scenario := range []string{"authority", "invalid", "requirement", "duplicate", "id", "missing", "version", "insert", "update", "audit", "forecast_invalid", "forecast_missing", "forecast_version", "forecast_update", "exception_id", "exception_insert", "exception_audit", "forecast_audit"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, projects, _ := harness(t)
			tol := toleranceFixture(t, m, db, "PROJECT", 3)
			ctx := testContext(t, 2)
			q := query.BindTolerance(db, m.names)
			before, err := q.WithContext(ctx).Where(q.ID.Eq(tol.ID)).First()
			require.NoError(t, err)
			body := ToleranceBody{ToleranceBudget: toleranceRecord(before).ToleranceBudget, SubjectKind: "PROJECT", SubjectID: 3}
			id, version := tol.ID, 1
			forecast := ToleranceForecast{EndAt: now().Add(30 * 24 * time.Hour)}
			failure := errors.New("tolerance boundary unavailable")
			switch scenario {
			case "authority":
				projects.deny = true
			case "invalid":
				body.SubjectID = 99
			case "requirement":
				body.SubjectKind, body.SubjectID = "REQUIREMENT", 999
			case "duplicate":
				id, version = 0, 0
			case "id":
				id, version, body.SubjectKind, body.SubjectID = 0, 0, "REQUIREMENT", 10
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			case "missing", "forecast_missing":
				id = 999
			case "version", "forecast_version":
				version = 99
			case "insert":
				id, version, body.SubjectKind, body.SubjectID = 0, 0, "REQUIREMENT", 10
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_tolerance_insert BEFORE INSERT ON g_clientwork_tolerances BEGIN SELECT RAISE(ABORT, 'insert unavailable'); END").Error)
			case "update", "forecast_update":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_tolerance_update BEFORE UPDATE ON g_clientwork_tolerances BEGIN SELECT RAISE(ABORT, 'update unavailable'); END").Error)
			case "audit", "exception_audit", "forecast_audit":
				m.audit = func(_ context.Context, _ *gorm.DB, event audit.DomainEvent) error {
					if scenario != "forecast_audit" || event.EventType == "clientwork.tolerance_forecasted" {
						return failure
					}
					return nil
				}
			case "forecast_invalid":
				forecast.EndAt = time.Time{}
			case "exception_id":
				m.ids = &unavailableIDs{source: m.ids, at: 1}
			case "exception_insert":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_exception_insert BEFORE INSERT ON g_clientwork_exceptions BEGIN SELECT RAISE(ABORT, 'insert unavailable'); END").Error)
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				var e error
				if strings.HasPrefix(scenario, "forecast_") || strings.HasPrefix(scenario, "exception_") {
					_, e = m.forecastToleranceOn(ctx, tx, 3, id, version, forecast)
				} else {
					_, e = m.putToleranceOn(ctx, tx, 3, id, version, body)
				}
				return e
			})
			require.Error(t, err)
			after, err := q.WithContext(ctx).Where(q.ID.Eq(tol.ID)).First()
			require.NoError(t, err)
			require.Equal(t, before, after)
			count, err := query.BindToleranceException(db, m.names).WithContext(ctx).Count()
			require.NoError(t, err)
			require.Zero(t, count, "failed forecast must leave no partial exception")
		})
	}
}

func TestExceptionReportFailuresPreserveOriginalBreachSnapshot(t *testing.T) {
	for _, scenario := range []string{"authority", "missing", "version", "update", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, projects, _ := harness(t)
			tol := toleranceFixture(t, m, db, "PROJECT", 3)
			ex := raisedException(t, m, db, tol)
			ctx := testContext(t, 2)
			q := query.BindToleranceException(db, m.names)
			before, err := q.WithContext(ctx).Where(q.ID.Eq(ex.ID)).First()
			require.NoError(t, err)
			id, version := ex.ID, ex.Version
			switch scenario {
			case "authority":
				projects.deny = true
			case "missing":
				id = 999
			case "version":
				version = 99
			case "update":
				require.NoError(t, db.Exec("CREATE TRIGGER refuse_exception_update BEFORE UPDATE ON g_clientwork_exceptions BEGIN SELECT RAISE(ABORT, 'update unavailable'); END").Error)
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				_, e := m.reportExceptionOn(ctx, tx, 3, id, version, ExceptionReport{Situation: "Confirmed breach", Options: []ExceptionOption{{Decision: "HOLD", Impact: "Wait for source"}}, Recommendation: "HOLD"})
				return e
			})
			require.Error(t, err)
			after, err := q.WithContext(ctx).Where(q.ID.Eq(ex.ID)).First()
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestExceptionInvalidDecisionShapesLeaveFrozenReportUnchanged(t *testing.T) {
	m, db, projects, _ := harness(t)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	ex := raisedException(t, m, db, tol)
	ctx := testContext(t, 2)
	q := query.BindToleranceException(db, m.names)
	before, err := q.WithContext(ctx).Where(q.ID.Eq(ex.ID)).First()
	require.NoError(t, err)
	for _, body := range []ExceptionDecisionBody{
		{Decision: "UNKNOWN", Reason: "Valid reason"},
		{Decision: "CONTINUE", Reason: "Valid reason"},
		{Decision: "HOLD", Reason: "Valid reason"},
		{Decision: "RESHAPE", Reason: "Valid reason", Budget: &ToleranceBudget{AppetiteEndAt: now().Add(time.Hour)}},
		{Decision: "KILL", Reason: "Valid reason", Change: &ClientChangeProposal{}},
		{Decision: "KILL", Reason: "Valid reason", Budget: &ToleranceBudget{}},
	} {
		err = db.Transaction(func(tx *gorm.DB) error {
			_, e := m.decideExceptionOn(ctx, tx, 3, ex.ID, ex.Version, "", body)
			return e
		})
		require.Error(t, err)
		after, e := q.WithContext(ctx).Where(q.ID.Eq(ex.ID)).First()
		require.NoError(t, e)
		require.Equal(t, before, after)
	}
	projects.deny = true
	err = db.Transaction(func(tx *gorm.DB) error {
		_, e := m.decideExceptionOn(ctx, tx, 3, ex.ID, ex.Version, "", ExceptionDecisionBody{Decision: "KILL", Reason: "Valid reason"})
		return e
	})
	require.Error(t, err)
}
