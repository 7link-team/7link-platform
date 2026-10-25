package delivery

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type verificationStartInput struct {
	ResultVersionInput
	Body verificationStartBody
}
type verificationCollectInput struct{ ResultVersionInput }
type verificationOverrideInput struct {
	ResultVersionInput
	Cookie string `header:"Cookie"`
	Body   verificationOverrideBody
}

func (m *Module) registerVerificationHTTP(api huma.API) error {
	errors := []int{400, 401, 403, 404, 409, 422, 500, 503}
	owner := authz.ResourceOwnerResolver(m.workOwner)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.verification.start", Method: http.MethodPost, Path: workResultPath + "/verifications", Summary: "Queue a frozen exact-result platform verification", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, authz.Guard{Resource: "verification", Verb: "run"}, m.startVerificationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.verification.collect", Method: http.MethodPost, Path: workResultPath + "/verification-receipts", Summary: "Collect authenticated platform verification receipts", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, authz.Guard{Resource: "verification", Verb: "run"}, m.collectVerificationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "result.verification.override", Method: http.MethodPost, Path: workResultPath + "/verification-overrides", Summary: "Record a separate Human exception without changing the verdict", Tags: []string{"work-items"}, DefaultStatus: 201, Errors: errors}, "work_item_id", owner, authz.Guard{Resource: "decision", Verb: "force_valid"}, m.overrideVerificationHTTP)
	for _, suffix := range []string{"/verifications", "/verification-receipts", "/verification-overrides"} {
		if err := authz.RegisterRequestContract(api, workResultPath+suffix); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) startVerificationHTTP(ctx context.Context, in *verificationStartInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	id, err := guid.RequireID(in.ResultID)
	if err != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, "", "result.verification.start", "/verifications", "verification_run", "", in.Body, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.startVerificationOn(ctx, tx, t, p, w, id, a, o, v, in.Body)
	})
}
func (m *Module) collectVerificationHTTP(ctx context.Context, in *verificationCollectInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	id, err := guid.RequireID(in.ResultID)
	if err != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, "", "result.verification.collect", "/verification-receipts", "verification_run", "", struct{}{}, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.collectVerificationOn(ctx, tx, t, p, w, id, a, o, v)
	})
}
func (m *Module) overrideVerificationHTTP(ctx context.Context, in *verificationOverrideInput) (*deliveryCommandOutput[resultCommandReceipt], error) {
	id, err := guid.RequireID(in.ResultID)
	if err != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	return m.resultCommandHTTP(ctx, in.ResultMutationInput, in.ResultID, "", "result.verification.override", "/verification-overrides", "work_item_read", "decision_force_valid", in.Body, func(tx *gorm.DB, t, p, w, a, o guid.ID, v int) (ResultView, error) {
		return m.overrideVerificationOn(ctx, tx, t, p, w, id, a, o, v, in.Cookie, in.Body)
	})
}
