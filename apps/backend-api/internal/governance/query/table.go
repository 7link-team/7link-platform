package query

import (
	"7link.com/platform/internal/governance/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct{ RoleSeeds gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{RoleSeeds: gormx.NewTableName(prefix, model.RoleSeed{}.TableName())}
}

func BindRoleSeeds(db *gorm.DB, names TableNames) *roleSeed {
	return Use(db).RoleSeed.Table(names.RoleSeeds.String())
}
