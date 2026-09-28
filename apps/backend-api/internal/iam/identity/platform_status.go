package identity

import (
	"context"
	"net/http"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/danielgtaylor/huma/v2"
)

const platformPrincipalSuspensionPath = "/system/principals/{principal_id}/suspension"

// SetPlatformStatus suspends or restores one principal without naming a tenant.
//
// The two existing routes that can disable a principal each need something this
// one does not: the tenant route needs a membership, and the closure route needs
// a request the person themselves made. An account in no tenant that never asked
// to be closed therefore had no way to reach the suspended state that erasure
// requires, and a platform administrator had no way to disable it at all.
//
// The effect is deliberately the tenant route's -- status and the credentials
// that carry a session -- rather than the closure route's teardown of every
// credential, so that lifting a suspension leaves an account its owner can still
// sign in to. Deleting credentials is what closure is for.
func (s *Service) SetPlatformStatus(ctx context.Context, id guid.ID, status string) (Principal, error) {
	if id.Zero() || status != "active" && status != "disabled" {
		return Principal{}, ErrInvalid
	}
	if s.platformAccessGuard == nil {
		return Principal{}, ErrUnavailable
	}
	now := identityTime(s.now())
	eventType := "principal_restored"
	if status == "disabled" {
		eventType = "principal_disabled"
	}
	// The event names no tenant, because the decision is about a person rather
	// than about their standing in one tenant -- the same reason the lifecycle
	// routes are platform-scoped.
	event := audit.NewDomainEvent(ctx, 0, eventType, "principal", id)
	var updated Principal
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.lifecyclePrincipal(ctx, id, true)
		if err != nil {
			return err
		}
		lifecycle, err := repo.principalLifecycle(ctx, id)
		if err != nil {
			return err
		}
		// A tombstone is permanent and the row is the last thing that says who
		// this was. The database refuses the write too; refusing here names the
		// reason instead of reporting a constraint failure.
		if lifecycle.AnonymizedAt != nil {
			return ErrPrincipalAnonymized
		}
		// Suspension must not remove the last administrator of a tenant the
		// principal still belongs to. An account in no tenant collects no
		// verifier, which is the case this route exists for.
		verifiers := make([]func() error, 0)
		if status == "disabled" && current.Status != "disabled" {
			tenantIDs, err := repo.activeTenantIDs(ctx, id)
			if err != nil {
				return err
			}
			for _, tenantID := range tenantIDs {
				verify, err := s.administrators.Protect(ctx, repo.writer(), tenantID)
				if err != nil {
					return err
				}
				verifiers = append(verifiers, verify)
			}
		}
		if err := s.platformAccessGuard(ctx, repo.writer(), id); err != nil {
			return err
		}
		var disabledAt *time.Time
		if status == "disabled" {
			disabledAt = &now
		}
		if err := repo.updatePrincipalRow(ctx, id, map[string]any{
			"status": status, "disabled_at": disabledAt, "updated_at": now,
		}); err != nil {
			return err
		}
		if status == "disabled" {
			if err := repo.revokeSessionsAndRefreshTokens(ctx, id, now); err != nil {
				return err
			}
		}
		for _, verify := range verifiers {
			if err := verify(); err != nil {
				return err
			}
		}
		event.Detail["changed"] = current.Status != status
		event.Detail["status"] = status
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		current.Status, current.DisabledAt, current.UpdatedAt = status, disabledAt, now
		updated = principalFromRow(current)
		return nil
	})
	if err != nil {
		return Principal{}, err
	}
	return updated, nil
}

func registerPlatformStatusHTTP(api huma.API, service *Service, registrar *authz.Registrar) {
	guard := authz.Guard{Resource: "platform", Verb: "administer"}
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-platform-suspend-principal",
		Method:      http.MethodPut,
		Path:        platformPrincipalSuspensionPath,
		Summary:     "Suspend a principal, including one no tenant administrator can reach",
		Description: "Reaches a principal that holds no membership, which the tenant suspension " +
			"route cannot address and the closure route only covers after the person asks. It " +
			"revokes sessions and refresh tokens and refuses to remove the last administrator of " +
			"a tenant the principal still belongs to.",
		Tags:   []string{"platform"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable},
	}, guard, func(ctx context.Context, in *platformLifecycleInput) (*respx.Body[Principal], error) {
		return platformPrincipalStatus(ctx, service, in, "disabled")
	})
	authz.RegisterPlatform(registrar, api, huma.Operation{
		OperationID: "identity-platform-restore-principal",
		Method:      http.MethodDelete,
		Path:        platformPrincipalSuspensionPath,
		Summary:     "Lift a principal suspension applied from the platform",
		Description: "Restores the account to active without recreating credentials, because " +
			"suspension keeps them. A permanently anonymized principal is refused: its row is a " +
			"tombstone and the database rejects the write as well.",
		Tags:   []string{"platform"},
		Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable},
	}, guard, func(ctx context.Context, in *platformLifecycleInput) (*respx.Body[Principal], error) {
		return platformPrincipalStatus(ctx, service, in, "active")
	})
}

func platformPrincipalStatus(ctx context.Context, service *Service, in *platformLifecycleInput, status string) (*respx.Body[Principal], error) {
	id, err := guid.RequireID(in.PrincipalID)
	if err != nil {
		return nil, err
	}
	principal, err := service.SetPlatformStatus(ctx, id, status)
	if err != nil {
		return nil, identityError(err)
	}
	return respx.OK(ctx, principal), nil
}
