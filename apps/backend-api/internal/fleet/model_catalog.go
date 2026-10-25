package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ManualModelsUpdate struct {
	ExpectedVersion int               `json:"expected_version" minimum:"0"`
	ModelIDs        []string          `json:"model_ids" maxItems:"50"`
	Families        map[string]string `json:"families"`
}
type ModelCatalog struct {
	ResolvedFamilies map[string]string `json:"resolved_families"`
	Runtime          string            `json:"runtime" enum:"CODEX,CLAUDE_CODE"`
	ObservedIDs      []string          `json:"observed_ids"`
	ManualIDs        []string          `json:"manual_ids"`
	Families         map[string]string `json:"families"`
	Version          int               `json:"version"`
}

func (m *Module) modelCatalogOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, runtime string) (ModelCatalog, *model.ManualModelCatalog, error) {
	out := ModelCatalog{Runtime: runtime, ObservedIDs: []string{}, ManualIDs: []string{}, Families: map[string]string{}}
	if !slices.Contains([]string{"CODEX", "CLAUDE_CODE"}, runtime) {
		return out, nil, ErrInvalidArgument
	}
	r := query.BindRuntimes(tx, m.names)
	observed, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.MachineID.Eq(machine), r.Kind.Eq(runtime)).Find()
	if err != nil {
		return out, nil, err
	}
	if len(observed) > 0 {
		if err := json.Unmarshal([]byte(observed[0].ModelsJSON), &out.ObservedIDs); err != nil {
			return out, nil, errors.Join(ErrUnavailable, err)
		}
	}
	i := query.BindInspections(tx, m.names)
	receipts, err := i.WithContext(ctx).Where(i.TenantID.Eq(tenant), i.MachineID.Eq(machine), i.Kind.Eq("MODELS"), i.ScopeKey.Eq("home"), i.State.Eq("READY")).Order(i.ID.Desc()).Limit(1).Find()
	if err != nil {
		return out, nil, err
	}
	if len(receipts) == 1 {
		var discoveries []RuntimeDiscovery
		if json.Unmarshal([]byte(receipts[0].ResultJSON), &discoveries) != nil {
			return out, nil, ErrUnavailable
		}
		for _, d := range discoveries {
			if d.Runtime == runtime && d.LastSuccessAt != nil {
				out.ResolvedFamilies = map[string]string{}
				for id, family := range d.Families {
					if slices.Contains(out.ObservedIDs, id) {
						out.ResolvedFamilies[id] = family
					}
				}
			}
		}
	}
	q := query.BindManualModels(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.Runtime.Eq(runtime)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		resolveCatalogFamilies(&out)
		return out, nil, nil
	}
	if err != nil {
		return out, nil, err
	}
	if json.Unmarshal([]byte(row.ModelsJSON), &out.ManualIDs) != nil || json.Unmarshal([]byte(row.FamiliesJSON), &out.Families) != nil {
		return out, nil, ErrUnavailable
	}
	out.Version = row.Version
	resolveCatalogFamilies(&out)
	return out, row, nil
}

func (m *Module) ModelCatalogOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, runtime string) (ModelCatalog, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return ModelCatalog{}, err
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); err != nil {
		return ModelCatalog{}, err
	}
	if _, err := m.machineOn(ctx, tx, tenant, machine, true); err != nil {
		return ModelCatalog{}, err
	}
	out, _, err := m.modelCatalogOn(ctx, tx, tenant, machine, runtime)
	return out, err
}

// SetManualModelsOn keeps refresh-owned observations separate. Replayed writes
// recheck current Human authority before returning the existing version.
func (m *Module) SetManualModelsOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, runtime string, in ManualModelsUpdate) (ModelCatalog, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return ModelCatalog{}, err
	}
	if in.ExpectedVersion < 0 || len(in.ModelIDs) > 50 || len(in.Families) > 1050 {
		return ModelCatalog{}, ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_manage"); err != nil {
		return ModelCatalog{}, err
	}
	machineRow, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return ModelCatalog{}, err
	}
	if machineRow.State == "REVOKED" {
		return ModelCatalog{}, ErrMachineRevoked
	}
	out, row, err := m.modelCatalogOn(ctx, tx, tenant, machine, runtime)
	if err != nil {
		return ModelCatalog{}, err
	}
	ids := slices.Clone(in.ModelIDs)
	if ids == nil {
		ids = []string{}
	}
	slices.Sort(ids)
	for i, id := range ids {
		if !discoveryID(id, 160) || i > 0 && ids[i-1] == id {
			return ModelCatalog{}, ErrInvalidArgument
		}
	}
	families := maps.Clone(in.Families)
	if families == nil {
		families = map[string]string{}
	}
	for id, family := range families {
		if !slices.Contains(ids, id) && !slices.Contains(out.ObservedIDs, id) || !slices.Contains([]string{"openai", "anthropic", "google"}, family) {
			return ModelCatalog{}, ErrInvalidArgument
		}
	}
	modelsJSON, _ := json.Marshal(ids)
	familiesJSON, _ := json.Marshal(families)
	same := slices.Equal(ids, out.ManualIDs) && maps.Equal(families, out.Families)
	if out.Version != in.ExpectedVersion {
		if out.Version == in.ExpectedVersion+1 && same {
			return out, nil
		}
		return ModelCatalog{}, ErrVersionConflict
	}
	if same {
		return out, nil
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if row == nil {
		if m.ids == nil {
			return ModelCatalog{}, ErrUnavailable
		}
		id, e := m.ids.Next()
		if e != nil {
			return ModelCatalog{}, e
		}
		if id <= 0 {
			return ModelCatalog{}, ErrUnavailable
		}
		row = &model.ManualModelCatalog{ID: guid.ID(id), TenantID: tenant, MachineID: machine, Runtime: runtime, Version: 1, ModelsJSON: string(modelsJSON), FamiliesJSON: string(familiesJSON), UpdatedAt: now}
		err = query.BindManualModels(tx, m.names).WithContext(ctx).Create(row)
	} else {
		q := query.BindManualModels(tx, m.names)
		result, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.Runtime.Eq(runtime), q.Version.Eq(in.ExpectedVersion)).Updates(map[string]any{"models_json": string(modelsJSON), "families_json": string(familiesJSON), "version": row.Version + 1, "updated_at": now})
		err = e
		if err == nil && result.RowsAffected != 1 {
			err = ErrVersionConflict
		}
		row.Version++
	}
	if err != nil {
		return ModelCatalog{}, err
	}
	if err = m.machineEventOn(humanMachineContext(ctx), tx, tenant, machine, "machine.runtimes_changed", row.Version, map[string]any{"runtime": runtime, "manual_count": len(ids), "catalog_version": row.Version}); err != nil {
		return ModelCatalog{}, err
	}
	current, _, err := m.modelCatalogOn(ctx, tx, tenant, machine, runtime)
	return current, err
}

func inferredModelFamily(runtime, id string) string {
	switch {
	case strings.HasPrefix(id, "claude-"):
		return "anthropic"
	case strings.HasPrefix(id, "gemini-"):
		return "google"
	case strings.HasPrefix(id, "gpt-"), strings.HasPrefix(id, "codex-"), strings.HasPrefix(id, "o1"), strings.HasPrefix(id, "o3"), strings.HasPrefix(id, "o4"):
		return "openai"
	case runtime == "CLAUDE_CODE":
		return "anthropic"
	case runtime == "CODEX":
		return "openai"
	default:
		return ""
	}
}

func resolveCatalogFamilies(out *ModelCatalog) {
	if out.ResolvedFamilies == nil {
		out.ResolvedFamilies = map[string]string{}
	}
	for _, id := range append(slices.Clone(out.ObservedIDs), out.ManualIDs...) {
		if family := out.Families[id]; family != "" {
			out.ResolvedFamilies[id] = family
		} else if out.ResolvedFamilies[id] == "" {
			out.ResolvedFamilies[id] = inferredModelFamily(out.Runtime, id)
		}
	}
}

// ModelFamilyOn is the single catalog resolver. Unknown IDs use the documented
// runtime default; a family is a classification, never proof of installation.
func (m *Module) ModelFamilyOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, runtime, id string) (string, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return "", err
	}
	if !discoveryID(id, 160) {
		return "", ErrInvalidArgument
	}
	if _, err := m.machineOn(ctx, tx, tenant, machine, false); err != nil {
		return "", err
	}
	catalog, _, err := m.modelCatalogOn(ctx, tx, tenant, machine, runtime)
	if err != nil {
		return "", err
	}
	if family := catalog.ResolvedFamilies[id]; family != "" {
		return family, nil
	}
	return inferredModelFamily(runtime, id), nil
}
