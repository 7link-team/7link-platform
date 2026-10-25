package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

func toleranceFixture(t *testing.T, m *Module, db *gorm.DB, kind string, subject guid.ID) Receipt {
	t.Helper()
	cost, scope := int64(100), int64(10)
	var out Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.putToleranceOn(testContext(t, 2), tx, 3, 0, 0, ToleranceBody{ToleranceBudget: ToleranceBudget{now().Add(10 * 24 * time.Hour), &cost, &scope, 20, 15, 10}, SubjectKind: kind, SubjectID: subject})
		return e
	}))
	return out
}
func raisedException(t *testing.T, m *Module, db *gorm.DB, tol Receipt) ExceptionView {
	t.Helper()
	cost := int64(200)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.forecastToleranceOn(testContext(t, 2), tx, 3, tol.ID, tol.Version, ToleranceForecast{EndAt: now().Add(20 * 24 * time.Hour), CostMicros: &cost})
		return e
	}))
	page, e := m.listExceptionsHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 50})
	require.NoError(t, e)
	require.NotEmpty(t, page.Body.Data.Items)
	return page.Body.Data.Items[len(page.Body.Data.Items)-1]
}
func TestToleranceForecastRaisesOneFrozenExceptionAndCannotSilentlyExtend(t *testing.T) {
	m, db, _, _ := harness(t)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	ex := raisedException(t, m, db, tol)
	require.ElementsMatch(t, []string{"TIME", "COST"}, ex.BreachedDimensions)
	require.Equal(t, "RAISED", ex.State)
	var original *model.Tolerance
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		original, e = m.toleranceOn(testContext(t, 2), tx, 3, tol.ID)
		return e
	}))
	before := *original
	e := db.Transaction(func(tx *gorm.DB) error {
		_, e := m.putToleranceOn(testContext(t, 2), tx, 3, tol.ID, original.Version, ToleranceBody{ToleranceBudget: ToleranceBudget{now().Add(90 * 24 * time.Hour), nil, nil, 20, 15, 10}, SubjectKind: "PROJECT", SubjectID: 3})
		return e
	})
	require.ErrorIs(t, e, ErrOpenException)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		r, e := m.toleranceOn(testContext(t, 2), tx, 3, tol.ID)
		require.Equal(t, before, rValue(r))
		return e
	}))
	// A recovered forecast must not close an unresolved exception, duplicate it,
	// or rewrite its original breach snapshot.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.forecastToleranceOn(testContext(t, 2), tx, 3, tol.ID, original.Version, ToleranceForecast{EndAt: now()})
		return e
	}))
	page, e := m.listExceptionsHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 50})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.Equal(t, ex, page.Body.Data.Items[0])
	_, e = m.listExceptionsHTTP(testContext(t, 4), &ListInput{EntityID: "3", Limit: 50})
	require.Error(t, e)
}
func rValue(r *model.Tolerance) model.Tolerance { return *r }

func TestToleranceArithmeticBoundariesAndForecastClassification(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cost := int64(math.MaxInt64)
	forecast := cost
	r := &model.Tolerance{CreatedAt: start, AppetiteEndAt: start.Add(10 * 24 * time.Hour), AppetiteCostMicros: &cost, ForecastCostMicros: &forecast, ToleranceCostPct: 100, ToleranceTimePct: 20}
	state, dims := toleranceState(r)
	require.Equal(t, "OK", state)
	require.Empty(t, dims)
	end := start.Add(12 * 24 * time.Hour)
	r.ForecastEndAt = &end
	state, _ = toleranceState(r)
	require.Equal(t, "AT_RISK", state)
	end = end.Add(time.Millisecond)
	state, dims = toleranceState(r)
	require.Equal(t, "EXCEPTION", state)
	require.Equal(t, []string{"TIME"}, dims)
	require.False(t, exceeds(math.MaxInt64, math.MaxInt64, 100))
	require.True(t, exceeds(math.MaxInt64, math.MaxInt64/2, 0))
	_, e := addNonnegative(&cost, 1)
	require.ErrorIs(t, e, ErrInvalid)
	zero := int64(0)
	_, e = addNonnegative(&zero, -1)
	require.ErrorIs(t, e, ErrInvalid)
	n, e := addNonnegative(nil, math.MinInt64)
	require.NoError(t, e)
	require.Nil(t, n)
}

func TestToleranceDatabaseAndOutboxFailuresRollBackEveryMutation(t *testing.T) {
	for _, operation := range []string{"create", "forecast", "report"} {
		t.Run(operation, func(t *testing.T) {
			m, db, _, _ := harness(t)
			tol := toleranceFixture(t, m, db, "PROJECT", 3)
			ex := raisedException(t, m, db, tol)
			before := collaborationSnapshot(t, m, db)
			failure := errors.New("audit failure")
			m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			e := db.Transaction(func(tx *gorm.DB) error {
				switch operation {
				case "create":
					_, e := m.putToleranceOn(testContext(t, 2), tx, 3, 0, 0, ToleranceBody{ToleranceBudget: ToleranceBudget{now().Add(time.Hour), nil, nil, 0, 0, 0}, SubjectKind: "REQUIREMENT", SubjectID: 10})
					return e
				case "forecast":
					_, e := m.forecastToleranceOn(testContext(t, 2), tx, 3, tol.ID, 2, ToleranceForecast{EndAt: now()})
					return e
				default:
					_, e := m.reportExceptionOn(testContext(t, 2), tx, 3, ex.ID, ex.Version, ExceptionReport{Situation: "At risk", Options: []ExceptionOption{{Decision: "HOLD", Impact: "Wait"}}, Recommendation: "HOLD"})
					return e
				}
			})
			require.ErrorIs(t, e, failure)
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func TestToleranceScopeBoundPaginationAndUniqueness(t *testing.T) {
	m, db, _, _ := harness(t)
	first := toleranceFixture(t, m, db, "PROJECT", 3)
	toleranceFixture(t, m, db, "REQUIREMENT", 10)
	page, e := m.listTolerancesHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 1})
	require.NoError(t, e)
	require.True(t, page.Body.Data.HasMore)
	require.Equal(t, first.ID, page.Body.Data.Items[0].ID)
	next, e := m.listTolerancesHTTP(testContext(t, 2), &ListInput{EntityID: "3", Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Len(t, next.Body.Data.Items, 1)
	require.False(t, next.Body.Data.HasMore)
	_, e = m.listTolerancesHTTP(testContext(t, 6), &ListInput{EntityID: "3", Limit: 1, Cursor: page.Body.Data.NextCursor})
	require.Error(t, e)
	q := query.BindTolerance(db, m.names)
	r, e := q.WithContext(t.Context()).Where(q.ID.Eq(first.ID)).First()
	require.NoError(t, e)
	r.ID++
	require.Error(t, q.WithContext(t.Context()).Create(r))
}
