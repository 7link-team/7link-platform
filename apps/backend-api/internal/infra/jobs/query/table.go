package query

import (
	"7link.com/platform/internal/infra/jobs/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ Jobs, Schedules gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Jobs: gormx.NewTableName(prefix, model.Job{}.TableName()), Schedules: gormx.NewTableName(prefix, model.Schedule{}.TableName())}
}
func BindJobs(db *gorm.DB, names TableNames) *job { return Use(db).Job.Table(names.Jobs.String()) }
func BindSchedules(db *gorm.DB, names TableNames) *schedule {
	return Use(db).Schedule.Table(names.Schedules.String())
}
