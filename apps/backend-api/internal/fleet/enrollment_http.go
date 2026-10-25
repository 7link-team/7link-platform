package fleet

import (
	"context"
	"errors"
	"net/http"

	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type enrollmentSettingsInput struct {
	OS string `query:"os" enum:"WINDOWS,MACOS,LINUX" default:"WINDOWS"`
}

type enrollmentLookupInput struct {
	UserCode string `query:"user_code" required:"true" minLength:"8" maxLength:"16"`
}

type enrollmentStartInput struct{ Body EnrollmentStart }
type EnrollmentInput struct {
	EnrollmentID string `path:"enrollment_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}
type enrollmentDecisionInput struct {
	EnrollmentInput
	Body struct {
		UserCode string `json:"user_code" minLength:"8" maxLength:"16"`
		Approve  bool   `json:"approve"`
		Name     string `json:"name,omitempty" maxLength:"100"`
	}
}
type enrollmentExchangeInput struct {
	EnrollmentInput
	Body struct {
		TenantID   guid.ID `json:"tenant_id"`
		DeviceCode string  `json:"device_code" minLength:"1" maxLength:"256"`
	}
}
type privateFleetBody[T any] struct {
	CacheControl string `header:"Cache-Control"`
	Body         respx.Envelope[T]
}

func (m *Module) registerEnrollmentHTTP(api huma.API) error {
	authz.Register(m.registrar, api, huma.Operation{OperationID: "machine-enrollment.settings", Method: http.MethodGet, Path: "/machine-enrollment-settings", Summary: "Read configured machine installation commands", Tags: []string{"machines"}, Errors: []int{401, 403, 422, 503}}, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.enrollmentSettingsHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "machine-enrollment.lookup", Method: http.MethodGet, Path: "/machine-enrollments", Summary: "Look up a pending machine by its enrollment code", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 422, 429, 503}, Metadata: authn.RateLimitMetadata(authn.RateLimitOAuth)}, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.enrollmentLookupHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "machine-enrollment.get", Method: http.MethodGet, Path: "/machine-enrollments/{enrollment_id}", Summary: "Read current machine enrollment status", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 422, 503}}, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.enrollmentStatusHTTP)
	authz.RegisterPublic(api, huma.Operation{OperationID: "machine-enrollment.create", Method: http.MethodPost, Path: "/machine-enrollments", Summary: "Start IAM device authorization for a machine", Tags: []string{"machines"}, DefaultStatus: 201, Errors: []int{403, 409, 422, 503}}, m.startEnrollmentHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "machine-enrollment.decide", Method: http.MethodPut, Path: "/machine-enrollments/{enrollment_id}/decision", Summary: "Confirm or deny a machine enrollment", Tags: []string{"machines"}, Errors: []int{401, 403, 404, 409, 422, 503}}, authz.Guard{Resource: "machine", Verb: "manage", FilterData: true}, m.decideEnrollmentHTTP)
	authz.RegisterPublic(api, huma.Operation{OperationID: "machine-enrollment.exchange", Method: http.MethodPost, Path: "/machine-enrollments/{enrollment_id}/credential", Summary: "Exchange an approved IAM device grant for a one-time machine credential", Tags: []string{"machines"}, Errors: []int{403, 404, 409, 422, 503}}, m.exchangeEnrollmentHTTP)
	authz.RegisterPublic(api, huma.Operation{OperationID: "machine-enrollment.cancel", Method: http.MethodPost, Path: "/machine-enrollments/{enrollment_id}/cancellation", Summary: "Withdraw a pending enrollment using the private device proof", Tags: []string{"machines"}, Errors: []int{404, 409, 422, 503}}, m.cancelEnrollmentHTTP)
	for _, path := range []string{"/tenants/{tenant_id}/machine-enrollment-settings", "/tenants/{tenant_id}/machine-enrollments", "/tenants/{tenant_id}/machine-enrollments/{enrollment_id}", "/machine-enrollments", "/tenants/{tenant_id}/machine-enrollments/{enrollment_id}/decision", "/machine-enrollments/{enrollment_id}/credential", "/machine-enrollments/{enrollment_id}/cancellation"} {
		if err := authz.RegisterRequestContract(api, path); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) cancelEnrollmentHTTP(ctx context.Context, in *enrollmentExchangeInput) (*privateFleetBody[bool], error) {
	id, err := guid.RequireID(in.EnrollmentID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	if err = m.CancelEnrollment(ctx, in.Body.TenantID, id, in.Body.DeviceCode); err != nil {
		return nil, enrollmentHTTPError(ctx, err)
	}
	return &privateFleetBody[bool]{CacheControl: "no-store", Body: respx.OK(ctx, true).Body}, nil
}
func enrollmentHTTPError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrEnrollmentSlowDown):
		return respx.ErrStatus(ctx, 409, 409, "fleet.slow_down", map[string]string{"reason_code": "SLOW_DOWN"})
	case errors.Is(err, ErrEnrollmentPending), errors.Is(err, oauth.ErrAuthorizationPending):
		return respx.ErrStatus(ctx, 409, 409, "fleet.pending", map[string]string{"reason_code": "AUTHORIZATION_PENDING"})
	case errors.Is(err, ErrEnrollmentConsumed):
		return respx.ErrStatus(ctx, 409, 409, "fleet.consumed", map[string]string{"reason_code": "CREDENTIAL_ALREADY_DELIVERED"})
	case errors.Is(err, ErrEnrollmentFailed), errors.Is(err, oauth.ErrDeviceRequestNotFound), errors.Is(err, oauth.ErrExpiredToken), errors.Is(err, oauth.ErrAccessDenied):
		return respx.ErrStatus(ctx, 409, 409, "fleet.enrollment_failed", map[string]string{"reason_code": "RESTART_ENROLLMENT"})
	case errors.Is(err, oauth.ErrInvalidRequest):
		return fleetHTTPError(ctx, ErrInvalidArgument)
	default:
		return fleetHTTPError(ctx, err)
	}
}
func (m *Module) startEnrollmentHTTP(ctx context.Context, in *enrollmentStartInput) (*privateFleetBody[EnrollmentStarted], error) {
	v, err := m.StartEnrollment(ctx, in.Body)
	if err != nil {
		return nil, enrollmentHTTPError(ctx, err)
	}
	return &privateFleetBody[EnrollmentStarted]{CacheControl: "no-store", Body: respx.OK(ctx, v).Body}, nil
}
func (m *Module) decideEnrollmentHTTP(ctx context.Context, in *enrollmentDecisionInput) (*respx.Body[bool], error) {
	id, err := guid.RequireID(in.EnrollmentID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	if err = m.DecideEnrollment(ctx, authn.TenantIDFromContext(ctx), id, in.Body.UserCode, in.Body.Approve, in.Body.Name); err != nil {
		return nil, enrollmentHTTPError(ctx, err)
	}
	return respx.OK(ctx, true), nil
}

func (m *Module) enrollmentLookupHTTP(ctx context.Context, in *enrollmentLookupInput) (*privateFleetBody[EnrollmentStatus], error) {
	v, e := m.LookupEnrollment(ctx, authn.TenantIDFromContext(ctx), in.UserCode)
	if e != nil {
		return nil, enrollmentHTTPError(ctx, e)
	}
	return &privateFleetBody[EnrollmentStatus]{CacheControl: "no-store", Body: respx.OK(ctx, v).Body}, nil
}
func (m *Module) exchangeEnrollmentHTTP(ctx context.Context, in *enrollmentExchangeInput) (*privateFleetBody[EnrollmentCredential], error) {
	id, err := guid.RequireID(in.EnrollmentID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	if in.Body.TenantID <= 0 || in.Body.DeviceCode == "" {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	v, err := m.ExchangeEnrollment(ctx, in.Body.TenantID, id, in.Body.DeviceCode)
	if err != nil {
		return nil, enrollmentHTTPError(ctx, err)
	}
	return &privateFleetBody[EnrollmentCredential]{CacheControl: "no-store", Body: respx.OK(ctx, v).Body}, nil
}

func (m *Module) enrollmentSettingsHTTP(ctx context.Context, in *enrollmentSettingsInput) (*privateFleetBody[EnrollmentSettings], error) {
	v, err := m.EnrollmentSettings(ctx, authn.TenantIDFromContext(ctx), in.OS)
	if err != nil {
		return nil, enrollmentHTTPError(ctx, err)
	}
	return &privateFleetBody[EnrollmentSettings]{CacheControl: "no-store", Body: respx.OK(ctx, v).Body}, nil
}
func (m *Module) enrollmentStatusHTTP(ctx context.Context, in *EnrollmentInput) (*privateFleetBody[EnrollmentStatus], error) {
	id, err := guid.RequireID(in.EnrollmentID)
	if err != nil {
		return nil, fleetHTTPError(ctx, ErrInvalidArgument)
	}
	v, err := m.EnrollmentStatus(ctx, authn.TenantIDFromContext(ctx), id)
	if err != nil {
		return nil, enrollmentHTTPError(ctx, err)
	}
	return &privateFleetBody[EnrollmentStatus]{CacheControl: "no-store", Body: respx.OK(ctx, v).Body}, nil
}
