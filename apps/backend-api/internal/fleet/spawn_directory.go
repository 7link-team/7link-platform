package fleet

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SpawnGrantDirectory exposes current identity and observed model inventory.
// It authorizes no spawning and never treats an unknown model as installed.
type SpawnGrantDirectory struct {
	MachineOn func(context.Context, *gorm.DB, guid.ID, guid.ID) (MachineFact, error)
	AgentOn   func(context.Context, *gorm.DB, guid.ID, guid.ID) (AgentIdentity, bool, error)
	ModelOn   func(context.Context, *gorm.DB, guid.ID, guid.ID, string, string) (bool, error)
}

func (m *Module) provideSpawnDirectory(s *module.Services) error {
	return module.Provide[SpawnGrantDirectory](s, SpawnGrantDirectory{MachineOn: m.MachineByIDOn, AgentOn: m.spawnAgentOn, ModelOn: m.spawnModelOn})
}
func (m *Module) spawnAgentOn(ctx context.Context, tx *gorm.DB, tenant, agent guid.ID) (AgentIdentity, bool, error) {
	if err := fleetTransaction(tx, tenant, agent); err != nil {
		return AgentIdentity{}, false, err
	}
	if m == nil || m.names.Agents.IsZero() {
		return AgentIdentity{}, false, ErrUnavailable
	}
	q := query.BindAgents(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(agent)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentIdentity{}, false, nil
	}
	if err != nil {
		return AgentIdentity{}, false, err
	}
	return m.ByPrincipalOn(ctx, tx, tenant, row.PrincipalID)
}
func (m *Module) spawnModelOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, runtime, model string) (bool, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return false, err
	}
	if m == nil || m.names.Runtimes.IsZero() {
		return false, ErrUnavailable
	}
	fact, err := m.MachineByIDOn(ctx, tx, tenant, machine)
	if err != nil {
		return false, err
	}
	if fact.State == "REVOKED" {
		return false, nil
	}
	q := query.BindRuntimes(tx, m.names)
	_, err = q.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.Kind.Eq(runtime)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	catalog, _, err := m.modelCatalogOn(ctx, tx, tenant, machine, runtime)
	if err != nil {
		return false, err
	}
	return slices.Contains(catalog.ObservedIDs, model) || slices.Contains(catalog.ManualIDs, model), nil
}
