package fleet

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type resetReportInput struct {
	MachineInput
	ResetID string `path:"reset_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body    struct {
		SessionID  guid.ID `json:"session_id"`
		ConnEpoch  int64   `json:"conn_epoch" minimum:"1"`
		State      string  `json:"state" enum:"DONE,FAILED"`
		ReasonCode string  `json:"reason_code" enum:",UNSUPPORTED_AGENT_RUNTIME,RESET_FAILED,OUTCOME_UNKNOWN"`
	}
}

func (m *Module) registerResetHTTP(api huma.API) error {
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.agent-reset.pull", Method: http.MethodPost, Path: "/machines/{machine_id}/agent-reset-pulls", Summary: "Pull one fenced agent reset", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.pullResetHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "runner.agent-reset.report", Method: http.MethodPut, Path: "/machines/{machine_id}/agent-resets/{reset_id}/result", Summary: "Report a fenced agent reset receipt", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.reportResetHTTP)
	for _, path := range []string{"/machines/{machine_id}/agent-reset-pulls", "/machines/{machine_id}/agent-resets/{reset_id}/result"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) pullResetHTTP(ctx context.Context, in *heartbeatInput) (*respx.Body[[]ResetCommandView], error) {
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
	var out []ResetCommandView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.pullAgentResetOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.SessionID, in.Body.ConnEpoch)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}

func (m *Module) reportResetHTTP(ctx context.Context, in *resetReportInput) (*respx.Body[ResetCommandView], error) {
	machine, err := guid.RequireID(in.MachineID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	id, err := guid.RequireID(in.ResetID)
	if err != nil {
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
	var out ResetCommandView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.reportAgentResetOn(ctx, tx, authn.TenantIDFromContext(ctx), machine, id, in.Body.SessionID, in.Body.ConnEpoch, in.Body.State, in.Body.ReasonCode)
		return e
	})
	if err != nil {
		return nil, fleetHTTPError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
