package clientwork

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestToleranceImpactHTTPIsReadOnlyAndReturnsBeforeAfterUncertainty(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	input := &ToleranceImpactInput{EntityID: "3", RequirementID: "10", Body: ApprovedChangeDelta{ScheduleDeltaDays: 4, CostDeltaMicros: 30}}
	empty, err := m.toleranceImpactHTTP(ctx, input)
	require.NoError(t, err)
	require.Empty(t, empty.Body.Data.Before)
	require.Empty(t, empty.Body.Data.Subjects)
	require.Empty(t, empty.Body.Data.Breached)
	toleranceFixture(t, m, db, "REQUIREMENT", 10)
	toleranceFixture(t, m, db, "PROJECT", 3)
	before := collaborationSnapshot(t, m, db)
	result, err := m.toleranceImpactHTTP(ctx, input)
	require.NoError(t, err)
	snapshot := result.Body.Data
	require.True(t, snapshot.LowConfidence)
	require.Len(t, snapshot.Before, 2)
	require.Len(t, snapshot.Subjects, 2)
	require.Len(t, snapshot.Breached, 4)
	for i, previous := range snapshot.Before {
		after := snapshot.Subjects[i]
		require.Equal(t, previous.ID, after.ID)
		require.Equal(t, previous.BaselineNo, after.BaselineNo)
		require.Equal(t, previous.Version, after.Version)
		require.Equal(t, previous.ToleranceBudget, after.ToleranceBudget)
		require.WithinDuration(t, previous.AppetiteEndAt.AddDate(0, 0, 4), after.Forecast.EndAt, time.Millisecond)
		require.EqualValues(t, 130, *after.Forecast.CostMicros)
		require.Equal(t, "EXCEPTION", after.State)
		require.Nil(t, after.OpenExceptionID)
	}
	require.Equal(t, before, collaborationSnapshot(t, m, db))
	input.Body = ApprovedChangeDelta{}
	result, err = m.toleranceImpactHTTP(ctx, input)
	require.NoError(t, err)
	require.Empty(t, result.Body.Data.Breached)
	require.Equal(t, before, collaborationSnapshot(t, m, db))
}

func TestToleranceImpactHTTPRejectsInvalidDeniedAndFailedReads(t *testing.T) {
	m, db, projects, _ := harness(t)
	ctx := testContext(t, 2)
	for _, in := range []*ToleranceImpactInput{{EntityID: "bad", RequirementID: "10"}, {EntityID: "3", RequirementID: "bad"}, {EntityID: "3", RequirementID: "999"}, {EntityID: "3", RequirementID: "10", Body: ApprovedChangeDelta{ScheduleDeltaDays: 36501}}} {
		_, err := m.toleranceImpactHTTP(ctx, in)
		require.Error(t, err)
	}
	in := &ToleranceImpactInput{EntityID: "3", RequirementID: "10"}
	for _, actor := range []guid.ID{4, 7, 9} {
		_, err := m.toleranceImpactHTTP(testContext(t, actor), in)
		require.Error(t, err)
	}
	projects.deny = true
	_, err := m.toleranceImpactHTTP(ctx, in)
	require.Error(t, err)
	projects.deny = false
	_, err = (&Module{}).toleranceImpactHTTP(ctx, in)
	require.Error(t, err)
	failed := false
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("impact_read_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.Tolerance.String() {
			failed = true
			_ = tx.AddError(errors.New("database unavailable"))
		}
	}))
	_, err = m.toleranceImpactHTTP(ctx, in)
	require.Error(t, err)
	require.True(t, failed)
}

func TestToleranceImpactUsesMeasuredForecastAndFlagsMissingDimensions(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	r := toleranceFixture(t, m, db, "REQUIREMENT", 10)
	cost, scope := int64(90), int64(9)
	forecast := ToleranceForecast{EndAt: now().Add(9 * 24 * time.Hour), CostMicros: &cost, ScopeCriteria: &scope}
	var receipt Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		receipt, e = m.forecastToleranceOn(ctx, tx, 3, r.ID, r.Version, forecast)
		return e
	}))
	in := &ToleranceImpactInput{EntityID: "3", RequirementID: "10", Body: ApprovedChangeDelta{ScheduleDeltaDays: 1, CostDeltaMicros: 10}}
	before := collaborationSnapshot(t, m, db)
	result, err := m.toleranceImpactHTTP(ctx, in)
	require.NoError(t, err)
	require.False(t, result.Body.Data.LowConfidence)
	require.Empty(t, result.Body.Data.Breached)
	require.Equal(t, forecast, result.Body.Data.Before[0].Forecast)
	require.EqualValues(t, 100, *result.Body.Data.Subjects[0].Forecast.CostMicros)
	require.Equal(t, before, collaborationSnapshot(t, m, db))
	forecast.ScopeCriteria = nil
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.forecastToleranceOn(ctx, tx, 3, r.ID, receipt.Version, forecast)
		return e
	}))
	result, err = m.toleranceImpactHTTP(ctx, in)
	require.NoError(t, err)
	require.True(t, result.Body.Data.LowConfidence)
}

func TestToleranceHTTPLifecyclePreservesReplayAndFrozenExceptionReport(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	budget := ToleranceBudget{AppetiteEndAt: now().Add(10 * 24 * time.Hour), ToleranceTimePct: 20, ToleranceCostPct: 15, ToleranceScopePct: 10}
	create := &CreateInput[ToleranceBody]{EntityID: "3", Key: "tolerance-create", Body: ToleranceBody{ToleranceBudget: budget, SubjectKind: "PROJECT", SubjectID: 3}}
	created, err := m.createToleranceHTTP(ctx, create)
	require.NoError(t, err)
	replay, err := m.createToleranceHTTP(ctx, create)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, created.Body.Data, replay.Body.Data)
	id := created.Body.Data.ID.String()
	get := &ObjectInput{EntityID: "3", ID: id}
	view, err := m.getToleranceHTTP(ctx, get)
	require.NoError(t, err)
	require.Equal(t, budget, view.Body.Data.ToleranceBudget)
	require.Equal(t, 1, view.Body.Data.Version)
	edit := &EditInput[ToleranceBody]{EntityID: "3", ID: id, Key: "tolerance-edit", IfMatch: "\"1\"", Body: create.Body}
	edit.Body.ToleranceTimePct = 25
	updated, err := m.editToleranceHTTP(ctx, edit)
	require.NoError(t, err)
	require.Equal(t, 2, updated.Body.Data.Version)
	replay, err = m.editToleranceHTTP(ctx, edit)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	forecast := &EditInput[ToleranceForecast]{EntityID: "3", ID: id, Key: "tolerance-forecast", IfMatch: "\"2\"", Body: ToleranceForecast{EndAt: now().Add(20 * 24 * time.Hour)}}
	raised, err := m.forecastToleranceHTTP(ctx, forecast)
	require.NoError(t, err)
	require.Equal(t, "EXCEPTION", raised.Body.Data.State)
	view, err = m.getToleranceHTTP(ctx, get)
	require.NoError(t, err)
	require.NotNil(t, view.Body.Data.OpenExceptionID)
	exceptionID := view.Body.Data.OpenExceptionID.String()
	report := &EditInput[ExceptionReport]{EntityID: "3", ID: exceptionID, Key: "exception-report", IfMatch: "\"1\"", Body: ExceptionReport{Situation: "Actual forecast exceeds the commitment", Options: []ExceptionOption{{Decision: "HOLD", Impact: "Wait for confirmed input"}}, Recommendation: "HOLD"}}
	reported, err := m.reportExceptionHTTP(ctx, report)
	require.NoError(t, err)
	require.Equal(t, "REPORTED", reported.Body.Data.State)
	replay, err = m.reportExceptionHTTP(ctx, report)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	exception, err := m.getExceptionHTTP(ctx, &ObjectInput{EntityID: "3", ID: exceptionID})
	require.NoError(t, err)
	require.Equal(t, report.Body, *exception.Body.Data.Report)
	decision := &ExceptionDecisionInput{EditInput: EditInput[ExceptionDecisionBody]{EntityID: "3", ID: exceptionID, Key: "exception-decide", IfMatch: "\"2\"", Body: ExceptionDecisionBody{Decision: "HOLD", Reason: "Human confirmed the pause", ResumeCondition: "Confirmed input received", ReauthProof: "secret-proof"}}, Cookie: "cookie"}
	decided, err := m.decideExceptionHTTP(ctx, decision)
	require.NoError(t, err)
	require.Equal(t, "DECIDED", decided.Body.Data.State)
	replay, err = m.decideExceptionHTTP(ctx, decision)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	exception, err = m.getExceptionHTTP(ctx, &ObjectInput{EntityID: "3", ID: exceptionID})
	require.NoError(t, err)
	require.Equal(t, "HOLD", *exception.Body.Data.Decision)
	require.NotContains(t, string(exception.Body.Data.DecisionPayload), "secret-proof")
	require.NotContains(t, string(exception.Body.Data.DecisionPayload), "cookie")
}

func TestToleranceHTTPRejectsInvalidScopeVersionsAndUnauthorizedReads(t *testing.T) {
	m, _, _, _ := harness(t)
	ctx := testContext(t, 2)
	calls := []func() (any, error){
		func() (any, error) { return m.createToleranceHTTP(ctx, &CreateInput[ToleranceBody]{EntityID: "bad"}) },
		func() (any, error) {
			return m.editToleranceHTTP(ctx, &EditInput[ToleranceBody]{EntityID: "3", ID: "1", IfMatch: "bad"})
		},
		func() (any, error) {
			return m.forecastToleranceHTTP(ctx, &EditInput[ToleranceForecast]{EntityID: "bad"})
		},
		func() (any, error) { return m.reportExceptionHTTP(ctx, &EditInput[ExceptionReport]{EntityID: "bad"}) },
		func() (any, error) { return m.decideExceptionHTTP(ctx, &ExceptionDecisionInput{}) },
	}
	for _, call := range calls {
		_, err := call()
		require.Error(t, err)
	}
	for _, in := range []*ObjectInput{{EntityID: "bad", ID: "1"}, {EntityID: "3", ID: "bad"}, {EntityID: "3", ID: "999"}} {
		_, err := m.getToleranceHTTP(ctx, in)
		require.Error(t, err)
		_, err = m.getExceptionHTTP(ctx, in)
		require.Error(t, err)
	}
	_, err := m.getToleranceHTTP(testContext(t, 9), &ObjectInput{EntityID: "3", ID: "1"})
	require.Error(t, err)
	unavailable := &Module{}
	for _, read := range []func(context.Context, *ObjectInput) (any, error){
		func(ctx context.Context, in *ObjectInput) (any, error) { return unavailable.getToleranceHTTP(ctx, in) },
		func(ctx context.Context, in *ObjectInput) (any, error) { return unavailable.getExceptionHTTP(ctx, in) },
	} {
		_, err := read(ctx, &ObjectInput{EntityID: "3", ID: "1"})
		require.Error(t, err)
	}
}
