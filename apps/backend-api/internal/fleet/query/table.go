package query

import (
	"7link.com/platform/internal/fleet/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// TableNames resolves fleet's own logical names for one deployment.
type TableNames struct {
	ManualModels                                                 gormx.TableName
	TerminalSessions, TerminalAccess                             gormx.TableName
	AgentMachineCommands                                         gormx.TableName
	EnsemblePlans, EnsembleBranches                              gormx.TableName
	ResetCommands                                                gormx.TableName
	GatewayConfigs, GatewayKeys, GatewayRequests                 gormx.TableName
	Rotations, Credentials                                       gormx.TableName
	Inspections                                                  gormx.TableName
	Enrollments                                                  gormx.TableName
	Agents, AgentRoles                                           gormx.TableName
	Machines, Sessions, Runtimes, Workdirs, Slots, Tools, Prices gormx.TableName
}

func BindNames(prefix string) TableNames {
	return TableNames{ManualModels: gormx.NewTableName(prefix, model.ManualModelCatalog{}.TableName()), TerminalSessions: gormx.NewTableName(prefix, model.TerminalSession{}.TableName()), TerminalAccess: gormx.NewTableName(prefix, model.TerminalAccess{}.TableName()), AgentMachineCommands: gormx.NewTableName(prefix, model.AgentMachineCommand{}.TableName()), EnsemblePlans: gormx.NewTableName(prefix, model.EnsemblePlan{}.TableName()), EnsembleBranches: gormx.NewTableName(prefix, model.EnsembleBranch{}.TableName()), ResetCommands: gormx.NewTableName(prefix, model.ResetCommand{}.TableName()), GatewayConfigs: gormx.NewTableName(prefix, model.GatewayConfig{}.TableName()), GatewayKeys: gormx.NewTableName(prefix, model.GatewayKey{}.TableName()), GatewayRequests: gormx.NewTableName(prefix, model.GatewayRequest{}.TableName()), Rotations: gormx.NewTableName(prefix, model.CredentialRotation{}.TableName()), Credentials: gormx.NewTableName(prefix, model.MachineCredential{}.TableName()), Inspections: gormx.NewTableName(prefix, model.Inspection{}.TableName()), Enrollments: gormx.NewTableName(prefix, model.Enrollment{}.TableName()), Agents: gormx.NewTableName(prefix, model.Agent{}.TableName()), AgentRoles: gormx.NewTableName(prefix, model.AgentRole{}.TableName()),
		Machines: gormx.NewTableName(prefix, model.Machine{}.TableName()), Sessions: gormx.NewTableName(prefix, model.RunnerSession{}.TableName()),
		Runtimes: gormx.NewTableName(prefix, model.MachineRuntime{}.TableName()), Workdirs: gormx.NewTableName(prefix, model.Workdir{}.TableName()),
		Slots: gormx.NewTableName(prefix, model.SlotReservation{}.TableName()), Tools: gormx.NewTableName(prefix, model.MachineTool{}.TableName()), Prices: gormx.NewTableName(prefix, model.ModelPrice{}.TableName())}
}

func BindRotations(db *gorm.DB, names TableNames) *credentialRotation {
	return Use(db).CredentialRotation.Table(names.Rotations.String())
}
func BindCredentials(db *gorm.DB, names TableNames) *machineCredential {
	return Use(db).MachineCredential.Table(names.Credentials.String())
}

func BindInspections(db *gorm.DB, names TableNames) *inspection {
	return Use(db).Inspection.Table(names.Inspections.String())
}
func BindMachines(db *gorm.DB, names TableNames) *machine {
	return Use(db).Machine.Table(names.Machines.String())
}
func BindSessions(db *gorm.DB, names TableNames) *runnerSession {
	return Use(db).RunnerSession.Table(names.Sessions.String())
}
func BindRuntimes(db *gorm.DB, names TableNames) *machineRuntime {
	return Use(db).MachineRuntime.Table(names.Runtimes.String())
}
func BindWorkdirs(db *gorm.DB, names TableNames) *workdir {
	return Use(db).Workdir.Table(names.Workdirs.String())
}
func BindSlots(db *gorm.DB, names TableNames) *slotReservation {
	return Use(db).SlotReservation.Table(names.Slots.String())
}
func BindTools(db *gorm.DB, names TableNames) *machineTool {
	return Use(db).MachineTool.Table(names.Tools.String())
}
func BindPrices(db *gorm.DB, names TableNames) *modelPrice {
	return Use(db).ModelPrice.Table(names.Prices.String())
}
func BindAgents(db *gorm.DB, names TableNames) *agent {
	return Use(db).Agent.Table(names.Agents.String())
}
func BindAgentRoles(db *gorm.DB, names TableNames) *agentRole {
	return Use(db).AgentRole.Table(names.AgentRoles.String())
}

func BindEnrollments(db *gorm.DB, names TableNames) *enrollment {
	return Use(db).Enrollment.Table(names.Enrollments.String())
}

func BindGatewayConfigs(db *gorm.DB, names TableNames) *gatewayConfig {
	return Use(db).GatewayConfig.Table(names.GatewayConfigs.String())
}
func BindGatewayKeys(db *gorm.DB, names TableNames) *gatewayKey {
	return Use(db).GatewayKey.Table(names.GatewayKeys.String())
}
func BindGatewayRequests(db *gorm.DB, names TableNames) *gatewayRequest {
	return Use(db).GatewayRequest.Table(names.GatewayRequests.String())
}

func BindResetCommands(db *gorm.DB, names TableNames) *resetCommand {
	return Use(db).ResetCommand.Table(names.ResetCommands.String())
}

func BindEnsemblePlans(db *gorm.DB, names TableNames) *ensemblePlan {
	return Use(db).EnsemblePlan.Table(names.EnsemblePlans.String())
}
func BindEnsembleBranches(db *gorm.DB, names TableNames) *ensembleBranch {
	return Use(db).EnsembleBranch.Table(names.EnsembleBranches.String())
}

func BindAgentMachineCommands(db *gorm.DB, n TableNames) *agentMachineCommand {
	return Use(db).AgentMachineCommand.Table(n.AgentMachineCommands.String())
}

func BindTerminalSessions(db *gorm.DB, n TableNames) *terminalSession {
	return Use(db).TerminalSession.Table(n.TerminalSessions.String())
}
func BindTerminalAccess(db *gorm.DB, n TableNames) *terminalAccess {
	return Use(db).TerminalAccess.Table(n.TerminalAccess.String())
}

func BindManualModels(db *gorm.DB, names TableNames) *manualModelCatalog {
	return Use(db).ManualModelCatalog.Table(names.ManualModels.String())
}
