package execution

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

type ApprovalInput struct {
	ExecutionInput
	ApprovalID string `path:"approval_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type approvalRequestInput struct {
	ExecutionInput
	Body struct {
		Fence   RunnerFence     `json:"fence"`
		Request ApprovalRequest `json:"request"`
	}
}
type approvalPollInput struct {
	ApprovalInput
	Body struct {
		Fence         RunnerFence `json:"fence"`
		DispatchEpoch int64       `json:"dispatch_epoch" minimum:"1"`
	}
}
type approvalDecisionInput struct {
	ApprovalInput
	Body struct {
		State  string `json:"state" enum:"ALLOW,DENY"`
		Reason string `json:"reason" minLength:"1" maxLength:"2000"`
	}
}

func (m *Module) registerApprovalHTTP(api huma.API) error {
	op := func(id, method, path, summary string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}
	}
	authz.RegisterAuthenticated(m.registrar, api, op("execution.approval.request", http.MethodPost, "/executions/{execution_id}/approval-requests", "Request a fenced Human runtime permission"), m.requestApprovalHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("execution.approval.poll", http.MethodPost, "/executions/{execution_id}/approval-requests/{approval_id}/polls", "Read the current fenced runtime permission decision"), m.pollApprovalHTTP)
	authz.RegisterOwned(m.registrar, api, op("execution.approval.decide", http.MethodPut, "/executions/{execution_id}/approval-requests/{approval_id}/decision", "Record an explicit Human runtime permission decision"), "execution_id", m.owner, authz.Guard{Resource: "interaction", Verb: "respond", FilterData: true}, m.decideApprovalHTTP)
	for _, path := range []string{"/executions/{execution_id}/approval-requests", "/executions/{execution_id}/approval-requests/{approval_id}/polls", "/executions/{execution_id}/approval-requests/{approval_id}/decision"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) requestApprovalHTTP(ctx context.Context, in *approvalRequestInput) (*respx.Body[ApprovalView], error) {
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	bound, e := m.machines.BindRunnerContext(ctx, in.Body.Fence.MachineID)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	ctx = bound
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.approvalHTTP(ctx, func(tx *gorm.DB) (ApprovalView, error) {
		return m.RequestApprovalOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.Fence, in.Body.Request)
	})
}
func (m *Module) pollApprovalHTTP(ctx context.Context, in *approvalPollInput) (*respx.Body[ApprovalView], error) {
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	bound, e := m.machines.BindRunnerContext(ctx, in.Body.Fence.MachineID)
	if e != nil {
		return nil, httpError(ctx, e)
	}
	ctx = bound
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	a, err := guid.RequireID(in.ApprovalID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.approvalHTTP(ctx, func(tx *gorm.DB) (ApprovalView, error) {
		return m.ReadRunnerApprovalOn(ctx, tx, authn.TenantIDFromContext(ctx), id, a, in.Body.Fence, in.Body.DispatchEpoch)
	})
}
func (m *Module) decideApprovalHTTP(ctx context.Context, in *approvalDecisionInput) (*respx.Body[ApprovalView], error) {
	id, err := guid.RequireID(in.ExecutionID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	a, err := guid.RequireID(in.ApprovalID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	return m.approvalHTTP(ctx, func(tx *gorm.DB) (ApprovalView, error) {
		return m.DecideApprovalOn(ctx, tx, authn.TenantIDFromContext(ctx), id, a, in.Body.State, in.Body.Reason)
	})
}
func (m *Module) approvalHTTP(ctx context.Context, run func(*gorm.DB) (ApprovalView, error)) (*respx.Body[ApprovalView], error) {
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out ApprovalView
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { var e error; out, e = run(tx); return e })
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
