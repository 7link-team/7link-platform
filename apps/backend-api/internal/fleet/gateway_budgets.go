package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const authenticationModule = iamauthn.Name

// GatewayBudgetConnector is bound by the project budget owner after fleet
// activation. Missing binding fails closed for project-linked requests.
type GatewayBudgetConnector struct {
	Bind func(GatewayBudgetConsumer) error
}
type GatewayBudgetConsumer struct {
	ReserveOn func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, int64, *int64) error
	SettleOn  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error
}

func (m *Module) provideGatewayBudgets(s *module.Services) error {
	return module.Provide[GatewayBudgetConnector](s, GatewayBudgetConnector{Bind: func(p GatewayBudgetConsumer) error {
		if p.ReserveOn == nil || p.SettleOn == nil {
			return ErrUnavailable
		}
		m.gatewayBudgets = p
		return nil
	}})
}

func (m *Module) injectGatewayBudgets(s *module.Services) error {
	var err error
	if m.revokeProof, err = module.Resolve[authn.ReauthenticationConsumer](s); err != nil {
		return err
	}
	if m.revokeAccount, err = module.Resolve[identity.ServiceAccountDisabler](s); err != nil {
		return err
	}
	m.revokeBrowser, err = module.Resolve[authn.BrowserSessionSecurity](s)
	return err
}

// RevokeMachineOn consumes fresh browser authority and disables the IAM account
// in the same transaction as machine/session/slot fencing and audit/outbox.
func (m *Module) RevokeMachineOn(ctx context.Context, tx *gorm.DB, tenant, machine guid.ID, version int, confirmName, reason, cookie, proof string) (MachineFact, error) {
	if err := fleetTransaction(tx, tenant, machine); err != nil {
		return MachineFact{}, err
	}
	if version < 1 || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 2000 {
		return MachineFact{}, ErrInvalidArgument
	}
	if m == nil || m.revokeProof == nil || m.revokeAccount == nil {
		return MachineFact{}, ErrUnavailable
	}
	if cookie == "" || proof == "" {
		return MachineFact{}, ErrMachineForbidden
	}
	if err := m.controlAuthorityOn(ctx, tx, tenant, "machine_revoke"); err != nil {
		return MachineFact{}, err
	}
	if err := m.revokeProof(ctx, tx, authn.PrincipalIDFromContext(ctx), cookie, proof, iamauthn.ReauthenticationMachineRevoke, machine.String()); err != nil {
		if errors.Is(err, authn.ErrInvalidCredentials) {
			return MachineFact{}, ErrMachineForbidden
		}
		return MachineFact{}, err
	}
	snapshot, err := m.machineOn(ctx, tx, tenant, machine, false)
	if err != nil {
		return MachineFact{}, err
	}
	// Principal before machine follows the runner's lock order.
	if err = m.principals.Lock(ctx, tx, snapshot.PrincipalID); err != nil {
		return MachineFact{}, err
	}
	if err = m.revokeAccount(ctx, tx, tenant, snapshot.PrincipalID); err != nil {
		return MachineFact{}, err
	}
	row, err := m.machineOn(ctx, tx, tenant, machine, true)
	if err != nil {
		return MachineFact{}, err
	}
	if row.PrincipalID != snapshot.PrincipalID || row.Version != version {
		return MachineFact{}, ErrVersionConflict
	}
	if row.Name != confirmName {
		return MachineFact{}, ErrInvalidArgument
	}
	if row.State == "REVOKED" {
		return MachineFact{}, ErrMachineRevoked
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	q := query.BindMachines(tx, m.names)
	if _, err = q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(machine)).Updates(map[string]any{"state": "REVOKED", "name_key": nil, "current_session_id": nil, "conn_epoch": row.ConnEpoch + 1, "version": row.Version + 1, "updated_at": now}); err != nil {
		return MachineFact{}, err
	}
	sessions := query.BindSessions(tx, m.names)
	if _, err = sessions.WithContext(ctx).Where(sessions.TenantID.Eq(tenant), sessions.MachineID.Eq(machine), sessions.State.Eq("ACTIVE")).Updates(map[string]any{"state": "CLOSED", "closed_at": now}); err != nil {
		return MachineFact{}, err
	}
	slots := query.BindSlots(tx, m.names)
	if _, err = slots.WithContext(ctx).Where(slots.TenantID.Eq(tenant), slots.MachineID.Eq(machine), slots.ActiveSlot.IsNotNull()).Updates(map[string]any{"active_slot": nil, "released_at": now}); err != nil {
		return MachineFact{}, err
	}
	credentials := query.BindCredentials(tx, m.names)
	if _, err = credentials.WithContext(ctx).Where(credentials.TenantID.Eq(tenant), credentials.MachineID.Eq(machine), credentials.State.Neq("REVOKED")).Updates(map[string]any{"state": "REVOKED", "reason_code": "MACHINE_REVOKED", "updated_at": now}); err != nil {
		return MachineFact{}, err
	}
	if err = m.machineEventOn(humanMachineContext(ctx), tx, tenant, machine, "machine.state_changed", row.Version+1, map[string]any{"state": "REVOKED", "reason": reason, "reason_code": "MACHINE_REVOKED"}); err != nil {
		return MachineFact{}, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "fleet.machine_revoked", "machine", machine)
	event.Detail["reason"] = reason
	if err = m.audit(ctx, tx, event); err != nil {
		return MachineFact{}, err
	}
	return m.MachineByIDOn(ctx, tx, tenant, machine)
}

type machineRevocationInput struct {
	MachineInput
	IfMatch        string `header:"If-Match" required:"true"`
	Cookie         string `header:"Cookie"`
	Origin         string `header:"Origin"`
	IdempotencyKey string `header:"Idempotency-Key"`
	Body           struct {
		ConfirmName string `json:"confirm_name" minLength:"1" maxLength:"100"`
		Reason      string `json:"reason" minLength:"1" maxLength:"2000"`
		Proof       string `json:"reauth_proof" maxLength:"256"`
	}
}

func (m *Module) registerGatewayBudgetsHTTP(api huma.API) error {
	path := "/machines/{machine_id}/revocation"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.revocation.create", Method: http.MethodPost, Path: path, DefaultStatus: http.StatusCreated, Summary: "Revoke a machine using fresh browser reauthentication", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "revoke", FilterData: true}, m.revokeMachineHTTP)
	return authz.RegisterRequestContract(api, path)
}

func (m *Module) revokeMachineHTTP(ctx context.Context, in *machineRevocationInput) (*respx.Body[machineView], error) {
	id, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	version, err := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || version < 1 {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	if m.revokeBrowser.ValidateCSRF == nil {
		return nil, fleetHTTPError(ctx, ErrUnavailable)
	}
	if err = m.revokeBrowser.ValidateCSRF(http.MethodPost, in.Origin, in.Cookie); err != nil {
		return nil, fleetHTTPError(ctx, ErrMachineForbidden)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out machineView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.controlAuthorityOn(ctx, tx, authn.TenantIDFromContext(ctx), "machine_revoke"); err != nil {
			return err
		}
		run := func(tx *gorm.DB) error {
			if _, e := m.RevokeMachineOn(ctx, tx, authn.TenantIDFromContext(ctx), id, version, in.Body.ConfirmName, in.Body.Reason, in.Cookie, in.Body.Proof); e != nil {
				return e
			}
			var e error
			out, e = m.machineViewOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
			return e
		}
		if in.IdempotencyKey == "" {
			return run(tx)
		}
		if m.idempotency == nil {
			return ErrUnavailable
		}
		hash, e := idempotency.HashRequest(http.MethodPost, "machine.revocation.create", nil, struct {
			Machine             guid.ID
			Version             int
			Name, Reason, Proof string
		}{id, version, in.Body.ConfirmName, in.Body.Reason, in.Body.Proof})
		if e != nil {
			return e
		}
		result, _, e := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: authn.TenantIDFromContext(ctx), SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "machine.revocation.create", Value: in.IdempotencyKey, RequestHash: hash}, func(tx *gorm.DB) (idempotency.Result, error) {
			if e := run(tx); e != nil {
				return idempotency.Result{}, e
			}
			body, e := json.Marshal(out)
			return idempotency.Result{StatusCode: http.StatusCreated, Body: body}, e
		})
		if e != nil {
			if errors.Is(e, idempotency.ErrInvalidKey) || errors.Is(e, idempotency.ErrKeyReused) {
				return ErrInvalidArgument
			}
			if errors.Is(e, idempotency.ErrInProgress) {
				return ErrVersionConflict
			}
			return e
		}
		return json.Unmarshal(result.Body, &out)
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

// RotationNotificationRequester reads the rotation initiator without publishing
// fleet storage. Callers check current IAM visibility before exposing content.
type RotationNotificationRequester func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error)

func (m *Module) rotationNotificationRequester(ctx context.Context, tx *gorm.DB, tenant, machine, rotation guid.ID) (guid.ID, error) {
	if !validRotationNotificationInput(tx, tenant, machine, rotation) {
		return 0, ErrInvalidArgument
	}
	q := query.BindRotations(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(rotation)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return r.RequestedBy, nil
}
func validRotationNotificationInput(tx *gorm.DB, tenant, machine, rotation guid.ID) bool {
	return tx != nil && tenant > 0 && machine > 0 && rotation > 0
}

// RealtimeTopics retains fleet's existing machine_read policy. It only grants
// an invalidation topic if the corresponding current row is visible.
type RealtimeTopics struct {
	Authorize func(context.Context, guid.ID, guid.ID, guid.ID, bool) (bool, error)
}

func (m *Module) authorizeRealtimeMachine(ctx context.Context, tenant, principal, id guid.ID, collection bool) (bool, error) {
	if tenant <= 0 || principal <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) != principal || (!collection && id <= 0) {
		return false, nil
	}
	if m == nil || m.policy == nil {
		return false, ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return false, err
	}
	allowed := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, err := m.policy(ctx, tx, tenant, "machine_read", principal)
		if err != nil {
			return err
		}
		if !c.AllowAll {
			return nil
		}
		q := query.BindMachines(tx, m.names)
		scope := q.WithContext(ctx).Where(q.TenantID.Eq(tenant))
		if !collection {
			scope = scope.Where(q.ID.Eq(id))
		}
		_, err = scope.First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		allowed = err == nil
		return err
	})
	return allowed, err
}

// RotationNotificationFact exposes only current rotation state for a delayed
// reminder. Fleet remains the storage owner; no credential material is returned.
type RotationNotificationFact struct {
	State       string
	RequestedBy guid.ID
	RetireOldAt time.Time
}
type RotationNotificationFacts func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (RotationNotificationFact, bool, error)

func (m *Module) rotationNotificationFacts(ctx context.Context, tx *gorm.DB, tenant, machine, rotation guid.ID) (RotationNotificationFact, bool, error) {
	if !validRotationNotificationInput(tx, tenant, machine, rotation) {
		return RotationNotificationFact{}, false, ErrInvalidArgument
	}
	q := query.BindRotations(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine), q.ID.Eq(rotation)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RotationNotificationFact{}, false, nil
	}
	if err != nil {
		return RotationNotificationFact{}, false, err
	}
	mq := query.BindMachines(tx, m.names)
	machineRow, err := mq.WithContext(ctx).Where(mq.TenantID.Eq(tenant), mq.ID.Eq(machine)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RotationNotificationFact{}, false, nil
	}
	if err != nil {
		return RotationNotificationFact{}, false, err
	}
	if machineRow.State == "REVOKED" {
		return RotationNotificationFact{}, false, nil
	}
	return RotationNotificationFact{State: r.State, RequestedBy: r.RequestedBy, RetireOldAt: r.RetireOldAt}, true, nil
}
