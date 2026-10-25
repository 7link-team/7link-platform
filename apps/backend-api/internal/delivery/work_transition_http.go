package delivery

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const workTransitionPath = "/work-items/{work_item_id}/transitions"

type workTransitionInput struct {
	WorkItemID     string `path:"work_item_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           workTransitionBody
}

func workTransitionHTTPError(ctx context.Context, err error) error {
	var interrupted *WorkWriteInterrupted
	if errors.Is(err, ErrWorkSeenSeqRequired) {
		return respx.ErrStatus(ctx, 422, 126062, "delivery.seen_seq_required", nil)
	}
	if errors.As(err, &interrupted) {
		return respx.ErrStatus(ctx, 409, 126061, "delivery.write_interrupted", interrupted)
	}
	return workHTTPError(ctx, err)
}

func (m *Module) registerWorkTransitionHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item_transition.apply", Method: "POST", Path: workTransitionPath, Summary: "Apply an authorized workflow display transition", Tags: []string{"work-items"}, DefaultStatus: 200, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "work_item_id", authz.ResourceOwnerResolver(m.workOwner), authz.Guard{Resource: "work_item", Verb: "write"}, m.applyWorkTransitionHTTP)
	return authz.RegisterRequestContract(api, workTransitionPath)
}

func (m *Module) applyWorkTransitionHTTP(ctx context.Context, in *workTransitionInput) (*deliveryCommandOutput[workTransitionView], error) {
	ctx = respx.WithNext(ctx, respx.NextStep{Operation: "inbox_events.list", Reason: "READ_NEWER_EVENTS"})
	id, err := guid.RequireID(in.WorkItemID)
	if err != nil {
		return nil, workTransitionHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, project := authn.TenantIDFromContext(ctx), authn.EntityIDFromContext(ctx)
	// The protocol cursor can change on same-key refresh; every business input
	// (including fields and comment) remains part of the canonical request hash.
	hashBody := in.Body
	hashBody.SeenSeq = nil
	return deliveryCommand(ctx, m, "work_item", workTransitionHTTPError, "work_item_transition.apply", "POST", workTransitionPath, in.IdempotencyKey, 200, hashBody, map[string]string{"work_item_id": id.String()}, func(tx *gorm.DB) (workTransitionView, guid.ID, error) {
		out, err := m.applyWorkTransitionOn(ctx, tx, tenant, project, id, in.Body)
		return out, id, err
	}, func(tx *gorm.DB) error {
		_, _, err := m.transitionAuthorityOn(ctx, tx, tenant, project, id)
		return err
	})
}
