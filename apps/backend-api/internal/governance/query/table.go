package query

import (
	"7link.com/platform/internal/governance/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

type TableNames struct {
	InboxAcknowledgements  gormx.TableName
	ProjectEscalationRules gormx.TableName
	SpawnRequests          gormx.TableName
	AutomatedDecisions     gormx.TableName
	QualityEscapes         gormx.TableName
	SpawnGrants            gormx.TableName
	SpawnGrantMembers      gormx.TableName
	SpawnCostCursors       gormx.TableName
	ClaimEvaluations       gormx.TableName
	Delegations            gormx.TableName
	WorkflowDecisions      gormx.TableName
	RoleSeeds              gormx.TableName
	Settings               gormx.TableName
	EscalationRules        gormx.TableName
	EscalationIncidents    gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{InboxAcknowledgements: gormx.NewTableName(prefix, model.InboxAcknowledgement{}.TableName()), ProjectEscalationRules: gormx.NewTableName(prefix, model.ProjectEscalationRule{}.TableName()), SpawnRequests: gormx.NewTableName(prefix, model.SpawnRequest{}.TableName()), AutomatedDecisions: gormx.NewTableName(prefix, model.AutomatedDecision{}.TableName()), QualityEscapes: gormx.NewTableName(prefix, model.QualityEscape{}.TableName()), SpawnGrants: gormx.NewTableName(prefix, model.SpawnGrant{}.TableName()), SpawnGrantMembers: gormx.NewTableName(prefix, model.SpawnGrantMember{}.TableName()), SpawnCostCursors: gormx.NewTableName(prefix, model.SpawnCostCursor{}.TableName()), ClaimEvaluations: gormx.NewTableName(prefix, model.ClaimEvaluation{}.TableName()), Delegations: gormx.NewTableName(prefix, model.Delegation{}.TableName()), WorkflowDecisions: gormx.NewTableName(prefix, model.WorkflowDecision{}.TableName()), RoleSeeds: gormx.NewTableName(prefix, model.RoleSeed{}.TableName()), Settings: gormx.NewTableName(prefix, model.Settings{}.TableName()), EscalationRules: gormx.NewTableName(prefix, model.EscalationRule{}.TableName()), EscalationIncidents: gormx.NewTableName(prefix, model.EscalationIncident{}.TableName())}
}

func BindSettings(db *gorm.DB, names TableNames) *settings {
	return Use(db).Settings.Table(names.Settings.String())
}
func BindEscalationRules(db *gorm.DB, names TableNames) *escalationRule {
	return Use(db).EscalationRule.Table(names.EscalationRules.String())
}
func BindEscalationIncidents(db *gorm.DB, names TableNames) *escalationIncident {
	return Use(db).EscalationIncident.Table(names.EscalationIncidents.String())
}

func BindRoleSeeds(db *gorm.DB, names TableNames) *roleSeed {
	return Use(db).RoleSeed.Table(names.RoleSeeds.String())
}

func BindWorkflowDecisions(db *gorm.DB, names TableNames) *workflowDecision {
	return Use(db).WorkflowDecision.Table(names.WorkflowDecisions.String())
}

func BindDelegations(db *gorm.DB, names TableNames) *delegation {
	return Use(db).Delegation.Table(names.Delegations.String())
}

func BindClaimEvaluations(db *gorm.DB, names TableNames) *claimEvaluation {
	return Use(db).ClaimEvaluation.Table(names.ClaimEvaluations.String())
}

func BindSpawnGrants(db *gorm.DB, names TableNames) *spawnGrant {
	return Use(db).SpawnGrant.Table(names.SpawnGrants.String())
}
func BindSpawnGrantMembers(db *gorm.DB, names TableNames) *spawnGrantMember {
	return Use(db).SpawnGrantMember.Table(names.SpawnGrantMembers.String())
}
func BindSpawnCostCursors(db *gorm.DB, names TableNames) *spawnCostCursor {
	return Use(db).SpawnCostCursor.Table(names.SpawnCostCursors.String())
}

func BindQualityEscapes(db *gorm.DB, names TableNames) *qualityEscape {
	return Use(db).QualityEscape.Table(names.QualityEscapes.String())
}

func BindAutomatedDecisions(db *gorm.DB, names TableNames) *automatedDecision {
	return Use(db).AutomatedDecision.Table(names.AutomatedDecisions.String())
}

func BindSpawnRequests(db *gorm.DB, n TableNames) *spawnRequest {
	return Use(db).SpawnRequest.Table(n.SpawnRequests.String())
}

func BindProjectEscalationRules(db *gorm.DB, names TableNames) *projectEscalationRule {
	return Use(db).ProjectEscalationRule.Table(names.ProjectEscalationRules.String())
}

func BindInboxAcknowledgements(db *gorm.DB, names TableNames) *inboxAcknowledgement {
	return Use(db).InboxAcknowledgement.Table(names.InboxAcknowledgements.String())
}
