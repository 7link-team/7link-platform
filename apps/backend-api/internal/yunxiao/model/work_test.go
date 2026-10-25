package model

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

func TestEffortModelsPreserveMissingConfirmationAndOriginalEventTime(t *testing.T) {
	work, err := schema.Parse(&Worklog{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	require.Equal(t, "delivery_worklogs", work.Table)
	for _, column := range []string{"timezone", "confirmed_by", "confirmed_at", "approved_by", "approved_at"} {
		field := work.LookUpField(column)
		require.NotNil(t, field, column)
		require.False(t, field.NotNull, column)
		require.EqualValues(t, 0, field.AutoCreateTime, column)
		require.EqualValues(t, 0, field.AutoUpdateTime, column)
	}
	revision, err := schema.Parse(&WorklogRevision{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	require.Equal(t, "delivery_worklog_revisions", revision.Table)
	require.EqualValues(t, 0, revision.LookUpField("recorded_at").AutoCreateTime)
	require.False(t, revision.LookUpField("actor_id").NotNull)
}
