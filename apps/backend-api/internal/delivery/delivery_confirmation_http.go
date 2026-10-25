package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/danielgtaylor/huma/v2"
	"gorm.io/gorm"
)

const deliveryConfirmationPath = "/entities/{entity_id}/project/delivery-packages/{package_id}/confirmation-requests"
const deliveryConfirmationWithdrawPath = "/entities/{entity_id}/project/delivery-confirmations/{confirmation_id}/withdrawal"

type deliveryConfirmationInput struct {
	EntityID  string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	PackageID string `path:"package_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key       string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
	Body      struct {
		SourceVersion int `json:"source_version" minimum:"1"`
	}
}
type deliveryConfirmationWithdrawInput struct {
	EntityID       string `path:"entity_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	ConfirmationID string `path:"confirmation_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Key            string `header:"Idempotency-Key" required:"true" minLength:"1" maxLength:"64"`
}

func (m *Module) registerDeliveryConfirmationHTTP(api huma.API) {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "delivery.confirmation.request", Method: "POST", Path: deliveryConfirmationPath, DefaultStatus: 201, Summary: "Request Human publication of an exact client package draft", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "delivery", Verb: "compose"}, m.requestDeliveryConfirmationHTTP)
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "delivery.confirmation.withdraw", Method: "PUT", Path: deliveryConfirmationWithdrawPath, Summary: "Withdraw a client package confirmation request", Tags: []string{"entities"}, Errors: []int{400, 401, 403, 404, 409, 422, 500, 503}}, authz.EntityPath, authz.ResourceOwnerResolver(m.entityOwner), authz.Guard{Resource: "delivery", Verb: "compose"}, m.withdrawDeliveryConfirmationHTTP)
}
func (m *Module) requestDeliveryConfirmationHTTP(ctx context.Context, in *deliveryConfirmationInput) (*deliveryCommandOutput[DeliveryConfirmationFact], error) {
	project, e := guid.RequireID(in.EntityID)
	pkg, e2 := guid.RequireID(in.PackageID)
	if e != nil || e2 != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	return deliveryCommand(ctx, m, "delivery_confirmation", resultHTTPError, "delivery.confirmation.request", "POST", deliveryConfirmationPath, in.Key, 201, in.Body, map[string]string{"entity_id": project.String(), "package_id": pkg.String()}, func(tx *gorm.DB) (DeliveryConfirmationFact, guid.ID, error) {
		v, e := m.requestDeliveryConfirmationOn(ctx, tx, tenant, project, pkg, actor, in.Body.SourceVersion)
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		_, _, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "delivery_compose")
		if err != nil {
			return err
		}
		// Replay still requires a visible source. Its old revision is allowed to be
		// published or edited; the receipt is history, not current pending authority.
		_, err = m.confirmationSourceOn(ctx, tx, tenant, project, pkg, false)
		return err
	})
}
func (m *Module) withdrawDeliveryConfirmationHTTP(ctx context.Context, in *deliveryConfirmationWithdrawInput) (*deliveryCommandOutput[DeliveryConfirmationFact], error) {
	project, e := guid.RequireID(in.EntityID)
	id, e2 := guid.RequireID(in.ConfirmationID)
	if e != nil || e2 != nil {
		return nil, resultHTTPError(ctx, ErrInvalidArgument)
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	return deliveryCommand(ctx, m, "delivery_confirmation", resultHTTPError, "delivery.confirmation.withdraw", "PUT", deliveryConfirmationWithdrawPath, in.Key, 200, struct{}{}, map[string]string{"entity_id": project.String(), "confirmation_id": id.String()}, func(tx *gorm.DB) (DeliveryConfirmationFact, guid.ID, error) {
		v, e := m.withdrawDeliveryConfirmationOn(ctx, tx, tenant, project, id, actor)
		return v, v.ID, e
	}, func(tx *gorm.DB) error {
		_, kind, err := m.constitutionProjectOn(ctx, tx, tenant, project, actor, "delivery_compose")
		if err != nil {
			return err
		}
		if kind != "HUMAN" {
			return ErrWorkForbidden
		}
		return nil
	})
}
