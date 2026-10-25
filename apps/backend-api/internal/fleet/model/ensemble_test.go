package model

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEnsembleModelsKeepIndependentOwnerTables(t *testing.T) {
	require.Equal(t, "fleet_ensemble_plans", (EnsemblePlan{}).TableName())
	require.Equal(t, "fleet_ensemble_branches", (EnsembleBranch{}).TableName())
}
