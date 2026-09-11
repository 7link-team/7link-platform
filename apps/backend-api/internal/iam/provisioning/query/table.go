package query

import (
	"7link.com/platform/internal/iam/provisioning/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type (
	Directories     = directory
	Credentials     = credential
	Resources       = resource
	Targets         = target
	TargetResources = targetResource
)

type Tables struct {
	Directories     *Directories
	Credentials     *Credentials
	Resources       *Resources
	Targets         *Targets
	TargetResources *TargetResources
}

// TableNames is the physical name of every table provisioning owns under one
// deployment prefix.
type TableNames struct {
	Directories     gormx.TableName
	Credentials     gormx.TableName
	Resources       gormx.TableName
	Targets         gormx.TableName
	TargetResources gormx.TableName
}

// BindNames returns the physical name of every table provisioning owns. It is
// the only place the deployment prefix is applied to one of them, so a
// deployment cannot end up with half of provisioning's tables prefixed.
func BindNames(prefix string) TableNames {
	return TableNames{
		Directories:     gormx.NewTableName(prefix, model.Directory{}.TableName()),
		Credentials:     gormx.NewTableName(prefix, model.Credential{}.TableName()),
		Resources:       gormx.NewTableName(prefix, model.Resource{}.TableName()),
		Targets:         gormx.NewTableName(prefix, model.Target{}.TableName()),
		TargetResources: gormx.NewTableName(prefix, model.TargetResource{}.TableName()),
	}
}

// Map indexes the names by logical name so the module root can publish them as
// a set to the modules that JOIN provisioning's tables.
func (names TableNames) Map() map[string]gormx.TableName {
	return map[string]gormx.TableName{
		model.Directory{}.TableName():      names.Directories,
		model.Credential{}.TableName():     names.Credentials,
		model.Resource{}.TableName():       names.Resources,
		model.Target{}.TableName():         names.Targets,
		model.TargetResource{}.TableName(): names.TargetResources,
	}
}

// Bind applies prefix to every table owned by the provisioning module. It
// builds from BindNames, so the prefix reaches the generated query objects
// through one path rather than five.
func Bind(db *gorm.DB, prefix string) *Tables {
	names := BindNames(prefix)
	q := Use(db)
	return &Tables{
		Directories:     q.Directory.Table(names.Directories.String()),
		Credentials:     q.Credential.Table(names.Credentials.String()),
		Resources:       q.Resource.Table(names.Resources.String()),
		Targets:         q.Target.Table(names.Targets.String()),
		TargetResources: q.TargetResource.Table(names.TargetResources.String()),
	}
}
