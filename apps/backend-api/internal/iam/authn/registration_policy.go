package authn

import (
	"context"
	"errors"
	"fmt"

	"7link.com/platform/internal/iam/authn/model"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RegistrationPolicyView separates the desired policy from runtime availability.
type RegistrationPolicyView struct {
	Enabled   bool `json:"enabled"`
	Available bool `json:"available"`
}

func (s *Service) registrationReady() bool {
	if s == nil || !s.Enabled() {
		return false
	}
	if !s.cfg.EmailVerification.Enabled {
		return s.directRegistrationCreator != nil
	}
	return s.EmailVerificationEnabled() && s.NotificationEnabled() && s.registrationCreator != nil
}

func (s *Service) registrationPolicy(ctx context.Context) (bool, error) {
	if s == nil || s.repo == nil {
		return false, authnext.ErrUnavailable
	}
	q, _, err := s.repo.tables()
	if err != nil {
		return false, err
	}
	row, err := q.RegistrationPolicy.WithContext(ctx).Where(q.RegistrationPolicy.ID.Eq(1)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.cfg.Registration.Enabled, nil
	}
	if err != nil {
		return false, fmt.Errorf("read registration policy: %w", err)
	}
	return row.Enabled, nil
}

func (s *Service) RegistrationPolicy(ctx context.Context) (RegistrationPolicyView, error) {
	enabled, err := s.registrationPolicy(ctx)
	if err != nil {
		return RegistrationPolicyView{}, err
	}
	return RegistrationPolicyView{Enabled: enabled, Available: s.registrationReady()}, nil
}

// SetRegistrationPolicy persists the system override and its audit atomically.
func (s *Service) SetRegistrationPolicy(ctx context.Context, enabled bool) (RegistrationPolicyView, error) {
	if s == nil || s.repo == nil {
		return RegistrationPolicyView{}, authnext.ErrUnavailable
	}
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		q, _, err := repo.tables()
		if err != nil {
			return err
		}
		row := model.RegistrationPolicy{ID: 1, Enabled: enabled, UpdatedAt: s.now().UTC(), UpdatedBy: authnext.PrincipalIDFromContext(ctx)}
		if err := q.RegistrationPolicy.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at", "updated_by"}),
		}).Create(&row); err != nil {
			return err
		}
		event := securityAudit(row.UpdatedBy, "registration_policy_updated", "success")
		event.Detail["enabled"] = enabled
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return RegistrationPolicyView{}, err
	}
	return RegistrationPolicyView{Enabled: enabled, Available: s.registrationReady()}, nil
}
