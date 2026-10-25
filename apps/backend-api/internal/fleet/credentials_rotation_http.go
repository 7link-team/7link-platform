package fleet

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type credentialPullInput struct {
	MachineInput
	Body InspectionSession
}
type credentialAckInput struct {
	MachineInput
	RotationID string `path:"rotation_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body       struct {
		InspectionSession
		CredentialID guid.ID `json:"credential_id"`
	}
}

func (m *Module) registerCredentialRotationHTTP(api huma.API) error {
	authz.RegisterOwned(m.registrar, api, huma.Operation{OperationID: "machine.credential-rotation.create", Method: http.MethodPost, Path: "/machines/{machine_id}/credential-rotations", Summary: "Request machine credential rotation", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{401, 403, 404, 409, 422, 503}}, "machine_id", m.machineOwner, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.requestCredentialRotationHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "machine.credential-delivery.pull", Method: http.MethodPost, Path: "/machines/{machine_id}/credential-deliveries", Summary: "Receive a one-time machine credential delivery", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.pullCredentialDeliveryHTTP)
	authz.RegisterAuthenticated(m.registrar, api, huma.Operation{OperationID: "machine.credential-delivery.acknowledge", Method: http.MethodPut, Path: "/machines/{machine_id}/credential-deliveries/{rotation_id}/ack", Summary: "Acknowledge persisted machine credentials", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, m.ackCredentialDeliveryHTTP)
	for _, path := range []string{"/machines/{machine_id}/credential-rotations", "/machines/{machine_id}/credential-deliveries", "/machines/{machine_id}/credential-deliveries/{rotation_id}/ack"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) requestCredentialRotationHTTP(ctx context.Context, in *MachineInput) (*respx.Body[CredentialRotationView], error) {
	id, e := guid.RequireID(in.MachineID)
	if e != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	out, e := m.RequestCredentialRotation(ctx, authn.TenantIDFromContext(ctx), id)
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
func (m *Module) pullCredentialDeliveryHTTP(ctx context.Context, in *credentialPullInput) (*privateFleetBody[[]CredentialDelivery], error) {
	id, e := guid.RequireID(in.MachineID)
	if e != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, e = m.BindRunnerContext(ctx, id)
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	out, e := m.PullCredentialDelivery(ctx, authn.TenantIDFromContext(ctx), id, in.Body.SessionID, in.Body.ConnEpoch)
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return &privateFleetBody[[]CredentialDelivery]{CacheControl: "no-store", Body: respx.OK(ctx, out).Body}, nil
}
func (m *Module) ackCredentialDeliveryHTTP(ctx context.Context, in *credentialAckInput) (*respx.Body[CredentialRotationView], error) {
	machine, e := guid.RequireID(in.MachineID)
	if e != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	id, e := guid.RequireID(in.RotationID)
	if e != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	ctx, e = m.BindRunnerContext(ctx, machine)
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	out, e := m.AckCredentialDelivery(ctx, authn.TenantIDFromContext(ctx), machine, id, in.Body.CredentialID, in.Body.SessionID, in.Body.ConnEpoch)
	if e != nil {
		return nil, fleetHTTPError(ctx, e)
	}
	return respx.OK(ctx, out), nil
}
