package delivery

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProjectBudgetHTTPRejectsInvalidBoundary(t *testing.T) {
	m, _, ctx := constitutionHarness(t)
	for _, in := range []*projectBudgetInput{{EntityID: "0", IfMatch: `"1"`}, {EntityID: "99", IfMatch: "invalid"}} {
		_, err := m.changeProjectBudgetHTTP(ctx, in)
		require.Error(t, err)
	}
	for _, in := range []*stageListInput{{EntityID: "0", Limit: 10}, {EntityID: "99", Limit: 201}, {EntityID: "99", Offset: -1, Limit: 10}} {
		_, err := m.projectBudgetEventsHTTP(ctx, in)
		require.Error(t, err)
	}
}

func TestProjectBudgetHistoryStorageFailuresRemainExplicit(t *testing.T) {
	m, db, ctx := projectModeHarness(t)
	require.NoError(t, db.Exec("ALTER TABLE "+m.service.names.BudgetEvents.String()+" RENAME TO unavailable_budget_history").Error)
	out, e := m.projectBudgetEventsHTTP(ctx, &stageListInput{EntityID: "99", Limit: 20})
	require.Nil(t, out)
	assertWorkStatus(t, e, 500)
	m.runtime = nil
	_, e = m.projectBudgetEventsHTTP(ctx, &stageListInput{EntityID: "99", Limit: 20})
	assertWorkStatus(t, e, 503)
}
