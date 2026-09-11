package query

import (
	"7link.com/platform/internal/iam/audit/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type (
	Events            = event
	Heads             = head
	RetentionPolicies = retentionPolicy
	ArchiveJobs       = archiveJob
)

type Tables struct {
	Events            *Events
	Heads             *Heads
	RetentionPolicies *RetentionPolicies
	ArchiveJobs       *ArchiveJobs
}

// TableNames is the physical name of every table audit owns under one
// deployment prefix.
type TableNames struct {
	Events            gormx.TableName
	Heads             gormx.TableName
	RetentionPolicies gormx.TableName
	ArchiveJobs       gormx.TableName
}

// BindNames returns the physical name of every table audit owns. It is the only
// place the deployment prefix is applied to one of them, so a deployment
// cannot end up with half of audit's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Events:            gormx.NewTableName(prefix, model.Event{}.TableName()),
		Heads:             gormx.NewTableName(prefix, model.Head{}.TableName()),
		RetentionPolicies: gormx.NewTableName(prefix, model.RetentionPolicy{}.TableName()),
		ArchiveJobs:       gormx.NewTableName(prefix, model.ArchiveJob{}.TableName()),
	}
}

// Map indexes the names by logical name so the module root can publish them as
// a set to the modules that JOIN audit's tables.
func (names TableNames) Map() map[string]gormx.TableName {
	return map[string]gormx.TableName{
		model.Event{}.TableName():           names.Events,
		model.Head{}.TableName():            names.Heads,
		model.RetentionPolicy{}.TableName(): names.RetentionPolicies,
		model.ArchiveJob{}.TableName():      names.ArchiveJobs,
	}
}

// Bind applies prefix to every table owned by the audit module. It builds from
// BindNames, so the prefix reaches the generated query objects through one path
// rather than four.
func Bind(db *gorm.DB, prefix string) *Tables {
	names := BindNames(prefix)
	q := Use(db)
	return &Tables{
		Events:            q.Event.Table(names.Events.String()),
		Heads:             q.Head.Table(names.Heads.String()),
		RetentionPolicies: q.RetentionPolicy.Table(names.RetentionPolicies.String()),
		ArchiveJobs:       q.ArchiveJob.Table(names.ArchiveJobs.String()),
	}
}
