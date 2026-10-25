package delivery

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const commentDefectPath = "/comment-threads/{thread_id}/defects"

type commentDefectInput struct {
	ThreadID       string `path:"thread_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	IdempotencyKey string `header:"Idempotency-Key" required:"true"`
	Body           CommentDefectCreate
}

func commentDefectHTTPError(ctx context.Context, err error) error {
	var conversion *commentDefectError
	if errors.As(err, &conversion) {
		code := 422126063
		if conversion.Reason == "DEFECT_FIELDS_REQUIRED" {
			code = 422126020
		}
		data := map[string]any{"reason_code": conversion.Reason}
		if conversion.TargetID > 0 {
			data["target_id"] = conversion.TargetID.String()
		}
		return respx.ErrStatus(ctx, 422, code, "delivery.comment_conversion", data)
	}
	if errors.Is(err, ErrWorkSeenSeqRequired) {
		return respx.ErrStatus(ctx, 422, 422126062, "delivery.comment_context_required", map[string]any{"reason_code": "SEEN_SEQ_REQUIRED"})
	}
	var interrupted *WorkWriteInterrupted
	if errors.As(err, &interrupted) {
		return respx.ErrStatus(ctx, 409, 409126061, "delivery.comment_interrupted", interrupted)
	}
	return workHTTPError(ctx, err)
}
func (m *Module) commentThreadOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	if m.runtime == nil || m.runtime.Database() == nil || m.artifactComments.ScopeOn == nil {
		return authz.ResourceOwner{}, false, ErrUnavailable
	}
	var scope ArtifactCommentScope
	err := m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		scope, err = m.artifactComments.ScopeOn(ctx, tx, authn.TenantIDFromContext(ctx), id)
		return err
	})
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{ScopeType: "entity", TenantID: authn.TenantIDFromContext(ctx), EntityID: scope.ProjectID}, true, nil
}
func (m *Module) registerCommentDefectHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "defect_from_comment.create", Method: http.MethodPost, Path: commentDefectPath, Summary: "Create a defect from an internal immutable artifact comment", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, "thread_id", m.commentThreadOwner, authz.Guard{Resource: "work_item", Verb: "write"}, m.createCommentDefectHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "work_item.comment-source", Method: http.MethodGet, Path: workPath + "/{work_item_id}/comment-source", Summary: "Read frozen artifact comment provenance", Tags: []string{"entities"}, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "work_item", Verb: "read"}, m.commentDefectProvenanceHTTP)
	return authz.RegisterRequestContract(api, commentDefectPath)
}
func (m *Module) createCommentDefectHTTP(ctx context.Context, in *commentDefectInput) (*deliveryCommandOutput[CommentDefectView], error) {
	ctx = respx.WithNext(ctx, respx.NextStep{Operation: "artifact_comment.list", Reason: "Review the immutable source comment and its conversion target."})
	id, err := guid.RequireID(in.ThreadID)
	if err != nil {
		return nil, commentDefectHTTPError(ctx, ErrInvalidArgument)
	}
	tenant := authn.TenantIDFromContext(ctx)
	return deliveryCommand(ctx, m, "work_item", commentDefectHTTPError, "defect_from_comment.create", http.MethodPost, commentDefectPath, in.IdempotencyKey, 201, in.Body, map[string]string{"thread_id": id.String()}, func(tx *gorm.DB) (CommentDefectView, guid.ID, error) {
		out, err := m.createCommentDefectOn(ctx, tx, tenant, id, in.Body)
		return out, out.ID, err
	}, func(tx *gorm.DB) error { _, _, _, err := m.commentDefectAuthorityOn(ctx, tx, tenant, id); return err })
}
func (m *Module) commentDefectProvenanceHTTP(ctx context.Context, in *workGetInput) (*respx.Body[CommentDefectView], error) {
	project, e := guid.RequireID(in.EntityID)
	if e != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	work, e := guid.RequireID(in.WorkItemID)
	if e != nil {
		return nil, workHTTPError(ctx, ErrInvalidArgument)
	}
	if m.runtime == nil || m.runtime.Database() == nil {
		return nil, workHTTPError(ctx, ErrUnavailable)
	}
	var out CommentDefectView
	e = m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = m.commentDefectProvenanceOn(ctx, tx, authn.TenantIDFromContext(ctx), project, work)
		return err
	})
	if e != nil {
		return nil, workHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
