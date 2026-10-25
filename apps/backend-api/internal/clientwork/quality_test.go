package clientwork

import (
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTestPlanFreezesCasesAndRecordsOnlyLinkedManualResults(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	var c, plan Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		c, e = m.writeCaseOn(ctx, tx, 3, 0, 0, TestCaseBody{Title: "Login", Steps: "Enter valid credentials", Expected: "Dashboard", WorkItemID: 11})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		plan, e = m.createPlanOn(ctx, tx, 3, TestPlanBody{Title: "Regression", CaseIDs: []guid.ID{c.ID}})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.writeCaseOn(ctx, tx, 3, c.ID, c.Version, TestCaseBody{Title: "Changed", Steps: "Different steps", Expected: "Different result", WorkItemID: 11})
		return e
	}))
	view, e := m.get_test_planHTTP(ctx, &ObjectInput{EntityID: "3", ID: plan.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "Enter valid credentials", view.Body.Data.Cases[0].Steps)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.recordRunOn(ctx, tx, 3, plan.ID, 1, TestRunBody{CaseID: c.ID, CaseVersion: 2, Result: "PASSED", Evidence: "Manual observation"})
		return e
	}), ErrConflict)
	var run Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, e = m.recordRunOn(ctx, tx, 3, plan.ID, 1, TestRunBody{CaseID: c.ID, CaseVersion: 1, Result: "FAILED", Evidence: "Manual failure observation"})
		return e
	}))
	result, e := m.get_test_runHTTP(ctx, &ObjectInput{EntityID: "3", ID: run.ID.String()})
	require.NoError(t, e)
	require.Equal(t, "HUMAN_RECORDED", result.Body.Data.Source)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.writeCaseOn(testContext(t, 4), tx, 3, 0, 0, TestCaseBody{Title: "Attempt", Steps: "Attempt", Expected: "Attempt", WorkItemID: 11})
		return e
	}))
	bad := guid.ID(99)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.writeCaseOn(ctx, tx, 3, 0, 0, TestCaseBody{Title: "Attempt", Steps: "Attempt", Expected: "Attempt", WorkItemID: 11, DefectID: &bad})
		return e
	}), ErrNotFound)
}
