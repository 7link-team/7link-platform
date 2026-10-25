package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type MachineRuntimeView struct {
	Kind        string    `json:"kind"`
	Version     string    `json:"version"`
	Path        string    `json:"path"`
	Models      []string  `json:"models"`
	InspectedAt time.Time `json:"inspected_at"`
}
type MachineToolView struct {
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Path        string    `json:"path"`
	InspectedAt time.Time `json:"inspected_at"`
}
type MachineWorkdirView struct {
	Version      int        `json:"version"`
	ArchivedAt   *time.Time `json:"archived_at,omitempty"`
	ID           guid.ID    `json:"id"`
	ProjectID    guid.ID    `json:"project_id"`
	RepositoryID guid.ID    `json:"repository_id"`
	Path         string     `json:"path"`
}
type MachineSlotView struct {
	ExecutionID guid.ID `json:"execution_id"`
	SlotNo      int     `json:"slot_no"`
}
type MachineCapabilities struct {
	Discoveries  []InspectionView     `json:"discoveries"`
	Runtimes     []MachineRuntimeView `json:"runtimes"`
	Tools        []MachineToolView    `json:"tools"`
	Workdirs     []MachineWorkdirView `json:"workdirs"`
	ActiveSlots  []MachineSlotView    `json:"active_slots"`
	RemoteAccess string               `json:"remote_access" enum:"UNAVAILABLE,AVAILABLE"`
}
type ModelGatewayStatus struct {
	Available bool     `json:"available"`
	Reason    string   `json:"reason" enum:"RUNNER_GATEWAY_UNAVAILABLE"`
	Models    []string `json:"models"`
}

func (m *Module) registerCapabilitiesHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.capabilities.get", Method: http.MethodGet, Path: "/machines/{machine_id}/capabilities", Summary: "Read observed runtimes, models, tools and workdirs", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.capabilitiesHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "model-gateway.get", Method: http.MethodGet, Path: "/model-gateway", Summary: "Read model gateway availability", Tags: []string{"machines"}, Errors: []int{401, 403, 503}}, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, func(ctx context.Context, _ *struct{}) (*respx.Body[ModelGatewayStatus], error) {
		return respx.OK(ctx, ModelGatewayStatus{Available: false, Reason: "RUNNER_GATEWAY_UNAVAILABLE", Models: []string{}}), nil
	})
}
func (m *Module) capabilitiesOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID) (MachineCapabilities, error) {
	out := MachineCapabilities{Discoveries: []InspectionView{}, Runtimes: []MachineRuntimeView{}, Tools: []MachineToolView{}, Workdirs: []MachineWorkdirView{}, ActiveSlots: []MachineSlotView{}, RemoteAccess: "UNAVAILABLE"}
	if _, err := m.machineOn(ctx, tx, tenant, machine, false); err != nil {
		return out, err
	}
	if m.terminalBroker != nil && m.terminalBroker.available(tenant, machine) {
		out.RemoteAccess = "AVAILABLE"
	}
	r := query.BindRuntimes(tx, m.names)
	runtimes, err := r.WithContext(ctx).Where(r.TenantID.Eq(tenant), r.MachineID.Eq(machine)).Order(r.ID).Find()
	if err != nil {
		return out, err
	}
	for _, row := range runtimes {
		models := []string{}
		if json.Unmarshal([]byte(row.ModelsJSON), &models) != nil {
			return out, ErrUnavailable
		}
		out.Runtimes = append(out.Runtimes, MachineRuntimeView{Kind: row.Kind, Version: row.Version, Path: row.ExecutablePath, Models: models, InspectedAt: row.InspectedAt})
	}
	t := query.BindTools(tx, m.names)
	tools, err := t.WithContext(ctx).Where(t.TenantID.Eq(tenant), t.MachineID.Eq(machine)).Order(t.ID).Find()
	if err != nil {
		return out, err
	}
	for _, row := range tools {
		out.Tools = append(out.Tools, MachineToolView{Name: row.Name, Version: row.Version, Path: row.ExecutablePath, InspectedAt: row.InspectedAt})
	}
	w := query.BindWorkdirs(tx, m.names)
	workdirs, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.MachineID.Eq(machine)).Order(w.ID).Find()
	if err != nil {
		return out, err
	}
	for _, row := range workdirs {
		out.Workdirs = append(out.Workdirs, workdirView(row))
	}
	s := query.BindSlots(tx, m.names)
	slots, err := s.WithContext(ctx).Where(s.TenantID.Eq(tenant), s.MachineID.Eq(machine), s.ReleasedAt.IsNull()).Order(s.SlotNo).Find()
	if err != nil {
		return out, err
	}
	for _, row := range slots {
		out.ActiveSlots = append(out.ActiveSlots, MachineSlotView{ExecutionID: row.ExecutionID, SlotNo: row.SlotNo})
	}

	inspections := query.BindInspections(tx, m.names)
	scopes := []string{"home"}
	for _, w := range workdirs {
		if w.ArchivedAt == nil {
			scopes = append(scopes, w.ID.String())
		}
	}
	for _, scope := range scopes {
		for _, kind := range []string{"MODELS", "CAPABILITIES"} {
			if scope != "home" && kind == "MODELS" {
				continue
			}
			rows, e := inspections.WithContext(ctx).Where(inspections.TenantID.Eq(tenant), inspections.MachineID.Eq(machine), inspections.Kind.Eq(kind), inspections.ScopeKey.Eq(scope), inspections.State.Eq("READY")).Order(inspections.ID.Desc()).Limit(1).Find()
			if e != nil {
				return out, e
			}
			if len(rows) > 0 && scope != "home" {
				for _, w := range workdirs {
					if w.ID.String() == scope && w.Path != rows[0].WorkdirPath {
						rows = nil
						break
					}
				}
			}
			if len(rows) > 0 {
				out.Discoveries = append(out.Discoveries, inspectionView(rows[0]))
			}
		}
	}
	return out, nil
}
func (m *Module) capabilitiesHTTP(ctx context.Context, in *MachineInput) (*respx.Body[MachineCapabilities], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out MachineCapabilities
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.controlAuthorityOn(ctx, tx, authn.TenantIDFromContext(ctx), "machine_read"); e != nil {
			return e
		}
		var e error
		out, e = m.capabilitiesOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
