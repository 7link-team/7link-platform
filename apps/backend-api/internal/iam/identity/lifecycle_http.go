package identity

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	identityi18n "7link.com/platform/internal/iam/identity/i18n"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
)

// The lifecycle routes are platform-scoped, and the path says so. Retention and
// anonymization are decisions about a person rather than about their standing
// in one tenant: a tenant administrator who could reach them would be erasing a
// principal that other tenants also admit, and the record of that erasure is
// global. authz.RegisterPlatform is what enforces it -- no tenant permission
// reaches these operations, which is the property the audit asked to be made
// structural rather than conventional.
const platformPrincipalLifecyclePath = "/platform/iam/principals/{principal_id}/lifecycle"

type platformLifecycleInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
}

type platformRetentionInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		// Version is the policy version this change was decided against. Zero
		// means no policy has ever been recorded, which is how the first
		// retention decision is told apart from an overwrite of someone else's.
		Version     int64     `json:"version" minimum:"0" doc:"lifecycle version this decision was made against; 0 records the first policy"`
		RetainUntil time.Time `json:"retain_until" format:"date-time" doc:"instant after which anonymization is permitted"`
		LegalHold   bool      `json:"legal_hold" doc:"block anonymization regardless of the deadline"`
		Reference   string    `json:"reference" minLength:"1" maxLength:"200" doc:"administrative case identifier; never the subject's own data"`
	}
}

type platformAnonymizeInput struct {
	PrincipalID string `path:"principal_id" pattern:"^[1-9][0-9]*$" maxLength:"19"`
	Body        struct {
		Version int64 `json:"version" minimum:"1" doc:"lifecycle version this erasure was decided against"`
		// Confirmation repeats the principal's current login name. It exists
		// because this cannot be undone and, once done, nothing left in the
		// record says who it was: a caller that cannot name the subject is a
		// caller that has not read which one this is.
		Confirmation string `json:"confirmation" minLength:"1" maxLength:"320" doc:"the principal's current login name, repeated back"`
	}
}

func registerPrincipalLifecycleHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	guard := authz.Guard{Resource: "platform", Verb: "administer"}

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-platform-get-principal-lifecycle",
		Method:      http.MethodGet,
		Path:        platformPrincipalLifecyclePath,
		Summary:     "Read a principal's retention policy and what blocks erasing it",
		Description: "Reports the retention deadline, legal hold, lifecycle version and tombstone state, " +
			"together with the reasons anonymization would be refused right now. Reading never records a " +
			"policy, so looking at a principal does not start a retention clock. Blockers cover what this " +
			"module decides; a module that still holds the principal's data refuses during the attempt and " +
			"names itself, because that is the reason that says where the data still is.",
		Tags:   []string{"identity"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, guard, func(ctx context.Context, in *platformLifecycleInput) (*respx.Body[PrincipalLifecycleStatus], error) {
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		status, err := service.PrincipalLifecycleStatus(ctx, id)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, status), nil
	})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-platform-set-principal-retention",
		Method:      http.MethodPut,
		Path:        platformPrincipalLifecyclePath + "/retention",
		Summary:     "Record how long a principal's data is kept, and whether a hold prevents erasing it",
		Description: "The version is the policy this decision was made against, so two administrators " +
			"deciding from the same screen cannot silently overwrite each other -- the second is told the " +
			"policy moved. Reference is an administrative case identifier and must not repeat the subject's " +
			"own data, which erasure would then be unable to remove.",
		Tags:   []string{"identity"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, guard, func(ctx context.Context, in *platformRetentionInput) (*respx.Body[PrincipalLifecycle], error) {
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		policy, err := service.SetPrincipalRetention(ctx, id, in.Body.Version, in.Body.RetainUntil, in.Body.LegalHold, in.Body.Reference)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, policy), nil
	})

	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-platform-anonymize-principal",
		Method:      http.MethodPost,
		Path:        platformPrincipalLifecyclePath + "/anonymization",
		Summary:     "Irreversibly erase a suspended principal's personal data",
		Description: "This cannot be undone. Every owning module erases what it holds in one transaction, " +
			"and a database tombstone then refuses to let the record be restored. The principal must already " +
			"be suspended, its retention deadline must have passed, no legal hold may stand, and the request " +
			"must repeat the principal's current login name -- checked while the row is locked, so it cannot " +
			"be satisfied by a name read before someone else renamed the principal.",
		Tags:   []string{"identity"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable},
	}, guard, func(ctx context.Context, in *platformAnonymizeInput) (*respx.Body[PrincipalLifecycle], error) {
		id, err := requireID(in.PrincipalID)
		if err != nil {
			return nil, err
		}
		policy, err := service.AnonymizePrincipal(ctx, id, in.Body.Version, in.Body.Confirmation)
		if err != nil {
			return nil, identityError(err)
		}
		return respx.OK(ctx, policy), nil
	})
}

// lifecycleAPIError maps the lifecycle refusals onto statuses an operator can
// act on: a conflict is something to go and change, an unprocessable request is
// something to correct in the request itself.
func lifecycleAPIError(err error) (error, bool) {
	switch {
	case errors.Is(err, ErrLifecycleConflict):
		return huma.Error409Conflict(identityi18n.LifecycleVersionConflict), true
	case errors.Is(err, ErrPrincipalRetained):
		return huma.Error409Conflict(identityi18n.PrincipalRetained), true
	case errors.Is(err, ErrPrincipalAnonymized):
		return huma.Error409Conflict(identityi18n.PrincipalAnonymized), true
	case errors.Is(err, ErrSuspensionRequired):
		return huma.Error409Conflict(identityi18n.SuspensionRequired), true
	case errors.Is(err, ErrConfirmationMismatch):
		return huma.Error422UnprocessableEntity(identityi18n.AnonymizationConfirmation), true
	case errors.Is(err, ErrUnavailable):
		return huma.Error503ServiceUnavailable(identityi18n.IdentityUnavailable), true
	}
	return nil, false
}
