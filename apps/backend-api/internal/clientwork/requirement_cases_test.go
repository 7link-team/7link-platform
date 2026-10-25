package clientwork

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRequirementCaseProviderReturnsActualScopedCaseVersions(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	var c Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		c, err = m.writeCaseOn(ctx, tx, 3, 0, 0, TestCaseBody{Title: "Login", Steps: "Enter credentials", Expected: "Dashboard", WorkItemID: 11})
		return err
	}))
	var snapshot []delivery.RequirementCaseSnapshot
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = m.requirementCasesOn(ctx, tx, 1, 3, []guid.ID{11})
		return err
	}))
	require.Len(t, snapshot, 1)
	require.Equal(t, c.ID, snapshot[0].ID)
	require.Equal(t, 1, snapshot[0].Version)
	require.Equal(t, "Enter credentials", snapshot[0].Steps)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.requirementCasesOn(ctx, tx, 1, 4, []guid.ID{11})
		require.Empty(t, rows)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.requirementCasesOn(ctx, tx, 2, 3, []guid.ID{11})
		require.Empty(t, rows)
		return err
	}))
}
