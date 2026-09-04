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
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
}

type createRequestInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	Body     struct {
		RoleID          string    `json:"role_id" minLength:"1" maxLength:"32"`
		Reason          string    `json:"reason" minLength:"3" maxLength:"500"`
		AccessExpiresAt time.Time `json:"access_expires_at" format:"date-time"`
	}
}

type decisionInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	ID       string `path:"id" maxLength:"64"`
	Body     struct {
		Note string `json:"note,omitempty" maxLength:"500"`
	}
}

type revokeInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	ID       string `path:"id" maxLength:"64"`
	Body     struct {
		Reason string `json:"reason,omitempty" maxLength:"500"`
	}
}

type createReviewInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	Body     struct {
		Name  string    `json:"name" minLength:"3" maxLength:"128"`
		DueAt time.Time `json:"due_at" format:"date-time"`
	}
}

type reviewInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	ID       string `path:"id" maxLength:"64"`
}

type reviewItemInput struct {
	TenantID string `header:"X-Tenant-Id" maxLength:"128"`
	ID       string `path:"id" maxLength:"64"`
	ItemID   string `path:"item_id" maxLength:"64"`
	Body     struct {
		Decision string `json:"decision" enum:"keep,revoke"`
		Note     string `json:"note,omitempty" maxLength:"500"`
	}
}

func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.RegisterTenantMember(registrar, api, operation(
		"governance-list-requestable-roles", http.MethodGet, "/iam/requestable-roles",
		"List tenant roles available for access requests", http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), func(ctx context.Context, in *tenantInput) (*respx.Body[[]RequestableRole], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, err := service.RequestableRoles(ctx, tenantID)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, items), nil
	})

	createRequest := operation(
		"governance-create-access-request", http.MethodPost, "/iam/access-requests",
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

	authz.RegisterTenantMember(registrar, api, operation(
		"governance-list-my-access-requests", http.MethodGet, "/iam/my/access-requests",
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
		"governance-list-access-requests", http.MethodGet, "/iam/access-requests",
		"List tenant access requests for approval", http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), authz.Guard{Resource: "access_request", Verb: "view"}, func(ctx context.Context, in *tenantInput) (*respx.Body[[]AccessRequest], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, err := service.List(ctx, tenantID, 0)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, items), nil
	})

	registerRequestDecisionHTTP(api, service, registrar)
	registerReviewHTTP(api, service, registrar)
}

func registerRequestDecisionHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	for _, route := range []struct {
		id, path, summary, decision string
	}{
		{"governance-approve-access-request", "/iam/access-requests/{id}/approve", "Approve an access request and activate its temporary role grant", StatusApproved},
		{"governance-reject-access-request", "/iam/access-requests/{id}/reject", "Reject a pending access request", StatusRejected},
	} {
		authz.Register(registrar, api, operation(route.id, http.MethodPost, route.path, route.summary,
			http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity, http.StatusInternalServerError,
		), authz.Guard{Resource: "access_request", Verb: "approve"}, func(ctx context.Context, in *decisionInput) (*respx.Body[AccessRequest], error) {
			tenantID, id, actorID, err := requestActor(ctx, in.TenantID, in.ID)
			if err != nil {
				return nil, err
			}
			var item AccessRequest
			if route.decision == StatusApproved {
				item, err = service.Approve(ctx, tenantID, id, actorID, in.Body.Note)
			} else {
				item, err = service.Reject(ctx, tenantID, id, actorID, in.Body.Note)
			}
			if err != nil {
				return nil, governanceError(err)
			}
			return respx.OK(ctx, item), nil
		})
	}

	authz.Register(registrar, api, operation(
		"governance-revoke-access-grant", http.MethodPost, "/iam/access-requests/{id}/revoke",
		"Revoke an approved temporary role grant", http.StatusNotFound, http.StatusConflict, http.StatusGone,
		http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), authz.Guard{Resource: "access_request", Verb: "approve"}, func(ctx context.Context, in *revokeInput) (*respx.Body[AccessRequest], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.TenantID, in.ID)
		if err != nil {
			return nil, err
		}
		item, err := service.Revoke(ctx, tenantID, id, actorID, in.Body.Reason)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.RegisterTenantMember(registrar, api, operation(
		"governance-withdraw-access-request", http.MethodPost, "/iam/access-requests/{id}/withdraw",
		"Withdraw a pending request or relinquish its approved access", http.StatusNotFound, http.StatusConflict,
		http.StatusGone, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), func(ctx context.Context, in *revokeInput) (*respx.Body[AccessRequest], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.TenantID, in.ID)
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
		"governance-list-access-reviews", http.MethodGet, "/iam/access-reviews",
		"List tenant access review campaigns", http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), authz.Guard{Resource: "access_review", Verb: "view"}, func(ctx context.Context, in *tenantInput) (*respx.Body[[]AccessReview], error) {
		tenantID, err := parseGovernanceID(in.TenantID)
		if err != nil {
			return nil, err
		}
		items, err := service.ListReviews(ctx, tenantID)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, items), nil
	})

	createReview := operation(
		"governance-create-access-review", http.MethodPost, "/iam/access-reviews",
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

	authz.Register(registrar, api, operation(
		"governance-get-access-review", http.MethodGet, "/iam/access-reviews/{id}",
		"Get an access review and its snapshotted grants", http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), authz.Guard{Resource: "access_review", Verb: "view"}, func(ctx context.Context, in *reviewInput) (*respx.Body[AccessReview], error) {
		tenantID, id, err := tenantResourceIDs(in.TenantID, in.ID)
		if err != nil {
			return nil, err
		}
		item, err := service.GetReview(ctx, tenantID, id)
		if err != nil {
			return nil, governanceError(err)
		}
		return respx.OK(ctx, item), nil
	})

	authz.Register(registrar, api, operation(
		"governance-decide-access-review-item", http.MethodPost, "/iam/access-reviews/{id}/items/{item_id}/decide",
		"Keep or revoke one snapshotted tenant access grant", http.StatusNotFound, http.StatusConflict, http.StatusGone,
		http.StatusUnprocessableEntity, http.StatusInternalServerError,
	), authz.Guard{Resource: "access_review", Verb: "decide"}, func(ctx context.Context, in *reviewItemInput) (*respx.Body[AccessReviewItem], error) {
		tenantID, id, actorID, err := requestActor(ctx, in.TenantID, in.ID)
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

	for _, route := range []struct {
		id, path, summary, status string
	}{
		{"governance-complete-access-review", "/iam/access-reviews/{id}/complete", "Complete an access review after every item is decided", ReviewStatusCompleted},
		{"governance-cancel-access-review", "/iam/access-reviews/{id}/cancel", "Cancel an open access review", ReviewStatusCancelled},
	} {
		authz.Register(registrar, api, operation(route.id, http.MethodPost, route.path, route.summary,
			http.StatusNotFound, http.StatusConflict, http.StatusGone, http.StatusUnprocessableEntity, http.StatusInternalServerError,
		), authz.Guard{Resource: "access_review", Verb: "manage"}, func(ctx context.Context, in *reviewInput) (*respx.Body[AccessReview], error) {
			tenantID, id, actorID, err := requestActor(ctx, in.TenantID, in.ID)
			if err != nil {
				return nil, err
			}
			var item AccessReview
			if route.status == ReviewStatusCompleted {
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
}

func operation(id, method, path, summary string, errors ...int) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"governance"}, Errors: errors}
}

func parseGovernanceID(value string) (guid.ID, error) {
	id, err := guid.Parse(strings.TrimSpace(value))
	if err != nil {
		return 0, huma.Error422UnprocessableEntity("respx.invalid_id")
	}
	return id, nil
}

func tenantResourceIDs(tenant, resource string) (guid.ID, guid.ID, error) {
	tenantID, err := parseGovernanceID(tenant)
	if err != nil {
		return 0, 0, err
	}
	id, err := parseGovernanceID(resource)
	return tenantID, id, err
}

func requestActor(ctx context.Context, tenant, resource string) (guid.ID, guid.ID, guid.ID, error) {
	tenantID, id, err := tenantResourceIDs(tenant, resource)
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
