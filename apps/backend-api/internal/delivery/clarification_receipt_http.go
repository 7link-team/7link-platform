package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
	"net/http"
)

const clarificationHandledPath = "/entities/{entity_id}/project/clarifications/{clarification_id}/handled"

type clarificationHandledInput struct {
	EntityID        string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ClarificationID string `path:"clarification_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key             string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body            struct {
		ReplyVersion int `json:"reply_version" minimum:"1"`
	}
}

func (m *Module) registerClarificationReceiptHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clarification.handle-reply", Method: http.MethodPost, Path: clarificationHandledPath, Summary: "Record handling of an exact clarification reply", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "intake", Verb: "read"}, m.handleClarificationHTTP)
}
func (m *Module) handleClarificationHTTP(ctx context.Context, in *clarificationHandledInput) (*deliveryCommandOutput[clarificationReceiptView], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.ClarificationID)
	if e != nil || e2 != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	// Reauthorize source ownership on replay, even after idempotency has stored a receipt.
	authorize := func(tx *gorm.DB) error {
		_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "intake_read")
		if err != nil {
			return err
		}
		if kind != "HUMAN" {
			return ErrWorkForbidden
		}
		if m.clarificationReplySource == nil {
			return ErrUnavailable
		}
		fact, found, err := m.clarificationReplySource(ctx, tx, tenant, id, true)
		if err != nil {
			return err
		}
		if !found || fact.ProjectID != project {
			return ErrProjectNotFound
		}
		if fact.RequesterID != actor {
			return ErrWorkForbidden
		}
		return nil
	}
	return deliveryCommand(ctx, m, "clarification", resultHTTPError, "clarification.handle-reply", http.MethodPost, clarificationHandledPath, in.Key, 200, in.Body, map[string]string{"entity_id": project.String(), "clarification_id": id.String()}, func(tx *gorm.DB) (clarificationReceiptView, guid.ID, error) {
		v, err := m.handleClarificationOn(ctx, tx, tenant, project, id, actor, in.Body.ReplyVersion)
		return v, v.ID, err
	}, authorize)
}
