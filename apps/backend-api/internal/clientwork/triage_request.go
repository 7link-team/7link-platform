package clientwork

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

type TriageRequestBody struct {
	Note string `json:"note,omitempty" maxLength:"2000"`
}
type TriageRequestInput struct {
	EntityID string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ID       string `path:"id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key      string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body     TriageRequestBody
}

func (m *Module) registerTriageRequestHTTP(api huma.API) error {
	path := entityBase + "/intakes/{id}/triage-requests"
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "clientwork.intake.triage_request", Method: "POST", Path: path, Summary: "Request configured Intake triage with durable ten-minute coalescing", Tags: []string{"entities"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.owner), authz.Guard{Resource: "intake", Verb: "triage"}, m.requestTriageHTTP)
	accepted := *api.OpenAPI().Paths[path].Post.Responses["201"]
	accepted.Description = "Accepted; coalesced with the existing triage request"
	api.OpenAPI().Paths[path].Post.Responses["202"] = &accepted
	if m.registrar.IsDeclarationOnly() {
		return nil
	}
	return authz.RegisterRequestContract(api, path)
}

func (m *Module) requestTriageHTTP(ctx context.Context, in *TriageRequestInput) (*CommandOutput, error) {
	p, err := guid.RequireID(in.EntityID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	id, err := guid.RequireID(in.ID)
	if err != nil {
		return nil, httpError(ctx, ErrInvalid)
	}
	return m.command(ctx, p, "clientwork.intake.triage_request", "POST", entityBase+"/intakes/"+id.String()+"/triage-requests", in.Key, 201, in, "intake_triage", false, nil, func(tx *gorm.DB) (Receipt, error) {
		if m.triageTarget.RequestOn == nil {
			return Receipt{}, ErrUnavailable
		}
		result, err := m.triageTarget.RequestOn(ctx, tx, authn.TenantIDFromContext(ctx), p, id, in.Body.Note)
		if err != nil {
			return Receipt{}, err
		}
		return Receipt{ID: id, Version: result.Version, State: "REQUESTED", RequestedAt: &result.RequestedAt, Coalesced: result.Coalesced}, nil
	})
}
