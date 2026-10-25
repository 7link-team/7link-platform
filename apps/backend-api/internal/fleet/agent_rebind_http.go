package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
	"strconv"
	"strings"
)

type RebindAgentPathInput struct {
	AgentPrincipalID string `path:"agent_principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type rebindAgentInput struct {
	RebindAgentPathInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	IfMatch        string `header:"If-Match" required:"true"`
	Body           AgentMachineBindingInput
}
type agentMachineCommandReportInput struct {
	MachineInput
	CommandID string `path:"command_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body      struct {
		SessionID  guid.ID `json:"session_id"`
		ConnEpoch  int64   `json:"conn_epoch" minimum:"1"`
		State      string  `json:"state" enum:"DONE,FAILED"`
		ReasonCode string  `json:"reason_code,omitempty" enum:"CREDENTIAL_STORE_FAILED,CLEANUP_FAILED,INVALID_PRIVATE_DIRECTORY"`
	}
}

func (m *Module) registerAgentRebindHTTP(api huma.API) error {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"agents"}, Errors: []int{400, 401, 403, 404, 409, 412, 422, 500, 503}}
	}
	authz.Register(m.registrar, api, op("agent.machine-binding.get", "GET", "/agents/{agent_principal_id}/machine-binding", "Read current Agent binding and receipt-backed delivery state"), authz.Guard{Resource: "agent", Verb: "read", FilterData: true}, m.getAgentBindingHTTP)
	authz.Register(m.registrar, api, op("agent.machine-binding.update", "PUT", "/agents/{agent_principal_id}/machine-binding", "Rebind a stopped Agent and rotate credentials atomically"), authz.Guard{Resource: "agent", Verb: "manage", FilterData: true}, m.rebindAgentHTTP)
	authz.Register(m.registrar, api, op("agent.machine-command.list", "GET", "/agents/{agent_principal_id}/machine-commands", "Read actual Agent machine command receipts"), authz.Guard{Resource: "agent", Verb: "read", FilterData: true}, m.listAgentMachineCommandsHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("runner.agent-machine-command.pull", "POST", "/machines/{machine_id}/agent-command-pulls", "Pull one fenced private Agent machine command"), m.pullAgentMachineCommandsHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("runner.agent-machine-command.report", "PUT", "/machines/{machine_id}/agent-commands/{command_id}/result", "Acknowledge actual Agent machine command completion"), m.reportAgentMachineCommandHTTP)
	for _, path := range []string{"/tenants/{tenant_id}/agents/{agent_principal_id}/machine-binding", "/tenants/{tenant_id}/agents/{agent_principal_id}/machine-commands", "/machines/{machine_id}/agent-command-pulls", "/machines/{machine_id}/agent-commands/{command_id}/result"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) getAgentBindingHTTP(ctx context.Context, in *RebindAgentPathInput) (*respx.Body[AgentMachineBinding], error) {
	id, err := guid.RequireID(in.AgentPrincipalID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out AgentMachineBinding
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.controlAuthorityOn(ctx, tx, authn.TenantIDFromContext(ctx), "agent_read"); err != nil {
			return err
		}
		q := query.BindAgents(tx, m.names)
		a, e := q.WithContext(ctx).Where(q.TenantID.Eq(authn.TenantIDFromContext(ctx)), q.PrincipalID.Eq(id)).First()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return ErrMachineNotFound
		}
		if e != nil {
			return e
		}
		out = bindingView(a)
		if a.MachineID != nil {
			var allowlist []string
			if json.Unmarshal([]byte(a.MCPAllowlist), &allowlist) != nil {
				return ErrUnavailable
			}
			stale, e := m.rebindCapabilitiesOn(ctx, tx, authn.TenantIDFromContext(ctx), AgentMachineBindingInput{MachineID: *a.MachineID, RuntimeKind: a.RuntimeKind, ModelID: a.ModelID, ReasoningEffort: a.ReasoningEffort}, allowlist)
			if e != nil && !errors.Is(e, ErrAgentRebindMCP) && !errors.Is(e, ErrAgentRebindEffort) {
				return e
			}
			out.CapabilityStale = stale
		}
		return nil
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) listAgentMachineCommandsHTTP(ctx context.Context, in *RebindAgentPathInput) (*respx.Body[[]AgentMachineCommandView], error) {
	id, err := guid.RequireID(in.AgentPrincipalID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	out := []AgentMachineCommandView{}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tenant := authn.TenantIDFromContext(ctx)
		if err := m.controlAuthorityOn(ctx, tx, tenant, "agent_read"); err != nil {
			return err
		}
		var rows []model.AgentMachineCommand
		if err := m.agentCommandStore(tx.WithContext(ctx)).Where(map[string]any{"tenant_id": tenant, "agent_principal_id": id}).Order("id DESC").Limit(100).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			out = append(out, machineCommandView(&row))
		}
		return nil
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) rebindAgentHTTP(ctx context.Context, in *rebindAgentInput) (*respx.Body[AgentMachineBinding], error) {
	id, err := guid.RequireID(in.AgentPrincipalID)
	version, e := strconv.Atoi(strings.Trim(in.IfMatch, "\""))
	if err != nil || e != nil || version < 1 {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out AgentMachineBinding
	tenant := authn.TenantIDFromContext(ctx)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := m.rebindAuthorityOn(ctx, tx, tenant, id, in.Body.MachineID); err != nil {
			return err
		}
		if m.idempotency == nil {
			return ErrUnavailable
		}
		digest, e := idempotency.HashRequest(http.MethodPut, "agent.machine-binding.update", map[string]string{"agent_principal_id": id.String(), "version": strconv.Itoa(version)}, in.Body)
		if e != nil {
			return e
		}
		result, _, e := m.idempotency.Do(ctx, tx, idempotency.Key{TenantID: tenant, SubjectKind: "PRINCIPAL", SubjectID: authn.PrincipalIDFromContext(ctx), Operation: "agent.machine-binding.update:" + id.String(), Value: in.IdempotencyKey, RequestHash: digest}, func(tx *gorm.DB) (idempotency.Result, error) {
			v, e := m.RebindAgentOn(ctx, tx, tenant, id, version, in.Body)
			if e != nil {
				return idempotency.Result{}, e
			}
			body, e := json.Marshal(v)
			return idempotency.Result{StatusCode: 200, Body: body}, e
		})
		if e != nil {
			return e
		}
		return json.Unmarshal(result.Body, &out)
	})
	if errors.Is(err, idempotency.ErrKeyReused) || errors.Is(err, idempotency.ErrInProgress) {
		err = ErrVersionConflict
	}
	if errors.Is(err, idempotency.ErrInvalidKey) {
		err = ErrInvalidArgument
	}
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) pullAgentMachineCommandsHTTP(ctx context.Context, in *heartbeatInput) (*privateFleetBody[[]AgentMachineDelivery], error) {
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
	var out []AgentMachineDelivery
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.pullAgentMachineCommandsOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.SessionID, in.Body.ConnEpoch)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return &privateFleetBody[[]AgentMachineDelivery]{CacheControl: "no-store", Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) reportAgentMachineCommandHTTP(ctx context.Context, in *agentMachineCommandReportInput) (*respx.Body[AgentMachineCommandView], error) {
	machine, err := guid.RequireID(in.MachineID)
	id, e := guid.RequireID(in.CommandID)
	if err != nil || e != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, err = m.BindRunnerContext(ctx, machine)
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	db, err := m.writer()
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	var out AgentMachineCommandView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.reportAgentMachineCommandOn(ctx, tx, authn.TenantIDFromContext(ctx), machine, id, in.Body.SessionID, in.Body.ConnEpoch, in.Body.State, in.Body.ReasonCode)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
