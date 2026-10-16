package query

import (
	"7link.com/platform/internal/fleet/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// TableNames resolves fleet's own logical names for one deployment.
type TableNames struct{ Agents, AgentRoles gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Agents: gormx.NewTableName(prefix, model.Agent{}.TableName()), AgentRoles: gormx.NewTableName(prefix, model.AgentRole{}.TableName())}
}
func BindAgents(db *gorm.DB, names TableNames) *agent {
	return Use(db).Agent.Table(names.Agents.String())
}
func BindAgentRoles(db *gorm.DB, names TableNames) *agentRole {
	return Use(db).AgentRole.Table(names.AgentRoles.String())
}
