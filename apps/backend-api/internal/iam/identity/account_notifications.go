package identity

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// ServiceAccountDisabler is an internal capability. Callers must authorize the
// domain action and lock the principal before acquiring business locks.
type ServiceAccountDisabler func(context.Context, *gorm.DB, guid.ID, guid.ID) error

func (m *Module) provideAccountNotifications(s *module.Services) error {
	return module.Provide[ServiceAccountDisabler](s, func(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
		if m == nil || m.Service == nil {
			return ErrUnavailable
		}
		return m.Service.disableServiceAccountOn(ctx, tx, tenant, principal)
	})
}

func (s *Service) disableServiceAccountOn(ctx context.Context, tx *gorm.DB, tenant, principal guid.ID) error {
	if s == nil || s.repo == nil || s.administrators == nil || tx == nil || tx.Statement == nil {
		return ErrUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrServiceAccountInvalid
	}
	repo, err := s.repo.on(tx)
	if err != nil {
		return err
	}
	if err = repo.updatePrincipalRow(ctx, principal, map[string]any{"updated_at": gorm.Expr("updated_at")}); err != nil {
		return err
	}
	current, err := repo.getServiceAccount(ctx, tenant, principal, true)
	if err != nil {
		return err
	}
	now := identityTime(s.now())
	verify, err := s.administrators.Protect(ctx, tx, tenant)
	if err != nil {
		return err
	}
	if current.Status != "disabled" {
		if err = repo.replaceServiceAccount(ctx, tenant, principal, current.Description, "disabled", current.ExpiresAt, current.Version, true, now); err != nil {
			return err
		}
	}
	if err = repo.updatePrincipalRow(ctx, principal, map[string]any{"status": "disabled", "disabled_at": now, "updated_at": now}); err != nil {
		return err
	}
	if err = repo.revokeServiceAccountCredentials(ctx, principal, now); err != nil {
		return err
	}
	if err = verify(); err != nil {
		return err
	}
	if err = repo.bumpPolicyRevision(ctx, tenant, now); err != nil {
		return err
	}
	return repo.appendAudit(ctx, audit.NewDomainEvent(ctx, tenant, "service_account_disabled", "service_account", principal))
}

// NotificationCandidates exposes identifiers only. The consumer must still
// verify active human state, tenant membership and current object authority.
type NotificationCandidates func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error)

func (m *Module) notificationCandidates(ctx context.Context, tx *gorm.DB, after guid.ID, limit int) ([]guid.ID, error) {
	if m == nil || m.Service == nil || m.repo == nil || tx == nil || after < 0 || limit < 1 || limit > 200 {
		return nil, ErrUnavailable
	}
	q := query.BindWithNames(tx, m.repo.names).Principals
	rows, err := q.WithContext(ctx).Select(q.ID).Where(q.ID.Gt(after), q.Status.Eq("active")).Order(q.ID).Limit(limit).Find()
	if err != nil {
		return nil, err
	}
	out := make([]guid.ID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out, nil
}
