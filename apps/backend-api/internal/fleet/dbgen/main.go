package main

import (
	"7link.com/platform/internal/fleet/model"
	gengorm "7link.com/platform/pkg/extension/gorm/gen"
)

func main() {
	gengorm.Generate(model.ManualModelCatalog{}, model.TerminalSession{}, model.TerminalAccess{}, model.AgentMachineCommand{}, model.EnsemblePlan{}, model.EnsembleBranch{}, model.ResetCommand{}, model.GatewayConfig{}, model.GatewayKey{}, model.GatewayRequest{}, model.Agent{}, model.AgentRole{}, model.Machine{}, model.RunnerSession{}, model.MachineRuntime{}, model.Workdir{}, model.SlotReservation{}, model.MachineTool{}, model.ModelPrice{}, model.Enrollment{}, model.Inspection{}, model.CredentialRotation{}, model.MachineCredential{})
}
