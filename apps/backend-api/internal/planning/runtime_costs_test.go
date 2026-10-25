package planning

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRuntimeCostConnectorPreservesUnknownAndUsesSettledFacts(t *testing.T) {
	h := setup(t)
	s := module.NewServices()
	require.NoError(t, h.m.ProvideServices(s))
	require.NoError(t, s.Seal())
	connector, err := module.Resolve[RuntimeCostsConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, connector.Bind(RuntimeCosts{}), ErrInvalid)
	known := false
	var expectedAgent guid.ID
	cost := int64(0)
	problem := errors.New("settlement unavailable")
	var failure error
	require.NoError(t, connector.Bind(RuntimeCosts{PeriodOn: func(_ context.Context, _ *gorm.DB, tenant, project, agent guid.ID, ids []guid.ID, start, end time.Time) (int64, bool, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, expectedAgent, agent)
		require.Equal(t, []guid.ID{100, 101}, ids)
		require.True(t, start.Before(end))
		return cost, known, failure
	}}))
	require.ErrorIs(t, connector.Bind(RuntimeCosts{PeriodOn: h.m.runtimeCosts.PeriodOn}), ErrConflict)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	read := func(subject guid.ID) (delivery.PlanningSnapshot, error) {
		return h.m.snapshotOn(claims(t, 2), h.db, 1, 3, subject, start, end)
	}
	out, err := read(2)
	require.NoError(t, err)
	require.Nil(t, out.ExecutionCostMicros)
	require.Contains(t, out.MissingSources, "EXECUTION_COST")
	known = true
	out, err = read(2)
	require.NoError(t, err)
	require.NotNil(t, out.ExecutionCostMicros)
	require.Zero(t, *out.ExecutionCostMicros, "observed settled zero is distinct from unknown")
	require.NotContains(t, out.MissingSources, "EXECUTION_COST")
	cost = 1234567
	out, err = read(2)
	require.NoError(t, err)
	require.Equal(t, cost, *out.ExecutionCostMicros)
	// Agent attribution is requested only for a non-Human member.
	expectedAgent = 7
	// The fixture has no Agent-owned work; preserve its explicit empty scope.
	facts := *h.facts
	require.NoError(t, h.m.hydrateRuntimeCostsOn(t.Context(), h.db, 1, 3, 7, start, end, &facts))
	expectedAgent = 0
	failure = problem
	_, err = read(2)
	require.ErrorIs(t, err, problem)
	failure, cost = nil, -1
	_, err = read(2)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestReportSettledCostRequiresCompleteProjectCoverageAndRejectsOverflow(t *testing.T) {
	h := setup(t)
	h.m.facts.ProjectsOn = func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{3, 4}, nil }
	mode := "known"
	h.m.facts.ReadOn = func(_ context.Context, _ *gorm.DB, tenant, project, subject guid.ID, _, _ time.Time) (delivery.PlanningSnapshot, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(2), subject)
		cost := int64(project)
		out := delivery.PlanningSnapshot{Work: []delivery.PlanningWorkFact{}, ExecutionCostMicros: &cost}
		if project == 4 {
			switch mode {
			case "unknown":
				out.ExecutionCostMicros = nil
				out.MissingSources = []string{"EXECUTION_COST"}
			case "negative":
				cost = -1
			case "overflow":
				cost = int64(^uint64(0) >> 1)
			}
		}
		return out, nil
	}
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	read := func() (delivery.PlanningSnapshot, error) {
		return h.m.reportFactsOn(claims(t, 2), h.db, 1, &model.Report{PrincipalID: 2}, start, start.AddDate(0, 0, 7))
	}
	out, err := read()
	require.NoError(t, err)
	require.EqualValues(t, 7, *out.ExecutionCostMicros)
	mode = "unknown"
	out, err = read()
	require.NoError(t, err)
	require.Nil(t, out.ExecutionCostMicros)
	require.Contains(t, out.MissingSources, "EXECUTION_COST")
	for _, bad := range []string{"negative", "overflow"} {
		mode = bad
		_, err = read()
		require.ErrorIs(t, err, ErrUnavailable, bad)
	}
}

func TestRuntimeCostSubjectClassificationFailureCannotInventAttribution(t *testing.T) {
	h := setup(t)
	calls := 0
	h.m.runtimeCosts = RuntimeCosts{PeriodOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, []guid.ID, time.Time, time.Time) (int64, bool, error) {
		calls++
		return 0, true, nil
	}}
	start := h.m.now()
	end := start.Add(time.Hour)
	out := delivery.PlanningSnapshot{}
	h.m.principals.ActiveHumanByID = nil
	require.ErrorIs(t, h.m.hydrateRuntimeCostsOn(t.Context(), h.db, 1, 3, 2, start, end, &out), ErrUnavailable)
	problem := errors.New("classification unavailable")
	h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, problem
	}
	require.ErrorIs(t, h.m.hydrateRuntimeCostsOn(t.Context(), h.db, 1, 3, 2, start, end, &out), problem)
	require.Zero(t, calls, "an unresolved Human/Agent identity cannot be charged to a guessed owner")
	require.Nil(t, out.ExecutionCostMicros)
}
