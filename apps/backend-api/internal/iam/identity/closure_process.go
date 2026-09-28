package identity

import (
	"context"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
)

// ProcessClosureRequest suspends one requested global account, including a
// principal with no memberships. Retention and anonymization remain separate.
func (s *Service) ProcessClosureRequest(ctx context.Context, id guid.ID, version int64) (ClosureRequest, error) {
	if id <= 0 || version < 1 {
		return ClosureRequest{}, ErrInvalid
	}
	if s.closeCredentials == nil || s.closeOAuth == nil || s.platformAccessGuard == nil {
		return ClosureRequest{}, ErrUnavailable
	}
	var result ClosureRequest
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		principal, err := repo.lifecyclePrincipal(ctx, id, true)
		if err != nil {
			return err
		}
		request, err := repo.closureRequest(ctx, id)
		if err != nil {
			return err
		}
		if request.Status != "pending" || request.Version != version {
			return ErrClosureConflict
		}
		if err := s.platformAccessGuard(ctx, repo.writer(), id); err != nil {
			return err
		}
		tenants, err := repo.activeTenantIDs(ctx, id)
		if err != nil {
			return err
		}
		verifiers := make([]func() error, 0, len(tenants))
		for _, tenant := range tenants {
			verify, err := s.administrators.Protect(ctx, repo.writer(), tenant)
			if err != nil {
				return err
			}
			verifiers = append(verifiers, verify)
		}
		now, err := lifecycleNow(ctx, repo.writer())
		if err != nil {
			return err
		}
		if err := repo.updatePrincipalRow(ctx, id, map[string]any{"status": "disabled", "disabled_at": now, "updated_at": now}); err != nil {
			return err
		}
		if err := repo.revokeAllSecurityState(ctx, id, now); err != nil {
			return err
		}
		if err := s.closeCredentials(ctx, repo.writer(), id); err != nil {
			return err
		}
		if err := s.closeOAuth(ctx, repo.writer(), id); err != nil {
			return err
		}
		for _, verify := range verifiers {
			if err := verify(); err != nil {
				return err
			}
		}
		tables, _, err := repo.tables()
		if err != nil {
			return err
		}
		update, err := tables.ClosureRequests.WithContext(ctx).Where(tables.ClosureRequests.PrincipalID.Eq(id), tables.ClosureRequests.Version.Eq(version), tables.ClosureRequests.Status.Eq("pending")).Updates(map[string]any{"status": "processing", "version": version + 1, "updated_at": now})
		if err != nil {
			return err
		}
		if update.RowsAffected != 1 {
			return ErrClosureConflict
		}
		event := audit.NewDomainEvent(ctx, 0, "principal_closure_processing", "principal", id)
		event.Detail["version"], event.Detail["previous_status"] = version+1, principal.Status
		if err := repo.appendAudit(ctx, event); err != nil {
			return err
		}
		request.Status, request.Version, request.UpdatedAt = "processing", version+1, &now
		result = request
		return nil
	})
	return result, err
}
