package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	fleeti18n "7link.com/platform/internal/fleet/i18n"
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	idempotencyi18n "7link.com/platform/internal/infra/idempotency/i18n"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
)

type TerminalAccessView struct {
	AllowRemotePreview  bool `json:"allow_remote_preview"`
	TunnelBandwidthKbps int  `json:"tunnel_bandwidth_kbps"`
	TunnelMaxStreams    int  `json:"tunnel_max_streams"`
	AllowRemoteTerminal bool `json:"allow_remote_terminal"`
	Version             int  `json:"version"`
	TunnelAvailable     bool `json:"tunnel_available"`
}
type terminalAccessInput struct {
	MachineInput
	IfMatch string `header:"If-Match" required:"true"`
	Cookie  string `header:"Cookie"`
	Origin  string `header:"Origin"`
	Body    struct {
		Preview    *bool  `json:"allow_remote_preview,omitempty"`
		Enabled    bool   `json:"allow_remote_terminal"`
		Bandwidth  *int   `json:"tunnel_bandwidth_kbps,omitempty" minimum:"1000" maximum:"1000000"`
		MaxStreams *int   `json:"tunnel_max_streams,omitempty" minimum:"16" maximum:"4096"`
		Proof      string `json:"reauth_proof,omitempty" maxLength:"256"`
	}
}
type terminalOpenInput struct {
	MachineInput
	Key    string `header:"Idempotency-Key" maxLength:"64"`
	Cookie string `header:"Cookie"`
	Origin string `header:"Origin"`
	Body   struct {
		ExecutionID guid.ID `json:"execution_id"`
		Cols        int     `json:"cols" default:"80" minimum:"1" maximum:"500"`
		Rows        int     `json:"rows" default:"24" minimum:"1" maximum:"200"`
		Proof       string  `json:"reauth_proof" maxLength:"256"`
	}
}
type TerminalIDInput struct {
	ID string `path:"terminal_session_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type terminalRevokeInput struct {
	TerminalIDInput
	Cookie string `header:"Cookie"`
	Origin string `header:"Origin"`
}

func terminalHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, idempotency.ErrKeyReused) {
		return respx.ErrStatus(ctx, 422, 176425, idempotencyi18n.KeyReused, nil)
	}
	if errors.Is(err, idempotency.ErrInProgress) {
		return respx.ErrStatus(ctx, 409, 176409, idempotencyi18n.InProgress, nil)
	}
	if errors.Is(err, idempotency.ErrInvalidKey) {
		return respx.ErrStatus(ctx, 422, 176425, idempotencyi18n.InvalidKey, nil)
	}
	if errors.Is(err, ErrTerminalAccessDisabled) {
		return respx.ErrStatus(ctx, 403, 130403, fleeti18n.TerminalDisabled, nil)
	}
	if errors.Is(err, ErrTerminalClosed) {
		return respx.ErrStatus(ctx, 409, 130467, fleeti18n.TerminalClosed, nil)
	}
	if errors.Is(err, authn.ErrInvalidCredentials) {
		return respx.ErrStatus(ctx, 403, 130403, fleeti18n.TerminalReauthentication, nil)
	}
	return fleetHTTPError(ctx, err)
}
func (m *Module) registerTerminalHTTP(api huma.API) error {
	errors := []int{401, 403, 404, 409, 422, 503}
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.remote-access.read", Method: http.MethodGet, Path: "/machines/{machine_id}/remote-access", Summary: "Read remote terminal policy", Tags: []string{"machines"}, Errors: errors}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "read", FilterData: true}, m.terminalAccessHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.remote-access.update", Method: http.MethodPut, Path: "/machines/{machine_id}/remote-access", Summary: "Change remote terminal policy", Tags: []string{"machines"}, Errors: errors}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine_access", Verb: "manage"}, m.setTerminalAccessHTTP)
	authz.RegisterOwnedTenantMember(m.registrar, api, huma.Operation{OperationID: "terminal-session.create", Method: http.MethodPost, Path: "/machines/{machine_id}/terminal-sessions", Summary: "Open a recorded interactive terminal", Tags: []string{"machines"}, DefaultStatus: 201, Errors: append(append([]int(nil), errors...), http.StatusTooManyRequests)}, "machine_id", m.machineOwner, m.openTerminalHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "terminal-session.read", Method: http.MethodGet, Path: "/terminal-sessions/{terminal_session_id}", Summary: "Read a terminal session", Tags: []string{"machines"}, Errors: errors}, "terminal_session_id", m.terminalOwner, authz.Guard{Resource: "terminal", Verb: "interact", FilterData: true}, m.getTerminalHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "terminal-session.revoke", Method: http.MethodPost, Path: "/terminal-sessions/{terminal_session_id}/revoke", Summary: "Close a terminal session", Tags: []string{"machines"}, Errors: errors}, "terminal_session_id", m.terminalOwner, authz.Guard{Resource: "terminal", Verb: "interact", FilterData: true}, m.revokeTerminalHTTP)
	for _, path := range []string{"/machines/{machine_id}/remote-access", "/machines/{machine_id}/terminal-sessions", "/terminal-sessions/{terminal_session_id}/revoke"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return m.registerTerminalTunnelHTTP(api)
}
func (m *Module) terminalOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	db, err := m.writer()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	q := query.BindTerminalSessions(db, m.names)
	r, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "tenant", TenantID: r.TenantID, EntityID: r.ProjectID}, true, nil
}
func (m *Module) terminalCSRF(method, cookie, origin string) error {
	if m.revokeBrowser.ValidateCSRF == nil {
		return ErrUnavailable
	}
	if err := m.revokeBrowser.ValidateCSRF(method, origin, cookie); err != nil {
		return ErrMachineForbidden
	}
	return nil
}
func (m *Module) terminalAccessHTTP(ctx context.Context, in *MachineInput) (*respx.Body[TerminalAccessView], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, terminalHTTPError(ctx, ErrInvalidArgument)
	}
	out, err := m.GetTerminalAccess(ctx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) setTerminalAccessHTTP(ctx context.Context, in *terminalAccessInput) (*respx.Body[TerminalAccessView], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, terminalHTTPError(ctx, ErrInvalidArgument)
	}
	version, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || version < 1 {
		return nil, terminalHTTPError(ctx, ErrInvalidArgument)
	}
	if err = m.terminalCSRF(http.MethodPut, in.Cookie, in.Origin); err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	out, err := m.SetTerminalAccessOptions(ctx, authn.TenantIDFromContext(ctx), id, version, TerminalAccessOptions{Enabled: in.Body.Enabled, AllowRemotePreview: in.Body.Preview, TunnelBandwidthKbps: in.Body.Bandwidth, TunnelMaxStreams: in.Body.MaxStreams}, in.Cookie, in.Body.Proof)
	if err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) openTerminalHTTP(ctx context.Context, in *terminalOpenInput) (*respx.Body[TerminalSession], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, terminalHTTPError(ctx, ErrInvalidArgument)
	}
	if err = m.terminalCSRF(http.MethodPost, in.Cookie, in.Origin); err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	if in.Key != "" {
		out, err := m.openTerminalReceipt(ctx, id, in)
		if err != nil {
			return nil, terminalHTTPError(ctx, err)
		}
		return respx.OK(ctx, out), nil
	}
	out, err := m.OpenTerminal(ctx, authn.TenantIDFromContext(ctx), id, in.Body.ExecutionID, in.Cookie, in.Body.Proof, in.Body.Cols, in.Body.Rows)
	if err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

// The shared C7 receipt commits with the reservation and proof consumption.
// Native setup happens after commit so no business transaction spans tunnel I/O.
// Both the initial request and replay return the immutable OPENING receipt;
// callers read the session resource for its current OPEN/CLOSED lifecycle state.
func (m *Module) openTerminalReceipt(ctx context.Context, machine guid.ID, in *terminalOpenInput) (TerminalSession, error) {
	var out TerminalSession
	if m.idempotency == nil || m.terminal.execution == nil {
		return out, ErrUnavailable
	}
	if in.Body.ExecutionID <= 0 || in.Body.Cols < 1 || in.Body.Cols > 500 || in.Body.Rows < 1 || in.Body.Rows > 200 {
		return out, ErrInvalidArgument
	}
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	digest, err := idempotency.HashRequest(http.MethodPost, "terminal-session.create", map[string]string{"machine_id": machine.String()}, struct {
		ExecutionID guid.ID
		Cols, Rows  int
	}{in.Body.ExecutionID, in.Body.Cols, in.Body.Rows})
	if err != nil {
		return out, err
	}
	tenant := authn.TenantIDFromContext(ctx)
	var row *model.TerminalSession
	var maxStreams int
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result, replay, callErr := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "terminal-session.create", Value: in.Key, RequestHash: digest}, func(tx *gorm.DB) (idempotency.Result, error) {
			if m.ids == nil || m.revokeProof == nil || !m.terminal.recordings.Available() || m.terminal.transport.Available == nil || !m.terminal.transport.Available(tenant, machine) {
				return idempotency.Result{}, ErrUnavailable
			}
			var e error
			row, maxStreams, e = m.reserveTerminalOn(ctx, tx, tenant, machine, in.Body.ExecutionID, in.Cookie, in.Body.Proof)
			if e != nil {
				return idempotency.Result{}, e
			}
			data, e := json.Marshal(terminalView(row))
			return idempotency.Result{StatusCode: http.StatusCreated, Body: data, ResourceKind: "terminal_session", ResourceID: row.ID}, e
		})
		// Even a cached receipt or key conflict is subject to current IAM.
		f, e := m.terminal.execution(ctx, tx, tenant, in.Body.ExecutionID, "terminal_interact")
		if e != nil {
			return e
		}
		if f.MachineID != machine {
			return ErrMachineForbidden
		}
		if callErr != nil {
			return callErr
		}
		if e = json.Unmarshal(result.Body, &out); e != nil {
			return e
		}
		if replay {
			row = nil
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if row != nil {
		// Setup errors close the durable session; the committed receipt remains
		// replayable and GET exposes the failure without spawning another shell.
		_, _ = m.activateTerminal(ctx, row, maxStreams, in.Body.Cols, in.Body.Rows)
	}
	return out, nil
}
func (m *Module) getTerminalHTTP(ctx context.Context, in *TerminalIDInput) (*respx.Body[TerminalSession], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, terminalHTTPError(ctx, ErrInvalidArgument)
	}
	out, err := m.GetTerminal(ctx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) revokeTerminalHTTP(ctx context.Context, in *terminalRevokeInput) (*respx.Body[TerminalSession], error) {
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, terminalHTTPError(ctx, ErrInvalidArgument)
	}
	if err = m.terminalCSRF(http.MethodPost, in.Cookie, in.Origin); err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	tenant := authn.TenantIDFromContext(ctx)
	if _, err = m.GetTerminal(ctx, tenant, id); err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	out, err := m.CloseTerminal(ctx, tenant, id, "USER")
	if err != nil {
		return nil, terminalHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) GetTerminalAccess(ctx context.Context, tenant, machine guid.ID) (TerminalAccessView, error) {
	var out TerminalAccessView
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.controlAuthorityOn(ctx, tx, tenant, "machine_read"); e != nil {
			return e
		}
		if _, e := m.machineOn(ctx, tx, tenant, machine, false); e != nil {
			return e
		}
		q := query.BindTerminalAccess(tx, m.names)
		r, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			out.Version = 1
			out.TunnelBandwidthKbps = 20000
			out.TunnelMaxStreams = 256
			return nil
		}
		if e != nil {
			return e
		}
		out = terminalAccessView(r)
		return nil
	})
	if m.terminal.transport.Available != nil {
		out.TunnelAvailable = m.terminal.transport.Available(tenant, machine)
	}
	return out, err
}

type TerminalAccessOptions struct {
	AllowRemotePreview  *bool
	Enabled             bool
	TunnelBandwidthKbps *int
	TunnelMaxStreams    *int
}

func terminalAccessView(r *model.TerminalAccess) TerminalAccessView {
	return TerminalAccessView{AllowRemotePreview: r.AllowRemotePreview, AllowRemoteTerminal: r.Enabled, Version: r.Version, TunnelBandwidthKbps: r.TunnelBandwidthKbps, TunnelMaxStreams: r.TunnelMaxStreams}
}
func (m *Module) SetTerminalAccess(ctx context.Context, tenant, machine guid.ID, version int, enabled bool, cookie, proof string) (TerminalAccessView, error) {
	return m.SetTerminalAccessOptions(ctx, tenant, machine, version, TerminalAccessOptions{Enabled: enabled}, cookie, proof)
}
func (m *Module) SetTerminalAccessOptions(ctx context.Context, tenant, machine guid.ID, version int, options TerminalAccessOptions, cookie, proof string) (TerminalAccessView, error) {
	var out TerminalAccessView
	if version < 1 || tenant <= 0 || machine <= 0 || (options.TunnelBandwidthKbps != nil && (*options.TunnelBandwidthKbps < 1000 || *options.TunnelBandwidthKbps > 1000000)) || (options.TunnelMaxStreams != nil && (*options.TunnelMaxStreams < 16 || *options.TunnelMaxStreams > 4096)) {
		return out, ErrInvalidArgument
	}
	db, err := m.writer()
	if err != nil {
		return out, err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.terminalAccessAuthorityOn(ctx, tx, tenant); e != nil {
			return e
		}
		q := query.BindTerminalAccess(tx, m.names)
		observed, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			observed = &model.TerminalAccess{MachineID: machine, TenantID: tenant, Version: 1, TunnelBandwidthKbps: 20000, TunnelMaxStreams: 256}
		} else if e != nil {
			return e
		}
		if observed.Version != version {
			return ErrVersionConflict
		}
		if options.Enabled && !observed.Enabled {
			if m.revokeProof == nil {
				return ErrUnavailable
			}
			if cookie == "" || proof == "" {
				return authn.ErrInvalidCredentials
			}
			if e = m.revokeProof(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, proof, "machine_terminal_enable", "machine:"+machine.String()); e != nil {
				return e
			}
		}
		machineRow, e := m.machineOn(ctx, tx, tenant, machine, true)
		if e != nil {
			return e
		}
		if machineRow.State == "REVOKED" {
			return ErrMachineRevoked
		}
		current, e := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine)).Clauses(clause.Locking{Strength: "UPDATE"}).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			current = &model.TerminalAccess{MachineID: machine, TenantID: tenant, Version: 1, TunnelBandwidthKbps: 20000, TunnelMaxStreams: 256}
		} else if e != nil {
			return e
		}
		if current.Version != version || current.Enabled != observed.Enabled {
			return ErrVersionConflict
		}
		before := terminalAccessView(current)
		current.Enabled = options.Enabled
		if options.AllowRemotePreview != nil {
			current.AllowRemotePreview = *options.AllowRemotePreview
		}
		if options.TunnelBandwidthKbps != nil {
			current.TunnelBandwidthKbps = *options.TunnelBandwidthKbps
		}
		if options.TunnelMaxStreams != nil {
			current.TunnelMaxStreams = *options.TunnelMaxStreams
		}
		after := terminalAccessView(current)
		if before == after {
			out = after
			return nil
		}
		current.Version++
		current.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if e = q.WithContext(ctx).Save(current); e != nil {
			return e
		}
		event := audit.NewDomainEvent(ctx, tenant, "fleet.machine_access_changed", "machine", machine)
		event.Detail["before"] = before
		event.Detail["after"] = terminalAccessView(current)
		if e = m.audit(ctx, tx, event); e != nil {
			return e
		}
		_, e = m.events.Publish(ctx, tx, events.Event{TenantID: tenant, Type: "machine.access_changed", Subject: events.Subject{Kind: "machine", ID: machine}, Actor: events.Actor{Kind: "HUMAN", ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: "IAM"}, Data: map[string]any{"machine_id": machine, "allow_remote_terminal": current.Enabled, "allow_remote_preview": current.AllowRemotePreview, "tunnel_bandwidth_kbps": current.TunnelBandwidthKbps, "tunnel_max_streams": current.TunnelMaxStreams, "access_version": current.Version}})
		out = terminalAccessView(current)
		return e
	})
	if err == nil {
		if options.AllowRemotePreview != nil && !*options.AllowRemotePreview && m.previewAccess.StopMachine != nil {
			err = errors.Join(err, m.previewAccess.StopMachine(ctx, tenant, machine, "ACCESS_DISABLED"))
		}
		if !options.Enabled {
			err = errors.Join(err, m.closeMachineTerminals(ctx, tenant, machine, "ACCESS_DISABLED"))
		}
	}
	if m.terminal.transport.Available != nil {
		out.TunnelAvailable = m.terminal.transport.Available(tenant, machine)
	}
	return out, err
}

// terminalAccessAuthorityOn checks an ordinary tenant action, not a data filter.
func (m *Module) terminalAccessAuthorityOn(ctx context.Context, tx *gorm.DB, tenant guid.ID) error {
	actor := authn.PrincipalIDFromContext(ctx)
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return ErrMachineForbidden
	}
	if m == nil || m.principals.Lock == nil || m.principals.ActiveHumanByID == nil || m.terminal.permission == nil {
		return ErrUnavailable
	}
	if err := m.principals.Lock(ctx, tx, actor); err != nil {
		return err
	}
	_, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active {
		return ErrMachineForbidden
	}
	answer, err := m.terminal.permission(ctx, tx, core.AuthorizationExplanationRequest{TenantID: tenant, PrincipalID: actor, Permission: "machine_access_manage"})
	if err != nil {
		return err
	}
	if !answer.Allowed {
		return ErrMachineForbidden
	}
	return nil
}
