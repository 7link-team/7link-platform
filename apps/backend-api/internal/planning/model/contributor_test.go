package model

import (
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
	"sync"
	"testing"
)

func TestContributorIdentityAndGeneratedStorageFields(t *testing.T) {
	s, e := schema.Parse(&Contributor{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, e)
	require.Equal(t, "planning_goal_contributors", s.Table)
	require.Equal(t, []string{"tenant_id", "id"}, s.PrimaryFieldDBNames)
	for _, field := range []string{"goal_id", "kr", "principal_id", "created_by", "created_at"} {
		require.Contains(t, s.FieldsByDBName, field)
		require.True(t, s.FieldsByDBName[field].NotNull)
	}
}
