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

type verificationPullInput struct{ Body RunnerFence }
type verificationReceiptInput struct {
	TaskID string `path:"task_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body   struct {
		Fence       RunnerFence             `json:"fence"`
		Nonce       string                  `json:"nonce" minLength:"64" maxLength:"64"`
		Observation VerificationObservation `json:"observation"`
	}
}

func (m *Module) registerVerificationHTTP(api huma.API) error {
	m.registerTemplateVerifierHTTP(api)
	op := func(id, path, summary string) huma.Operation {
		operation := huma.Operation{OperationID: id, Method: http.MethodPost, Path: path, Summary: summary, Tags: []string{"executions"}, Errors: []int{401, 403, 404, 409, 422, 503}}
		if id == "execution.verification.receipt" {
			operation.MaxBodyBytes = 12 << 20
		}
		return operation
	}
	authz.RegisterAuthenticated(m.registrar, api, op("execution.verification.pull", "/runner/verification-pulls", "Lease a platform verification command"), m.verificationPullHTTP)
	authz.RegisterAuthenticated(m.registrar, api, op("execution.verification.receipt", "/runner/verifications/{task_id}/receipts", "Record a fenced platform verification observation"), m.verificationCompleteHTTP)
	return authz.RegisterRequestContract(api, "/runner/verifications/{task_id}/receipts")
}
func (m *Module) verificationPullHTTP(ctx context.Context, in *verificationPullInput) (*respx.Body[[]VerificationPlan], error) {
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	bound, bindErr := m.machines.BindRunnerContext(ctx, in.Body.MachineID)
	if bindErr != nil {
		return nil, httpError(ctx, bindErr)
	}
	ctx = bound
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	var out []VerificationPlan
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.pullVerificationOn(ctx, tx, authn.TenantIDFromContext(ctx), in.Body)
		return e
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) verificationCompleteHTTP(ctx context.Context, in *verificationReceiptInput) (*respx.Body[bool], error) {
	if m.machines.BindRunnerContext == nil {
		return nil, httpError(ctx, ErrUnavailable)
	}
	bound, bindErr := m.machines.BindRunnerContext(ctx, in.Body.Fence.MachineID)
	if bindErr != nil {
		return nil, httpError(ctx, bindErr)
	}
	ctx = bound
	id, err := guid.RequireID(in.TaskID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalidArgument)
	}
	db, err := m.writer()
	if err != nil {
		return nil, httpError(ctx, err)
	}
	observation, err := m.prepareVisualEvidence(ctx, authn.TenantIDFromContext(ctx), id, in.Body.Fence, in.Body.Nonce, in.Body.Observation)
	if err != nil {
		return nil, httpError(ctx, err)
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return m.completeVerificationOn(ctx, tx, authn.TenantIDFromContext(ctx), id, in.Body.Fence, in.Body.Nonce, observation)
	})
	if err != nil {
		return nil, httpError(ctx, err)
	}
	return respx.OK(ctx, true), nil
}
