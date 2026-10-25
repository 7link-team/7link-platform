package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type InspectionView struct {
	ScopeKey    string             `json:"scope_key"`
	WorkdirPath string             `json:"workdir_path,omitempty"`
	Discovery   []RuntimeDiscovery `json:"discovery"`
	ID          guid.ID            `json:"id"`
	MachineID   guid.ID            `json:"machine_id"`
	Kind        string             `json:"kind" enum:"RUNTIMES,MODELS,CAPABILITIES"`
	State       string             `json:"state" enum:"QUEUED,RUNNING,READY,ERROR"`
	ReasonCode  string             `json:"reason_code"`
	ExpiresAt   time.Time          `json:"expires_at"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

func inspectionView(row *model.Inspection) InspectionView {
	discovery := []RuntimeDiscovery{}
	if row.ResultJSON != "" {
		_ = json.Unmarshal([]byte(row.ResultJSON), &discovery)
	}
	return InspectionView{ScopeKey: row.ScopeKey, WorkdirPath: row.WorkdirPath, Discovery: discovery, ID: row.ID, MachineID: row.MachineID, Kind: row.Kind, State: row.State, ReasonCode: row.ReasonCode, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// InspectionResult contains observations only for a successful full runtime/tool
// scan. Unsupported model/Skills/MCP discovery is an explicit error, never an
// empty successful catalog. Diagnostic text is deliberately not persisted.
type InspectionResult struct {
	Discovery  []RuntimeDiscovery `json:"discovery,omitempty" maxItems:"2"`
	Succeeded  bool               `json:"succeeded"`
	ReasonCode string             `json:"reason_code,omitempty" enum:",INSPECTION_FAILED,UNSUPPORTED_INSPECTION_KIND"`
	Inventory  Inventory          `json:"inventory,omitempty"`
}

func (m *Module) inspectionEventOn(ctx context.Context, tx *gorm.DB, row *model.Inspection) error {
	return m.machineEventOn(ctx, tx, row.TenantID, row.MachineID, "machine.inspection_changed", 0, map[string]any{"inspection_id": row.ID, "kind": row.Kind, "state": row.State, "reason_code": row.ReasonCode})
}

// RequestInspectionOn serializes on the machine and coalesces an outstanding
// request of the same kind. Only a live ONLINE connection can receive new work.
func (m *Module) RequestInspectionOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, kind string) (InspectionView, error) {
	return m.requestScopedInspectionOn(ctx, tx, tenant, machine, kind, 0)
}
func (m *Module) requestScopedInspectionOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, kind string, workdir guid.ID) (InspectionView, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return InspectionView{}, err
	}
	if !slices.Contains([]string{"RUNTIMES", "MODELS", "CAPABILITIES"}, kind) {
		return InspectionView{}, ErrInvalidArgument
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); err != nil {
		return InspectionView{}, err
	}
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return InspectionView{}, err
	}
	scope, path := "home", ""
	if workdir != 0 {
		if workdir < 0 || kind != "CAPABILITIES" {
			return InspectionView{}, ErrInvalidArgument
		}
		w := query.BindWorkdirs(tx, m.names)
		found, e := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.MachineID.Eq(machine), w.ID.Eq(workdir), w.ArchivedAt.IsNull()).First()
		if e != nil {
			return InspectionView{}, ErrMachineNotFound
		}
		scope, path = workdir.String(), found.Path
	}
	ctx = humanMachineContext(ctx)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if machineState(row, now) != "ONLINE" || row.CurrentSessionID == nil {
		return InspectionView{}, ErrVersionConflict
	}
	q := query.BindInspections(tx, m.names)
	prior, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.Kind.Eq(kind), q.ActiveKey.IsNotNull()).First()
	if err == nil {
		if prior.ScopeKey != scope {
			return InspectionView{}, ErrVersionConflict
		}
		return inspectionView(prior), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return InspectionView{}, err
	}
	if m.ids == nil {
		return InspectionView{}, ErrUnavailable
	}
	id, err := m.ids.Next()
	if err != nil {
		return InspectionView{}, err
	}
	if id <= 0 {
		return InspectionView{}, ErrUnavailable
	}
	active := 1
	request := &model.Inspection{ScopeKey: scope, WorkdirPath: path, ID: guid.ID(id), TenantID: tenant, MachineID: machine, RequestedBy: authn.PrincipalIDFromContext(ctx), Kind: kind, State: "QUEUED", ActiveKey: &active, SessionID: *row.CurrentSessionID, ConnEpoch: row.ConnEpoch, ExpiresAt: now.Add(65 * time.Second), CreatedAt: now, UpdatedAt: now}
	if err = q.WithContext(ctx).Create(request); err != nil {
		return InspectionView{}, err
	}
	if err = m.inspectionEventOn(ctx, tx, request); err != nil {
		return InspectionView{}, err
	}
	return inspectionView(request), nil
}

// PullInspectionOn claims once, under the same machine lock as all session
// operations. A lost response is resolved as unknown by the watchdog, not replayed.
func (m *Module) PullInspectionOn(ctx context.Context, tx *gorm.DB, tenant, machine, session guid.ID, epoch int64) ([]InspectionView, error) {
	out := []InspectionView{}
	if err := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return nil, err
	}
	q := query.BindInspections(tx, m.names)
	running, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.State.Eq("RUNNING")).Count()
	if err != nil {
		return nil, err
	}
	if running > 0 {
		return out, nil
	}
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.State.Eq("QUEUED"), q.SessionID.Eq(session), q.ConnEpoch.Eq(epoch), q.ExpiresAt.Gt(time.Now().UTC())).Order(q.ID).Limit(1).Find()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		automatic, e := m.nextWorkdirInspectionOn(ctx, tx, tenant, machine, session, epoch)
		if e != nil {
			return nil, e
		}
		if automatic == nil {
			return out, nil
		}
		rows = append(rows, automatic)
	}
	row := rows[0]
	row.State, row.UpdatedAt = "RUNNING", time.Now().UTC().Truncate(time.Millisecond)
	if _, err = q.WithContext(ctx).Where(q.ID.Eq(row.ID), q.TenantID.Eq(tenant)).Updates(map[string]any{"state": row.State, "updated_at": row.UpdatedAt}); err != nil {
		return nil, err
	}
	if err = m.inspectionEventOn(ctx, tx, row); err != nil {
		return nil, err
	}
	return append(out, inspectionView(row)), nil
}

// ReportInspectionOn stores the terminal receipt and inventory in one caller
// transaction. Exact retry is safe; a changed result, old epoch or late report is
// rejected. Runtime-only inspection cannot claim to have refreshed model facts.
func (m *Module) ReportInspectionOn(ctx context.Context, tx *gorm.DB, tenant, machine, id, session guid.ID, epoch int64, in InspectionResult) (InspectionView, error) {
	if id <= 0 {
		return InspectionView{}, ErrInvalidArgument
	}
	if in.Succeeded && in.ReasonCode != "" || !in.Succeeded && (!slices.Contains([]string{"INSPECTION_FAILED", "UNSUPPORTED_INSPECTION_KIND"}, in.ReasonCode) || len(in.Inventory.Runtimes)+len(in.Inventory.Tools)+len(in.Discovery) > 0) {
		return InspectionView{}, ErrInvalidArgument
	}
	if err := validateInventory(in.Inventory); err != nil {
		return InspectionView{}, err
	}
	if err := m.HeartbeatOn(ctx, tx, tenant, machine, session, epoch); err != nil {
		return InspectionView{}, err
	}
	q := query.BindInspections(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return InspectionView{}, ErrMachineNotFound
	}
	if err != nil {
		return InspectionView{}, err
	}
	if row.SessionID != session || row.ConnEpoch != epoch {
		return InspectionView{}, ErrSessionStale
	}
	encoded, _ := json.Marshal(in) // The concrete DTO contains no unsupported JSON types.
	digest := enrollmentHash(string(encoded))
	if row.State == "READY" || row.State == "ERROR" {
		if row.ResultHash == digest {
			return inspectionView(row), nil
		}
		return InspectionView{}, ErrVersionConflict
	}
	if row.State != "RUNNING" || !row.ExpiresAt.After(time.Now().UTC()) {
		return InspectionView{}, ErrVersionConflict
	}
	if in.Succeeded {
		if row.Kind == "RUNTIMES" {
			if len(in.Discovery) > 0 {
				return InspectionView{}, ErrInvalidArgument
			}
			if err = m.recordRuntimeInventoryOn(ctx, tx, tenant, machine, session, epoch, in.Inventory); err != nil {
				return InspectionView{}, err
			}
		} else {
			if len(in.Inventory.Runtimes)+len(in.Inventory.Tools) > 0 {
				return InspectionView{}, ErrInvalidArgument
			}
			facts, e := m.discoveryOn(ctx, tx, row, in.Discovery)
			if e != nil {
				return InspectionView{}, e
			}
			data, _ := json.Marshal(facts)
			row.ResultJSON = string(data)
		}
		row.State = "READY"
	} else {
		row.State = "ERROR"
	}
	row.ReasonCode, row.ResultHash, row.ActiveKey, row.UpdatedAt = in.ReasonCode, digest, nil, time.Now().UTC().Truncate(time.Millisecond)
	if _, err = q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant)).Updates(map[string]any{"state": row.State, "reason_code": row.ReasonCode, "result_hash": digest, "result_json": row.ResultJSON, "active_key": nil, "updated_at": row.UpdatedAt}); err != nil {
		return InspectionView{}, err
	}
	if err = m.inspectionEventOn(ctx, tx, row); err != nil {
		return InspectionView{}, err
	}
	return inspectionView(row), nil
}

func (m *Module) expireInspectionOn(ctx context.Context, tx *gorm.DB, tenant, machine, id guid.ID, now time.Time) error {
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return err
	}
	q := query.BindInspections(tx, m.names)
	request, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	if request.ActiveKey == nil {
		return nil
	}
	if request.ExpiresAt.After(now) && machineState(row, now) != "OFFLINE" && row.State != "REVOKED" && row.ConnEpoch == request.ConnEpoch && row.CurrentSessionID != nil && *row.CurrentSessionID == request.SessionID {
		return nil
	}
	request.State, request.ReasonCode, request.UpdatedAt = "ERROR", "RESULT_UNKNOWN", now
	if _, err = q.WithContext(ctx).Where(q.ID.Eq(id), q.TenantID.Eq(tenant)).Updates(map[string]any{"state": request.State, "reason_code": request.ReasonCode, "active_key": nil, "updated_at": now}); err != nil {
		return err
	}
	return m.inspectionEventOn(ctx, tx, request)
}

func (m *Module) expireInspections(ctx context.Context, db *gorm.DB, tenant guid.ID, now time.Time) error {
	q := query.BindInspections(db, m.names)
	cursor := guid.ID(0)
	for {
		rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ActiveKey.IsNotNull(), q.ID.Gt(cursor)).Order(q.ID).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return m.expireInspectionOn(ctx, tx, tenant, row.MachineID, row.ID, now) }); err != nil {
				return err
			}
			cursor = row.ID
		}
	}
}

type inspectionCreateInput struct {
	MachineInput
	Body struct {
		Kind      string  `json:"kind" enum:"RUNTIMES,MODELS,CAPABILITIES"`
		WorkdirID guid.ID `json:"workdir_id,omitempty"`
	}
}
type InspectionSession struct {
	SessionID guid.ID `json:"session_id"`
	ConnEpoch int64   `json:"conn_epoch" minimum:"1"`
}
type inspectionPullInput struct {
	MachineInput
	Body InspectionSession
}
type inspectionReportInput struct {
	MachineInput
	InspectionID string `path:"inspection_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body         struct {
		InspectionSession
		InspectionResult
	}
}
type inspectionListInput struct {
	MachineInput
	Cursor string `query:"cursor" maxLength:"19"`
	Limit  int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
}
type inspectionPage struct {
	Items      []InspectionView `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

func (m *Module) registerInspectionsHTTP(api huma.API) error {
	owner := authz.ResourceOwnerResolver(m.machineOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.inspection.create", Method: http.MethodPost, Path: "/machines/{machine_id}/inspections", Summary: "Request a machine inspection", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, "machine_id", owner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.requestInspectionHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.inspection.list", Method: http.MethodGet, Path: "/machines/{machine_id}/inspections", Summary: "Read machine inspection requests", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 422, 503}}, "machine_id", owner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.listInspectionsHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.inspection.pull", Method: http.MethodPost, Path: "/machines/{machine_id}/inspection-pulls", Summary: "Claim one queued machine inspection", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.pullInspectionHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.inspection.report", Method: http.MethodPut, Path: "/machines/{machine_id}/inspections/{inspection_id}/result", Summary: "Report a fenced machine inspection result", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.reportInspectionHTTP)
	for _, path := range []string{"/machines/{machine_id}/inspections", "/machines/{machine_id}/inspection-pulls", "/machines/{machine_id}/inspections/{inspection_id}/result"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) requestInspectionHTTP(ctx context.Context, in *inspectionCreateInput) (*respx.Body[InspectionView], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out InspectionView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.requestScopedInspectionOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.Kind, in.Body.WorkdirID)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) pullInspectionHTTP(ctx context.Context, in *inspectionPullInput) (*respx.Body[[]InspectionView], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.BindRunnerContext(ctx, id)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out []InspectionView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.PullInspectionOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.SessionID, in.Body.ConnEpoch)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) reportInspectionHTTP(ctx context.Context, in *inspectionReportInput) (*respx.Body[InspectionView], error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.BindRunnerContext(ctx, machine)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	id, err := guid.RequireID(in.InspectionID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out InspectionView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.ReportInspectionOn(ctx, tx, authn.TenantIDFromContext(ctx), machine, id, in.Body.SessionID, in.Body.ConnEpoch, in.Body.InspectionResult)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listInspectionsHTTP(ctx context.Context, in *inspectionListInput) (*respx.Body[inspectionPage], error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	cursor := guid.ID(0)
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	out := inspectionPage{Items: []InspectionView{}}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); e != nil {
			return e
		}
		if _, e := m.machineOn(ctx, tx, tenant, machine, false); e != nil {
			return e
		}
		q := query.BindInspections(tx, m.names)
		rows, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Gt(cursor)).Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		out.HasMore = len(rows) > in.Limit
		if out.HasMore {
			rows = rows[:in.Limit]
		}
		for _, row := range rows {
			out.Items = append(out.Items, inspectionView(row))
		}
		if out.HasMore {
			out.NextCursor = rows[len(rows)-1].ID.String()
		}
		return nil
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
