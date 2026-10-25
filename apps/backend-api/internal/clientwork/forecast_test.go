package clientwork

import (
	"context"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMeasuredForecastEvenMedianRoundsOnlyAfterExactArithmetic(t *testing.T) {
	values := []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64 - 1, 0, 0}
	before := append([]int64(nil), values...)
	median := medianForecast(values)
	require.Equal(t, before, values, "measurement must not reorder provider facts")
	want := new(big.Rat).SetFrac(new(big.Int).Sub(new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(2)), big.NewInt(1)), big.NewInt(2))
	require.Zero(t, median.Cmp(want), "even median cannot overflow int64 or lose the half unit")
	got, ok := ceilForecast(median)
	require.True(t, ok)
	require.EqualValues(t, math.MaxInt64, got)
	_, ok = ceilForecast(big.NewRat(-1, 2))
	require.False(t, ok, "negative observations cannot become forecasts")
	tooLarge := new(big.Rat).Add(new(big.Rat).SetInt64(math.MaxInt64), big.NewRat(1, 2))
	_, ok = ceilForecast(tooLarge)
	require.False(t, ok, "rounding beyond int64 must remain unavailable")
}

func TestMeasuredForecastUsesMediansSevenDayParallelismAndExactCosts(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	spent := int64(100)
	work := delivery.ForecastWorkMeasurement{Remaining: 3, ScopeCriteria: 9, SpentMicros: &spent}
	for i, v := range []int64{100, 1000, 1000, 1000, 900000} {
		work.Samples = append(work.Samples, delivery.ForecastWorkSample{WorkItemID: guid.ID(i + 1), CycleMillis: v})
	}
	start := at.Add(-8 * 24 * time.Hour)
	runs := []execution.ForecastExecution{{ID: 1, StartedAt: &start}, {ID: 2, StartedAt: &start}}
	end, cost, scope, meta := measureForecast(at, work, runs, []int64{1, 20, 20, 20, 9000})
	require.Equal(t, at.Add(1500*time.Millisecond), *end)
	require.EqualValues(t, 160, *cost)
	require.EqualValues(t, 9, *scope)
	require.InDelta(t, 2, meta.EffectiveParallelism, 0)
	require.False(t, meta.LowConfidence)
	// A terminal execution without a terminal receipt cannot assert 7 active days.
	runs[1].Terminal = true
	end, _, _, meta = measureForecast(at, work, runs, []int64{1, 20, 20, 20, 9000})
	require.Equal(t, at.Add(3*time.Second), *end)
	require.InDelta(t, 1, meta.EffectiveParallelism, 0)
}

func TestMeasuredForecastSparseSamplesAndOverflowNeverBecomeInventedDefaults(t *testing.T) {
	at := now()
	spent := int64(100)
	work := delivery.ForecastWorkMeasurement{Remaining: 2, ScopeCriteria: 4, SpentMicros: &spent}
	for i := 0; i < 4; i++ {
		work.Samples = append(work.Samples, delivery.ForecastWorkSample{CycleMillis: 1000})
	}
	end, cost, scope, meta := measureForecast(at, work, nil, []int64{1, 2, 3, 4})
	require.Nil(t, end)
	require.Nil(t, cost)
	require.EqualValues(t, 4, *scope)
	require.True(t, meta.LowConfidence)
	require.ElementsMatch(t, []string{"TIME", "COST"}, meta.UnavailableDimensions)
	work.Samples = append(work.Samples, delivery.ForecastWorkSample{CycleMillis: 1000})
	spent = math.MaxInt64
	end, cost, _, meta = measureForecast(at, work, nil, []int64{1, 2, 3, 4, 5})
	require.NotNil(t, end)
	require.Nil(t, cost)
	require.Equal(t, []string{"COST"}, meta.UnavailableDimensions)
	work.Remaining = 0
	end, cost, _, _ = measureForecast(at, work, nil, nil)
	require.Equal(t, at, *end)
	require.EqualValues(t, math.MaxInt64, *cost)
}

func TestMeasuredForecastPersistsFactsRaisesOneExceptionAndDebounces(t *testing.T) {
	m, db, _, _ := harness(t)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	at := now().Add(time.Hour)
	spent := int64(200)
	m.forecastWork.ReadOn = func(_ context.Context, tx *gorm.DB, tenant, project guid.ID, requirement *guid.ID, _ time.Time) (delivery.ForecastWorkMeasurement, error) {
		require.NotNil(t, tx)
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 3, project)
		require.Nil(t, requirement)
		work := delivery.ForecastWorkMeasurement{Remaining: 1, SpentMicros: &spent, ScopeCriteria: 12}
		for i := 1; i <= 5; i++ {
			work.Samples = append(work.Samples, delivery.ForecastWorkSample{WorkItemID: guid.ID(i), CycleMillis: 1000})
			work.AcceptedWorkIDs = append(work.AcceptedWorkIDs, guid.ID(i))
		}
		return work, nil
	}
	m.forecastExecutions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]execution.ForecastExecution, error) {
		var out []execution.ForecastExecution
		for i := 1; i <= 5; i++ {
			out = append(out, execution.ForecastExecution{ID: guid.ID(i), WorkItemID: guid.ID(i)})
		}
		return out, nil
	}
	m.forecastCosts.ExecutionsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error) {
		return 20, true, nil
	}
	m.audit = func(_ context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		require.EqualValues(t, 1, event.TenantID)
		require.Equal(t, "system:tolerance_forecast", event.Detail["authority_source"])
		return tx.Exec("INSERT INTO audit_probe VALUES (?)", event.EventType).Error
	}
	m.events = expiryPublisher{publish: func(_ context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
		require.Equal(t, "SYSTEM", event.Actor.Kind)
		require.EqualValues(t, 1, event.TenantID)
		return 1, nil
	}}
	require.NoError(t, m.forecastDue(t.Context(), 1, 3, at))
	q := query.BindTolerance(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(tol.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "EXCEPTION", row.State)
	require.NotNil(t, row.OpenExceptionID)
	require.NotNil(t, row.ForecastMeasurement)
	require.Contains(t, *row.ForecastMeasurement, "MEASURED_30D")
	require.NotContains(t, *row.ForecastMeasurement, strings.Repeat("?", 3))
	version := row.Version
	exception := *row.OpenExceptionID
	require.NoError(t, m.forecastDue(t.Context(), 1, 3, at.Add(30*time.Second)))
	row, e = q.WithContext(t.Context()).Where(q.ID.Eq(tol.ID)).First()
	require.NoError(t, e)
	require.Equal(t, version, row.Version)
	require.NoError(t, m.forecastDue(t.Context(), 1, 3, at.Add(2*time.Minute)))
	row, e = q.WithContext(t.Context()).Where(q.ID.Eq(tol.ID)).First()
	require.NoError(t, e)
	require.Equal(t, exception, *row.OpenExceptionID)
}

func TestMeasuredForecastCostUsesAcceptedItemsWithoutCycleStartHistory(t *testing.T) {
	m, db, _, _ := harness(t)
	tol := toleranceFixture(t, m, db, "PROJECT", 3)
	at := now().Add(time.Hour)
	spent := int64(20)
	m.forecastWork.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, *guid.ID, time.Time) (delivery.ForecastWorkMeasurement, error) {
		return delivery.ForecastWorkMeasurement{Remaining: 3, ScopeCriteria: 4, SpentMicros: &spent, AcceptedWorkIDs: []guid.ID{1, 2, 3, 4, 5}}, nil
	}
	m.forecastExecutions.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) ([]execution.ForecastExecution, error) {
		var out []execution.ForecastExecution
		for i := 1; i <= 5; i++ {
			out = append(out, execution.ForecastExecution{ID: guid.ID(100 + i), WorkItemID: guid.ID(i)})
		}
		return out, nil
	}
	var measured []guid.ID
	m.forecastCosts.ExecutionsOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, ids []guid.ID) (int64, bool, error) {
		measured = append(measured, ids...)
		return 10, true, nil
	}
	require.NoError(t, m.forecastDue(t.Context(), 1, 3, at))
	q := query.BindTolerance(db, m.names)
	row, e := q.WithContext(t.Context()).Where(q.ID.Eq(tol.ID)).First()
	require.NoError(t, e)
	require.Nil(t, row.ForecastEndAt, "unknown cycle history must remain unavailable")
	require.NotNil(t, row.ForecastCostMicros)
	require.EqualValues(t, 50, *row.ForecastCostMicros)
	require.ElementsMatch(t, []guid.ID{101, 102, 103, 104, 105}, measured)
	meta := toleranceRecord(row).Measurement
	require.NotNil(t, meta)
	require.Zero(t, meta.CycleSamples)
	require.Equal(t, 5, meta.CostSamples)
	require.Equal(t, []string{"TIME"}, meta.UnavailableDimensions)
}
