package execution

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestProjectMetricsExecutionsIncludesFailedUnacceptedBranchesAndFencesWindow(t *testing.T) {
	f := executionHarness(t)
	v := f.create(t)
	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(time.Hour)
	require.NoError(t, f.db.Table(f.m.names.Executions.String()).Where("id = ?", v.ID).Update("state", "FAILED").Error)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		rows, e := f.m.metricExecutionsOn(f.human, tx, 7, 9, []guid.ID{3}, start, end)
		require.NoError(t, e)
		require.Len(t, rows, 1)
		require.Equal(t, v.ID, rows[0].ID)
		require.Equal(t, "observed-model", rows[0].Model)
		rows, e = f.m.metricExecutionsOn(f.human, tx, 7, 9, []guid.ID{4}, start, end)
		require.NoError(t, e)
		require.Empty(t, rows)
		rows, e = f.m.metricExecutionsOn(f.human, tx, 7, 9, []guid.ID{3}, end, end.Add(time.Hour))
		require.NoError(t, e)
		require.Empty(t, rows)
		_, e = f.m.metricExecutionsOn(authn.WithClaims(f.human, &authn.Claims{TenantID: 8, PrincipalID: 11}), tx, 7, 9, []guid.ID{3}, start, end)
		require.Error(t, e)
		return nil
	}))
	_, err := f.m.metricExecutionsOn(f.human, f.db, 7, 9, []guid.ID{3}, start, end)
	require.Error(t, err)
}

func TestBindMetricsRejectsUnsealedDependencyRegistry(t *testing.T) {
	f := executionHarness(t)
	require.ErrorIs(t, f.m.bindMetrics(module.NewServices()), module.ErrServicesProviding)
}
