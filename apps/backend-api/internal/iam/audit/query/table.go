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

// Bind applies prefix to every table owned by the audit module. It builds from
// BindNames, so the prefix reaches the generated query objects through one path
// rather than four.
func Bind(db *gorm.DB, prefix string) *Tables {
	return BindWithNames(db, BindNames(prefix))
}

// BindWithNames binds every table owned by the audit module to names prepared
// by BindNames. Bind delegates here so a repository carrying its own TableNames
// binds the generated query objects through the same single path.
func BindWithNames(db *gorm.DB, names TableNames) *Tables {
	q := Use(db)
	return &Tables{
		Events:            q.Event.Table(names.Events.String()),
		Heads:             q.Head.Table(names.Heads.String()),
		RetentionPolicies: q.RetentionPolicy.Table(names.RetentionPolicies.String()),
		ArchiveJobs:       q.ArchiveJob.Table(names.ArchiveJobs.String()),
	}
}
