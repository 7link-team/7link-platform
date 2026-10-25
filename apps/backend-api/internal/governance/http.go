package governance

import (
	"context"
	"errors"
	"net/http"

	iamauthn "7link.com/platform/internal/iam/authn"
	corei18n "7link.com/platform/internal/iam/core/i18n"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type inboxListInput struct {
	TenantID  string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Cursor    string `query:"cursor" maxLength:"2048"`
	Limit     int    `query:"limit" default:"50" minimum:"1" maximum:"100"`
	Kind      string `query:"kind" maxLength:"64"`
	ProjectID string `query:"project_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Sort      string `query:"sort" enum:"priority,-priority" doc:"Fixed priority descending, due_at ascending nulls last, created_at ascending; priority and -priority select this fixed order."`
}

func (m *Module) RegisterHTTP(_ context.Context, api huma.API) error {
	if m == nil || m.registrar == nil {
		return ErrUnavailable
	}
	authz.RegisterTenantMember(m.registrar, api, huma.Operation{
		OperationID: "governance.inbox.list", Method: http.MethodGet, Path: "/inbox-items", Summary: "List my current authorized decisions", Tags: []string{"tenants"}, Errors: []int{400, 401, 403, 422, 500, 503},
	}, m.listInboxHTTP)
	authz.Register(m.registrar, api, huma.Operation{OperationID: "governance.catalog.get", Method: http.MethodGet, Path: "/governance-catalog", Summary: "Get registered governance policies and capability status", Tags: []string{"tenants"}, Errors: []int{401, 403, 500, 503}}, authz.Guard{Resource: "policy", Verb: "read"}, m.getGovernanceHTTP)
	m.registerInboxCommandsHTTP(api)
	m.registerSettingsHTTP(api)
	m.registerEscalationHTTP(api)
	m.registerDelegationHTTP(api)
	m.registerSpawnHTTP(api)
	m.registerAgentSpawnRequestHTTP(api)
	m.registerQualityEscapeHTTP(api)
	m.registerBusinessAuditHTTP(api)
	m.registerJReviewerRolesHTTP(api)
	iamauthn.RegisterGovernanceReauthentication(api)
	return nil
}

type governanceCatalogInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type GovernancePolicyView struct {
	ID    string   `json:"id"`
	Codes []string `json:"codes"`
}

type GovernanceActionView struct {
	Code string `json:"code"`
	ActionRules
}

type GovernanceCatalog struct {
	Policies             []GovernancePolicyView `json:"policies"`
	Actions              []GovernanceActionView `json:"actions"`
	Escalations          string                 `json:"escalations" enum:"AVAILABLE"`
	Settings             string                 `json:"settings" enum:"AVAILABLE"`
	VerificationOverride string                 `json:"verification_override" enum:"UNAVAILABLE"`
}

func (m *Module) getGovernanceHTTP(ctx context.Context, _ *governanceCatalogInput) (*respx.Body[GovernanceCatalog], error) {
	if m == nil || m.seed == nil || m.seed.registry == nil {
		return nil, inboxHTTPError(ctx, ErrUnavailable)
	}
	catalog := GovernanceCatalog{Policies: []GovernancePolicyView{}, Actions: []GovernanceActionView{}, Escalations: "AVAILABLE", Settings: "AVAILABLE", VerificationOverride: "UNAVAILABLE"}
	m.policies.mu.RLock()
	if !m.policies.sealed {
		m.policies.mu.RUnlock()
		return nil, inboxHTTPError(ctx, ErrUnavailable)
	}
	for _, rule := range m.policies.rules {
		catalog.Policies = append(catalog.Policies, GovernancePolicyView{ID: rule.ID, Codes: append([]string{}, rule.Codes...)})
	}
	m.policies.mu.RUnlock()
	for _, action := range m.seed.registry.All() {
		if action.Scope == authz.ActionScopeTenant {
			catalog.Actions = append(catalog.Actions, GovernanceActionView{Code: action.Code, ActionRules: RulesForAction(action.Code, "AGENT")})
		}
	}
	return respx.OK(ctx, catalog), nil
}

func (m *Module) listInboxHTTP(ctx context.Context, in *inboxListInput) (*respx.Body[InboxPage], error) {
	if m == nil || in == nil {
		return nil, inboxHTTPError(ctx, ErrUnavailable)
	}
	tenant, err := guid.RequireID(in.TenantID)
	if err != nil {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	// Never derive the recipient from query parameters or the URL's tenant.
	if authn.TenantIDFromContext(ctx) != tenant || authn.PrincipalIDFromContext(ctx) <= 0 {
		return nil, respx.ErrStatus(ctx, 403, 160403, corei18n.PrivilegeEscalationDenied, nil)
	}
	var project guid.ID
	if in.ProjectID != "" {
		project, err = guid.RequireID(in.ProjectID)
		if err != nil {
			return nil, inboxHTTPError(ctx, ErrInvalidArgument)
		}
	}
	if in.Sort != "" && in.Sort != "priority" && in.Sort != "-priority" {
		return nil, inboxHTTPError(ctx, ErrInvalidArgument)
	}
	page, err := m.readInbox(ctx, InboxRequest{TenantID: tenant, PrincipalID: authn.PrincipalIDFromContext(ctx), ProjectID: project, Kind: in.Kind, Limit: in.Limit}, in.Cursor, false)
	if err != nil {
		return nil, inboxHTTPError(ctx, err)
	}
	return respx.OK(ctx, page), nil
}

func inboxHTTPError(ctx context.Context, err error) error {
	if errors.Is(err, ErrForbidden) {
		return respx.ErrStatus(ctx, 403, 160403, corei18n.PrivilegeEscalationDenied, nil)
	}
	if errors.Is(err, ErrNotFound) {
		return respx.ErrStatus(ctx, 404, 160404, corei18n.ResourceNotFound, nil)
	}
	if errors.Is(err, ErrVersion) {
		return respx.ErrStatus(ctx, 409, 160409, corei18n.ResourceConflict, nil)
	}
	if errors.Is(err, ErrInvalidArgument) {
		return respx.ErrStatus(ctx, 400, 160400, corei18n.InvalidRequest, nil)
	}
	if errors.Is(err, ErrUnavailable) {
		return respx.ErrStatus(ctx, 503, 160503, corei18n.Unavailable, nil)
	}
	return respx.ErrStatus(ctx, 500, 160500, corei18n.Unavailable, nil)
}
