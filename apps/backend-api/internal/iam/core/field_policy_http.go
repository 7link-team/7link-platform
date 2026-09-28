package core

import (
	"context"
	"net/http"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type fieldPolicyListInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Resource string `query:"resource" maxLength:"64"`
	EntityID string `query:"entity_id" maxLength:"64"`
}

// listFieldPoliciesInput bounds the policy listing by default. It repeats
// fieldPolicyListInput's parameters rather than sharing the type, so that the
// field catalog and the effective-fields routes -- which do not page -- keep
// their published contract, and this one gains only offset and limit. EntityID
// is carried for the same reason: this route has always declared it.
type listFieldPoliciesInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Resource string `query:"resource" maxLength:"64"`
	EntityID string `query:"entity_id" maxLength:"64"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type fieldPolicyInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Resource string `path:"resource" maxLength:"64"`
	Field    string `path:"field" maxLength:"64"`
	Body     struct {
		Version int64             `json:"version" minimum:"0"`
		Policy  authz.FieldPolicy `json:"policy"`
	}
}

type fieldPolicyDeleteInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Resource string `path:"resource" maxLength:"64"`
	Field    string `path:"field" maxLength:"64"`
	Version  int64  `query:"version" minimum:"1" required:"true"`
}

func registerFieldPolicyHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{OperationID: "iam-field-catalog", Method: http.MethodGet, Path: "/field-catalog", Summary: "List declared field capabilities", Tags: []string{"access-control"}}, authz.Guard{Resource: "field_policy", Verb: "view"},
		func(ctx context.Context, _ *fieldPolicyListInput) (*respx.Body[[]authz.Field], error) {
			return respx.OK(ctx, service.registry.Fields("")), nil
		})
	authz.Register(registrar, api, huma.Operation{OperationID: "iam-list-field-policies", Method: http.MethodGet, Path: "/field-policies", Summary: "List tenant field policies", Tags: []string{"access-control"}}, authz.Guard{Resource: "field_policy", Verb: "view"},
		func(ctx context.Context, in *listFieldPoliciesInput) (*respx.Body[[]ManagedFieldPolicy], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			policies, total, err := service.ListFieldPoliciesPage(ctx, tenantID, in.Resource, in.Offset, in.Limit)
			if err != nil {
				return nil, apiError("list field policies", err)
			}
			return respx.List(ctx, policies, respx.Page{Offset: in.Offset, Limit: in.Limit, Total: total}), nil
		})
	authz.Register(registrar, api, huma.Operation{OperationID: "iam-set-field-policy", Method: http.MethodPut, Path: "/field-policies/{resource}/{field}", Summary: "Set a tenant field policy", Tags: []string{"access-control"}, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity}}, authz.Guard{Resource: "field_policy", Verb: "update"},
		func(ctx context.Context, in *fieldPolicyInput) (*respx.Body[ManagedFieldPolicy], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			policy, err := service.SetFieldPolicy(ctx, tenantID, in.Resource, in.Field, in.Body.Policy, in.Body.Version)
			if err != nil {
				return nil, apiError("set field policy", err)
			}
			return respx.OK(ctx, policy), nil
		})
	authz.Register(registrar, api, huma.Operation{OperationID: "iam-delete-field-policy", Method: http.MethodDelete, Path: "/field-policies/{resource}/{field}", Summary: "Restore a field's declared default policy", Tags: []string{"access-control"}, Errors: []int{http.StatusConflict}}, authz.Guard{Resource: "field_policy", Verb: "update"},
		func(ctx context.Context, in *fieldPolicyDeleteInput) (*respx.Body[MutationResult], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			if err := service.DeleteFieldPolicy(ctx, tenantID, in.Resource, in.Field, in.Version); err != nil {
				return nil, apiError("delete field policy", err)
			}
			return respx.OK(ctx, MutationResult{Changed: true}), nil
		})
	// Effective fields reveal capabilities only, never values or other users' roles.
	authz.RegisterTenantMember(registrar, api, huma.Operation{OperationID: "iam-effective-fields", Method: http.MethodGet, Path: "/effective-fields", Summary: "Get own effective field capabilities", Tags: []string{"access-control"}},
		func(ctx context.Context, in *fieldPolicyListInput) (*respx.Body[authz.FieldConstraint], error) {
			claims, _ := authn.FromContext(ctx)
			if claims == nil {
				return nil, apiError("effective fields", ErrPrivilegeEscalation)
			}
			entityID, err := guid.OptionalID(in.EntityID)
			if err != nil {
				return nil, err
			}
			constraint, err := service.checker.FieldConstraint(ctx, claims.TenantID, entityID, in.Resource, claims.PrincipalID)
			if err != nil {
				return nil, apiError("effective fields", err)
			}
			return respx.OK(ctx, constraint), nil
		})
}
