package webhook

import (
	"context"
	"errors"
	"net/http"

	webhooki18n "7link.com/platform/internal/iam/webhook/i18n"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

type subscriptionListInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset   int    `query:"offset" minimum:"0" default:"0"`
	Limit    int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type subscriptionIDInput struct {
	SubscriptionID string `path:"subscription_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type deliveryListInput struct {
	SubscriptionID string `path:"subscription_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Offset         int    `query:"offset" minimum:"0" default:"0"`
	Limit          int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type subscriptionBody struct {
	Name       string   `json:"name" minLength:"1" maxLength:"128"`
	URL        string   `json:"url" minLength:"1" maxLength:"2048" doc:"Receiver endpoint; https unless the deployment permits plaintext"`
	Secret     string   `json:"secret,omitempty" maxLength:"512" doc:"Signing secret; an empty value keeps the stored secret on update"`
	EventTypes []string `json:"event_types,omitempty" maxItems:"64" doc:"Event types to deliver; omit for every type"`
	Status     string   `json:"status,omitempty" enum:"active,disabled" default:"active"`
}

type subscriptionCreateInput struct {
	TenantID string `path:"tenant_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body     subscriptionBody
}

type subscriptionReplaceInput struct {
	SubscriptionID string `path:"subscription_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body           subscriptionBody
}

// RegisterHTTP mounts the tenant management API. There is no public endpoint
// here: a receiver is called by this deployment, never the other way round.
func RegisterHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	authz.Register(registrar, api, huma.Operation{
		OperationID: "webhook-list-subscriptions", Method: http.MethodGet, Path: "/webhook-subscriptions",
		Summary: "List tenant webhook subscriptions", Tags: []string{"applications"},
	}, authz.Guard{Resource: "webhook_subscription", Verb: "view"},
		func(ctx context.Context, in *subscriptionListInput) (*respx.Body[[]Subscription], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			if service == nil {
				return nil, webhookError(ErrUnavailable)
			}
			items, total, err := service.ListSubscriptions(ctx, tenantID, in.Offset, in.Limit)
			if err != nil {
				return nil, webhookError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
		})

	authz.Register(registrar, api, huma.Operation{
		OperationID: "webhook-create-subscription", Method: http.MethodPost, Path: "/webhook-subscriptions",
		Summary: "Create a webhook subscription", Tags: []string{"applications"},
		DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict, http.StatusUnprocessableEntity},
	}, authz.Guard{Resource: "webhook_subscription", Verb: "create"},
		func(ctx context.Context, in *subscriptionCreateInput) (*respx.Body[Subscription], error) {
			tenantID, err := guid.RequireID(in.TenantID)
			if err != nil {
				return nil, err
			}
			if service == nil {
				return nil, webhookError(ErrUnavailable)
			}
			item, err := service.CreateSubscription(ctx, tenantID, subscriptionInputFrom(in.Body))
			if err != nil {
				return nil, webhookError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "webhook-update-subscription", Method: http.MethodPut, Path: "/webhook-subscriptions/{subscription_id}",
		Summary: "Replace a webhook subscription", Tags: []string{"applications"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, "subscription_id", service.resolveOwner, authz.Guard{Resource: "webhook_subscription", Verb: "update"},
		func(ctx context.Context, in *subscriptionReplaceInput) (*respx.Body[Subscription], error) {
			tenantID := authnext.TenantIDFromContext(ctx)
			id, err := guid.RequireID(in.SubscriptionID)
			if err != nil {
				return nil, err
			}
			if service == nil {
				return nil, webhookError(ErrUnavailable)
			}
			item, err := service.UpdateSubscription(ctx, tenantID, id, subscriptionInputFrom(in.Body))
			if err != nil {
				return nil, webhookError(err)
			}
			return respx.OK(ctx, item), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "webhook-delete-subscription", Method: http.MethodDelete, Path: "/webhook-subscriptions/{subscription_id}",
		Summary: "Delete a webhook subscription and its delivery history", Tags: []string{"applications"},
		Errors: []int{http.StatusNotFound},
	}, "subscription_id", service.resolveOwner, authz.Guard{Resource: "webhook_subscription", Verb: "delete"},
		func(ctx context.Context, in *subscriptionIDInput) (*respx.Body[map[string]bool], error) {
			tenantID := authnext.TenantIDFromContext(ctx)
			id, err := guid.RequireID(in.SubscriptionID)
			if err != nil {
				return nil, err
			}
			if service == nil {
				return nil, webhookError(ErrUnavailable)
			}
			if err := service.DeleteSubscription(ctx, tenantID, id); err != nil {
				return nil, webhookError(err)
			}
			return respx.OK(ctx, map[string]bool{"deleted": true}), nil
		})

	authz.RegisterOwned(registrar, api, huma.Operation{
		OperationID: "webhook-list-deliveries", Method: http.MethodGet, Path: "/webhook-subscriptions/{subscription_id}/deliveries",
		Summary: "List a subscription's delivery attempts", Tags: []string{"applications"},
		Errors: []int{http.StatusNotFound},
	}, "subscription_id", service.resolveOwner, authz.Guard{Resource: "webhook_subscription", Verb: "view"},
		func(ctx context.Context, in *deliveryListInput) (*respx.Body[[]Delivery], error) {
			tenantID := authnext.TenantIDFromContext(ctx)
			id, err := guid.RequireID(in.SubscriptionID)
			if err != nil {
				return nil, err
			}
			if service == nil {
				return nil, webhookError(ErrUnavailable)
			}
			items, total, err := service.ListDeliveries(ctx, tenantID, id, in.Offset, in.Limit)
			if err != nil {
				return nil, webhookError(err)
			}
			return respx.List(ctx, items, respx.Page{Offset: in.Offset, Limit: in.Limit, Count: len(items), Total: total}), nil
		})
}

// subscriptionInputFrom converts the request body to the service's input. The
// two have the same fields on purpose -- the contract and the validated input
// describe the same registration -- so this is a conversion rather than a copy
// that would silently drop a field added to one and not the other.
func subscriptionInputFrom(body subscriptionBody) SubscriptionInput {
	return SubscriptionInput(body)
}

// resolveOwner tells the authorization layer which tenant a subscription belongs
// to, so a caller cannot reach another tenant's subscription by its identifier.
func (s *Service) resolveOwner(ctx context.Context, id guid.ID) (authz.ResourceOwner, bool, error) {
	if s == nil {
		return authz.ResourceOwner{}, false, ErrUnavailable
	}
	row, err := s.repo.subscriptionOwner(ctx, id)
	if errors.Is(err, ErrSubscriptionNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	return authz.ResourceOwner{TenantID: row}, true, nil
}

func webhookError(err error) error {
	if respx.IsStatusError(err) {
		return err
	}
	switch {
	case errors.Is(err, ErrSubscriptionNotFound):
		return huma.Error404NotFound(webhooki18n.SubscriptionMissing)
	case errors.Is(err, ErrSubscriptionNameTaken):
		return huma.Error409Conflict(webhooki18n.SubscriptionExists)
	case errors.Is(err, ErrInvalidSubscription):
		return huma.Error422UnprocessableEntity(webhooki18n.InvalidRequest)
	case errors.Is(err, ErrUnavailable), errors.Is(err, errRepositoryUnavailable):
		return huma.Error503ServiceUnavailable(webhooki18n.Unavailable, err)
	default:
		return huma.Error500InternalServerError(webhooki18n.Unavailable, err)
	}
}
