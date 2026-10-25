package fleet

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	fleeti18n "7link.com/platform/internal/fleet/i18n"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gen"
	"gorm.io/gorm"
)

type MachineInput struct {
	MachineID string `path:"machine_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type machineListInput struct {
	Cursor    string `query:"cursor" maxLength:"19" doc:"Last machine identifier returned by the previous page."`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	State     string `query:"state" enum:",ONLINE,OFFLINE,DRAINING,REVOKED"`
	Runtime   string `query:"runtime" enum:",CODEX,CLAUDE_CODE"`
	FreeSlots bool   `query:"free_slots" doc:"Only online machines with at least one unreserved execution slot."`
}
type machineView struct {
	ID             guid.ID `json:"id"`
	Name           string  `json:"name"`
	State          string  `json:"state" enum:"ONLINE,OFFLINE,DRAINING,REVOKED"`
	OS             string  `json:"os" enum:"WINDOWS,MACOS,LINUX"`
	SlotCount      int     `json:"slot_count"`
	DrainRequested bool    `json:"drain_requested"`
	Version        int     `json:"version"`
	RemoteAccess   string  `json:"remote_access" enum:"UNAVAILABLE,AVAILABLE"`
}
type machinePage struct {
	Items      []machineView `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}
type machineDrainInput struct {
	MachineInput
	IfMatch string `header:"If-Match" required:"true"`
	Body    struct {
		Drain bool `json:"drain"`
	}
}
type sessionInput struct {
	MachineInput
	Body struct {
		Incarnation string `json:"incarnation_id" minLength:"1" maxLength:"64"`
	}
}
type sessionView struct {
	ID        guid.ID `json:"id"`
	ConnEpoch int64   `json:"conn_epoch"`
}
type heartbeatInput struct {
	MachineInput
	Body struct {
		SessionID guid.ID `json:"session_id"`
		ConnEpoch int64   `json:"conn_epoch" minimum:"1"`
	}
}
type inventoryInput struct {
	MachineInput
	Body struct {
		SessionID       guid.ID   `json:"session_id"`
		ConnEpoch       int64     `json:"conn_epoch" minimum:"1"`
		Inventory       Inventory `json:"inventory"`
		ObservationMode string    `json:"observation_mode,omitempty" enum:",FULL,RUNTIME_VERSIONS"`
	}
}

func fleetHTTPError(ctx context.Context, err error) error {
	var capability *RebindCapabilityError
	if errors.As(err, &capability) {
		code := 142007
		if errors.Is(err, ErrAgentRebindEffort) {
			code = 142026
		}
		return respx.ErrStatus(ctx, 422, code, fleeti18n.Invalid, capability)
	}
	switch {
	case errors.Is(err, ErrAgentRunningExecutions):
		return respx.ErrStatus(ctx, 409, 142012, fleeti18n.Conflict, nil)
	case errors.Is(err, ErrAgentRebindState):
		return respx.ErrStatus(ctx, 409, 142010, fleeti18n.Conflict, nil)
	case errors.Is(err, ErrAgentRebindSameTarget):
		return respx.ErrStatus(ctx, 422, 142013, fleeti18n.Invalid, nil)
	case errors.Is(err, ErrAgentRebindRuntime):
		return respx.ErrStatus(ctx, 422, 142003, fleeti18n.Invalid, nil)
	case errors.Is(err, ErrAgentRebindMCP):
		return respx.ErrStatus(ctx, 422, 142007, fleeti18n.Invalid, nil)
	case errors.Is(err, ErrAgentRebindEffort):
		return respx.ErrStatus(ctx, 422, 142026, fleeti18n.Invalid, nil)
	case errors.Is(err, ErrInvalidArgument):
		return respx.ErrStatus(ctx, 422, 422, fleeti18n.Invalid, nil)
	case errors.Is(err, ErrMachineNotFound):
		return respx.ErrStatus(ctx, 404, 404, fleeti18n.NotFound, nil)
	case errors.Is(err, ErrTunnelQuota):
		return respx.ErrStatus(ctx, 429, 140486, fleeti18n.TunnelQuota, nil)
	case errors.Is(err, ErrMachineForbidden):
		return respx.ErrStatus(ctx, 403, 403, fleeti18n.Forbidden, nil)
	case errors.Is(err, ErrMachineOffline), errors.Is(err, ErrVersionConflict), errors.Is(err, ErrSessionStale), errors.Is(err, ErrSessionActive), errors.Is(err, ErrMachineRevoked):
		return respx.ErrStatus(ctx, 409, 409, fleeti18n.Conflict, nil)
	default:
		return respx.ErrStatus(ctx, 503, 503, fleeti18n.Unavailable, nil)
	}
}
func (m *Module) machineOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	q := query.BindMachines(db, m.names)
	row, err := q.WithContext(ctx).Select(q.TenantID).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: row.TenantID}, true, nil
}
func (m *Module) RegisterHTTP(ctx context.Context, api huma.API) error {
	if err := m.registerPreviewTunnelHTTP(api); err != nil {
		return err
	}
	if m == nil || m.registrar == nil {
		return ErrUnavailable
	}
	if err := m.registerTerminalHTTP(api); err != nil {
		return err
	}

	m.registerAgentSpawnHTTP(api)
	if err := m.registerAgentRebindHTTP(api); err != nil {
		return err
	}
	m.registerDirectoryHTTP(api)
	if err := m.registerAgentRegistrationHTTP(api); err != nil {
		return err
	}
	if err := m.registerAgentWorkersHTTP(api); err != nil {
		return err
	}
	if _, err := m.writer(); err != nil {
		slog.WarnContext(ctx, "fleet routes unavailable; configure a writable database")
	}
	authz.Register(m.registrar, api, huma.Operation{OperationID: "machine.list", Method: http.MethodGet, Path: "/machines", Summary: "List machines", Tags: []string{"machines"}, Errors: []int{401, 403, 422, 503}}, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.listMachinesHTTP)
	if err := m.registerEnrollmentHTTP(api); err != nil {
		return err
	}
	if err := m.registerEnsembleHTTP(api); err != nil {
		return err
	}
	if err := m.registerModelCatalogHTTP(api); err != nil {
		return err
	}
	m.registerCapabilitiesHTTP(api)
	if err := m.registerDiscoveryHTTP(api); err != nil {
		return err
	}
	if err := m.registerGatewayHTTP(api); err != nil {
		return err
	}
	if err := m.registerResetHTTP(api); err != nil {
		return err
	}
	m.registerCredentialsHTTP(api)
	if err := m.registerGatewayBudgetsHTTP(api); err != nil {
		return err
	}
	if err := m.registerCredentialRotationHTTP(api); err != nil {
		return err
	}
	if err := m.registerInspectionsHTTP(api); err != nil {
		return err
	}
	if err := m.registerWorkdirHTTP(api); err != nil {
		return err
	}
	owner := authz.ResourceOwnerResolver(m.machineOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.get", Method: http.MethodGet, Path: "/machines/{machine_id}", Summary: "Read a machine", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 422, 503}}, "machine_id", owner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.getMachineHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.drain", Method: http.MethodPut, Path: "/machines/{machine_id}/drain", Summary: "Change machine drain intent", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "machine_id", owner, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.drainMachineHTTP)
	// A runner authenticates as its own IAM service account. Human fleet grants
	// are not required for heartbeat; OpenSessionOn/HeartbeatOn enforce ownership.
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.session.create", Method: http.MethodPost, Path: "/machines/{machine_id}/runner-sessions", Summary: "Open a fenced runner session", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, m.openSessionHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.heartbeat", Method: http.MethodPut, Path: "/machines/{machine_id}/heartbeat", Summary: "Renew the current runner heartbeat", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.heartbeatHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.inventory", Method: http.MethodPut, Path: "/machines/{machine_id}/inventory", Summary: "Replace observed runner capabilities", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.inventoryHTTP)
	for _, path := range []string{"/machines/{machine_id}/drain", "/machines/{machine_id}/runner-sessions", "/machines/{machine_id}/heartbeat", "/machines/{machine_id}/inventory"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) machineViewOn(ctx context.Context, tx *gorm.DB, tenant, id guid.ID) (machineView, error) {
	row, err := m.machineOn(ctx, tx, tenant, id, false)
	if err != nil {
		return machineView{}, err
	}
	fact, err := m.MachineByIDOn(ctx, tx, tenant, id)
	remote := "UNAVAILABLE"
	if m.terminalBroker != nil && m.terminalBroker.available(tenant, id) {
		remote = "AVAILABLE"
	}
	return machineView{ID: id, Name: fact.Name, State: fact.State, OS: fact.OS, SlotCount: fact.SlotCount, DrainRequested: row.DrainRequested, Version: row.Version, RemoteAccess: remote}, err
}
func (m *Module) listMachinesHTTP(ctx context.Context, in *machineListInput) (*respx.Body[machinePage], error) {
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	cursor := guid.ID(0)
	if in.Cursor != "" {
		cursor, err = guid.RequireID(in.Cursor)
		if err != nil {
			return nil, fleetHTTPError(ctx, ErrInvalidArgument)
		}
	}
	if in.Limit < 1 || in.Limit > 100 || !slices.Contains([]string{"", "ONLINE", "OFFLINE", "DRAINING", "REVOKED"}, in.State) || !slices.Contains([]string{"", "CODEX", "CLAUDE_CODE"}, in.Runtime) {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	out := machinePage{Items: []machineView{}}
	tenant := authn.TenantIDFromContext(ctx)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.policy == nil {
			return ErrUnavailable
		}
		c, e := m.policy(ctx, tx, tenant, "machine_read", authn.PrincipalIDFromContext(ctx))
		if e != nil {
			return e
		}
		if !c.AllowAll {
			return ErrMachineForbidden
		}
		q := query.BindMachines(tx, m.names)
		scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Gt(cursor))
		// Apply the same heartbeat freshness rule as machineState before paging.
		cutoff := time.Now().UTC().Add(-30 * time.Second)
		if in.State == "OFFLINE" {
			scope = scope.Where(q.WithContext(ctx).Where(q.State.Eq("OFFLINE")).Or(q.State.In("ONLINE", "DRAINING"), q.WithContext(ctx).Where(q.LastSeenAt.IsNull()).Or(q.LastSeenAt.Lt(cutoff))))
		} else if in.State != "" {
			scope = scope.Where(q.State.Eq(in.State))
			if in.State == "ONLINE" || in.State == "DRAINING" {
				scope = scope.Where(q.LastSeenAt.Gte(cutoff))
			}
		}
		if in.Runtime != "" {
			runtimes := query.BindRuntimes(tx, m.names)
			scope = scope.Where(gen.Columns{q.ID}.In(runtimes.WithContext(ctx).Select(runtimes.MachineID).Where(runtimes.TenantID.Eq(tenant), runtimes.Kind.Eq(in.Runtime))))
		}
		if in.FreeSlots {
			slots := query.BindSlots(tx, m.names)
			scope = scope.Where(q.State.Eq("ONLINE"), q.LastSeenAt.Gte(cutoff), gen.Columns{q.SlotCount}.Gt(slots.WithContext(ctx).Select(slots.ID.Count()).Where(slots.TenantID.Eq(tenant), slots.MachineID.EqCol(q.ID), slots.ActiveSlot.IsNotNull())))
		}
		rows, e := scope.Order(q.ID).Limit(in.Limit + 1).Find()
		if e != nil {
			return e
		}
		if len(rows) > in.Limit {
			out.HasMore = true
			rows = rows[:in.Limit]
		}
		for _, row := range rows {
			v, e := m.machineViewOn(ctx, tx, tenant, row.ID)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
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
func (m *Module) getMachineHTTP(ctx context.Context, in *MachineInput) (*respx.Body[machineView], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out machineView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if m.policy == nil {
			return ErrUnavailable
		}
		c, e := m.policy(ctx, tx, authn.TenantIDFromContext(ctx), "machine_read", authn.PrincipalIDFromContext(ctx))
		if e != nil {
			return e
		}
		if !c.AllowAll {
			return ErrMachineForbidden
		}
		out, e = m.machineViewOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) drainMachineHTTP(ctx context.Context, in *machineDrainInput) (*respx.Body[machineView], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	version, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || version < 1 {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out machineView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := m.SetDrainOn(ctx, tx, authn.TenantIDFromContext(ctx), id, version, in.Body.Drain); e != nil {
			return e
		}
		var e error
		out, e = m.machineViewOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) openSessionHTTP(ctx context.Context, in *sessionInput) (*respx.Body[sessionView], error) {
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
	var s SessionFact
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		s, e = m.OpenSessionOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.Incarnation)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, sessionView{ID: s.ID, ConnEpoch: s.ConnEpoch}), nil
}
func (m *Module) heartbeatHTTP(ctx context.Context, in *heartbeatInput) (*respx.Body[bool], error) {
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
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.HeartbeatOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.SessionID, in.Body.ConnEpoch)
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, true), nil
}
func (m *Module) inventoryHTTP(ctx context.Context, in *inventoryInput) (*respx.Body[bool], error) {
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
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if in.Body.ObservationMode == "RUNTIME_VERSIONS" {
			return m.recordRuntimeInventoryOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.SessionID, in.Body.ConnEpoch, in.Body.Inventory)
		}
		return m.RecordInventoryOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.SessionID, in.Body.ConnEpoch, in.Body.Inventory)
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, true), nil
}

var _ module.HTTPRegistrar = (*Module)(nil)
