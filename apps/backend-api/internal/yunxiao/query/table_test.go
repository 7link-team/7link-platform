package query

import (
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"reflect"
	"testing"
)

func TestAllBindingsCarryPrefixExactlyOnce(t *testing.T) {
	plain, prefixed := reflect.ValueOf(BindNames("")), reflect.ValueOf(BindNames("uat_"))
	require.Equal(t, 19, plain.NumField())
	for i := range plain.NumField() {
		a := plain.Field(i).Interface().(gormx.TableName)
		b := prefixed.Field(i).Interface().(gormx.TableName)
		require.Equal(t, "uat_"+a.String(), b.String())
		require.Equal(t, a.Logical(), b.Logical())
		require.False(t, b.IsZero())
	}
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	require.NoError(t, e)
	names := BindNames("uat_")
	actual := []string{BindIngressObservations(db, names).TableName(), BindWorklogRevisions(db, names).TableName(), BindIterations(db, names).TableName(), BindVersions(db, names).TableName(), BindMilestones(db, names).TableName(), BindWorkPlanning(db, names).TableName(), BindPlanningCarryovers(db, names).TableName(), BindIntakeChannels(db, names).TableName(), BindWorklogs(db, names).TableName(), BindWorkflowConfigs(db, names).TableName(), BindConnections(db, names).TableName(), BindProjectMappings(db, names).TableName(), BindExternalRefs(db, names).TableName(), BindStateMappings(db, names).TableName(), BindImportJobs(db, names).TableName(), BindInboundEvents(db, names).TableName(), BindSyncLogs(db, names).TableName(), BindAutomationRules(db, names).TableName(), BindAutomationRuns(db, names).TableName()}
	require.Len(t, actual, plain.NumField())
	for i, name := range actual {
		require.Equal(t, prefixed.Field(i).Interface().(gormx.TableName).String(), name)
	}
}
