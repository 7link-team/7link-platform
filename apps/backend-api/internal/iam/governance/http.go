package governance

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"7link.com/platform/internal/iam/core"
	governancei18n "7link.com/platform/internal/iam/governance/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type tenantInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
}

// listInput bounds a tenant listing by default. tenantInput stays as it is
// because the endpoints that only address a tenant share it; these listings grow
// with the tenant and an omitted limit used to mean "every row".
type listInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type createRequestInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     struct {
		RoleID          string    `json:"role_id" minLength:"1" maxLength:"32"`
		Reason          string    `json:"reason" minLength:"3" maxLength:"500"`
		AccessExpiresAt time.Time `json:"access_expires_at" format:"date-time"`
	}
}

type decisionInput struct {
	ID   string `path:"request_id" maxLength:"64"`
	Body struct {
		Decision string `json:"decision" enum:"approved,rejected"`
		Note     string `json:"note,omitempty" maxLength:"500"`
	}
}

type revokeInput struct {
	ID   string `path:"request_id" maxLength:"64"`
	Body struct {
		Reason string `json:"reason,omitempty" maxLength:"500"`
	}
}

type createReviewInput struct {
	TenantID string `path:"tenant_id" maxLength:"128"`
	Body     struct {
		Name  string    `json:"name" minLength:"3" maxLength:"128"`
		DueAt time.Time `json:"due_at" format:"date-time"`
	}
}

type reviewInput struct {
	ID string `path:"review_id" maxLength:"64"`
}

type reviewUpdateInput struct {
	ID   string `path:"review_id" maxLength:"64"`
	Body struct {
		Status string `json:"status" enum:"completed,cancelled"` //nolint:misspell // Frozen source state value.
	}
}

type reviewItemInput struct {
	ID     string `path:"review_id" maxLength:"64"`
	ItemID string `path:"item_id" maxLength:"64"`
	Body   struct {
		Decision string `json:"decision" enum:"keep,revoke"`
		Note     string `json:"note,omitempty" maxLength:"500"`
	}
}

func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.RegisterTenantMember(registrar, api, operation(
		"governance-list-requestable-roles", http.MethodGet, "/requestable-roles",
		"List tenant roles available for access requests", http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), func(ctx context.Context, in *listInput) (*respx.Body[[]RequestableRole], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.RequestableRolesPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})

	createRequest := operation(
		"governance-create-access-request", http.MethodPost, "/access-requests",
		"Request time-limited membership in a tenant role", http.StatusNotFound, http.StatusConflict,
		http.StatusUnprocessableEntity, http.StatusInternalServerError,
	)
	createRequest.DefaultStatus = http.StatusCreated
	authz.RegisterTenantMember(registrar, api, createRequest, func(ctx context.Context, in *createRequestInput) (*respx.Body[AccessRequest], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		principalID, err := humanPrincipal(ctx)
		if err != nil {
			return nil, err
		}
		roleID, err := parseGovernanceID(in.Body.RoleID)
		if err != nil {
			return nil, err
		}
		item, err := service.Create(ctx, tenantID, principalID, CreateAccessRequest{
			RoleID: roleID, Reason: in.Body.Reason, AccessExpiresAt: in.Body.AccessExpiresAt,
		})
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.RegisterPrincipalTenantMember(registrar, api, operation(
		"governance-list-my-access-requests", http.MethodGet, "/principals/{principal_id}/access-requests",
		"List the current principal's tenant access requests", http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), func(ctx context.Context, in *tenantInput) (*respx.Body[[]AccessRequest], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		principalID, err := humanPrincipal(ctx)
		if err != nil {
			return nil, err
		}
		items, err := service.List(ctx, tenantID, principalID)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, items), nil
	})

	authz.Register(registrar, api, operation(
		"governance-list-access-requests", http.MethodGet, "/access-requests",
		"List tenant access requests for approval", http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), authz.Guard{Resource: "access_request", Verb: "view"}, func(ctx context.Context, in *listInput) (*respx.Body[[]AccessRequest], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.ListPage(ctx, tenantID, 0, in.Offset, in.Limit)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})

	registerRequestDecisionHTTP(api, service, registrar)
	registerReviewHTTP(api, service, registrar)
}

func registerRequestDecisionHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.RegisterOwned(registrar, api, operation(
		"governance-set-access-request-decision", http.MethodPut, "/access-requests/{request_id}/decision",
		"Set the approval decision for an access request", http.StatusNotFound, http.StatusConflict,
		http.StatusGone, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), "request_id", service.resolveAccessRequestOwner, authz.Guard{Resource: "access_request", Verb: "approve"}, func(ctx context.Context, in *decisionInput) (*respx.Body[AccessRequest], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var item AccessRequest
		if in.Body.Decision == StatusApproved {
			item, err = service.Approve(ctx, tenantID, id, actorID, in.Body.Note)
		} else {
			item, err = service.Reject(ctx, tenantID, id, actorID, in.Body.Note)
		}
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.RegisterOwned(registrar, api, operation(
		"governance-revoke-access-grant", http.MethodPut, "/access-requests/{request_id}/grant-revocation",
		"Revoke an approved temporary role grant", http.StatusNotFound, http.StatusConflict, http.StatusGone,
		http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), "request_id", service.resolveAccessRequestOwner, authz.Guard{Resource: "access_request", Verb: "approve"}, func(ctx context.Context, in *revokeInput) (*respx.Body[AccessRequest], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		item, err := service.Revoke(ctx, tenantID, id, actorID, in.Body.Reason)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.RegisterOwnedTenantMember(registrar, api, operation(
		"governance-withdraw-access-request", http.MethodPut, "/access-requests/{request_id}/withdrawal",
		"Withdraw a pending request or relinquish its approved access", http.StatusNotFound, http.StatusConflict,
		http.StatusGone, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), "request_id", service.resolveAccessRequestOwner, func(ctx context.Context, in *revokeInput) (*respx.Body[AccessRequest], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		item, err := service.Withdraw(ctx, tenantID, id, actorID, in.Body.Reason)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})
}

func registerReviewHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.Register(registrar, api, operation(
		"governance-list-access-reviews", http.MethodGet, "/access-reviews",
		"List tenant access review campaigns", http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), authz.Guard{Resource: "access_review", Verb: "view"}, func(ctx context.Context, in *listInput) (*respx.Body[[]AccessReview], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, total, err := service.ListReviewsPage(ctx, tenantID, in.Offset, in.Limit)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
	})

	createReview := operation(
		"governance-create-access-review", http.MethodPost, "/access-reviews",
		"Create a review of current tenant access grants", http.StatusConflict, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	)
	createReview.DefaultStatus = http.StatusCreated
	authz.Register(registrar, api, createReview, authz.Guard{Resource: "access_review", Verb: "create"}, func(ctx context.Context, in *createReviewInput) (*respx.Body[AccessReview], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		actorID, err := humanPrincipal(ctx)
		if err != nil {
			return nil, err
		}
		item, err := service.CreateReview(ctx, tenantID, actorID, CreateAccessReview{Name: in.Body.Name, DueAt: in.Body.DueAt})
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.RegisterOwned(registrar, api, operation(
		"governance-get-access-review", http.MethodGet, "/access-reviews/{review_id}",
		"Get an access review and its snapshotted grants", http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), "review_id", service.resolveAccessReviewOwner, authz.Guard{Resource: "access_review", Verb: "view"}, func(ctx context.Context, in *reviewInput) (*respx.Body[AccessReview], error) {
		tenantID := authnext.TenantIDFromContext(ctx)
		id, err := parseGovernanceID(in.ID)
		if err != nil {
			return nil, err
		}
		item, err := service.GetReview(ctx, tenantID, id)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.RegisterOwned(registrar, api, operation(
		"governance-decide-access-review-item", http.MethodPut, "/access-reviews/{review_id}/items/{item_id}/decision",
		"Keep or revoke one snapshotted tenant access grant", http.StatusNotFound, http.StatusConflict, http.StatusGone,
		http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), "review_id", service.resolveAccessReviewOwner, authz.Guard{Resource: "access_review", Verb: "decide"}, func(ctx context.Context, in *reviewItemInput) (*respx.Body[AccessReviewItem], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		itemID, err := parseGovernanceID(in.ItemID)
		if err != nil {
			return nil, err
		}
		item, err := service.DecideReviewItem(ctx, tenantID, id, itemID, actorID, in.Body.Decision, in.Body.Note)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.RegisterOwned(registrar, api, operation(
		"governance-update-access-review", http.MethodPatch, "/access-reviews/{review_id}",
		"Complete or cancel an access review", http.StatusNotFound, http.StatusConflict,
		http.StatusGone, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), "review_id", service.resolveAccessReviewOwner, authz.Guard{Resource: "access_review", Verb: "manage"}, func(ctx context.Context, in *reviewUpdateInput) (*respx.Body[AccessReview], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var item AccessReview
		if in.Body.Status == ReviewStatusCompleted {
			item, err = service.CompleteReview(ctx, tenantID, id, actorID)
		} else {
			item, err = service.CancelReview(ctx, tenantID, id, actorID)
		}
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})
}

func operation(id, method, path, summary string, errors ...int) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"access-control"}, Errors: errors}
}

func parseGovernanceID(value string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(value))
	if err != nil {
		return 0, huma.Error422UnprocessableEntity("respx.invalid_id")
	}
	return id, nil
}

func requestActor(ctx context.Context, resource string) (guid.ID, guid.ID, guid.ID, error) {
	tenantID := authnext.TenantIDFromContext(ctx)
	id, err := parseGovernanceID(resource)
	if err != nil {
		return 0, 0, 0, err
	}
	actorID, err := humanPrincipal(ctx)
	return tenantID, id, actorID, err
}

func humanPrincipal(ctx context.Context) (guid.ID, error) {
	claims, ok := authnext.FromContext(ctx)
	if !ok || claims == nil || claims.SubjectType != authnext.SubjectTypePrincipal || claims.PrincipalID.Zero() {
		return 0, huma.Error403Forbidden(governancei18n.HumanPrincipalRequired)
	}
	return claims.PrincipalID, nil
}

func governanceError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound(governancei18n.RequestNotFound)
	case errors.Is(err, ErrRoleNotFound):
		return huma.Error404NotFound(governancei18n.RoleNotFound)
	case errors.Is(err, ErrExpired):
		return huma.NewError(http.StatusGone, governancei18n.RequestExpired)
	case errors.Is(err, ErrRequesterInactive):
		return huma.Error409Conflict(governancei18n.RequesterInactive)
	case errors.Is(err, ErrAlreadyGranted):
		return huma.Error409Conflict(governancei18n.AlreadyGranted)
	case errors.Is(err, ErrStateConflict):
		return huma.Error409Conflict(governancei18n.RequestStateConflict)
	case errors.Is(err, ErrSelfApproval):
		return huma.Error409Conflict(governancei18n.SelfApproval)
	case errors.Is(err, ErrRequesterOnly):
		return huma.Error403Forbidden(governancei18n.RequesterOnly)
	case errors.Is(err, ErrReviewNotFound):
		return huma.Error404NotFound(governancei18n.ReviewNotFound)
	case errors.Is(err, ErrReviewItemNotFound):
		return huma.Error404NotFound(governancei18n.ReviewItemNotFound)
	case errors.Is(err, ErrReviewExpired):
		return huma.NewError(http.StatusGone, governancei18n.ReviewExpired)
	case errors.Is(err, ErrReviewEmpty):
		return huma.Error409Conflict(governancei18n.ReviewEmpty)
	case errors.Is(err, ErrReviewStateConflict):
		return huma.Error409Conflict(governancei18n.ReviewStateConflict)
	case errors.Is(err, ErrReviewIncomplete):
		return huma.Error409Conflict(governancei18n.ReviewIncomplete)
	case errors.Is(err, ErrReviewSelfDecision):
		return huma.Error409Conflict(governancei18n.ReviewSelfDecision)
	case errors.Is(err, ErrReviewLastAdministrator), errors.Is(err, core.ErrLastTenantAdministrator):
		return huma.Error409Conflict(governancei18n.ReviewLastAdministrator)
	case errors.Is(err, ErrReviewDynamicDerived):
		return huma.Error422UnprocessableEntity(governancei18n.ReviewDerivedNotRevocable)
	case errors.Is(err, ErrInvalidReview):
		return huma.Error422UnprocessableEntity(governancei18n.InvalidReview)
	case errors.Is(err, ErrInvalid):
		return huma.Error422UnprocessableEntity(governancei18n.InvalidRequest)
	default:
		return huma.Error500InternalServerError(governancei18n.Unavailable)
	}
}
