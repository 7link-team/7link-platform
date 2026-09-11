package core

import (
	"context"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// TenantPersonalTokenRevoker withdraws only the departing membership's PATs.
// The caller holds the principal lock and owns the transaction.
type TenantPersonalTokenRevoker func(context.Context, *gorm.DB, guid.ID, guid.ID) error

// TenantOAuthRevoker withdraws the departing membership's grants and tokens.
// The caller holds the principal lock and owns the transaction.
type TenantOAuthRevoker func(context.Context, *gorm.DB, guid.ID, guid.ID) error

type MyMembership struct {
	TenantID   guid.ID `json:"tenant_id"`
	TenantName string  `json:"tenant_name"`
	Status     string  `json:"status"`
}

func (s *Service) MyMemberships(ctx context.Context, principalID guid.ID, offset, limit int) ([]MyMembership, int64, error) {
	if principalID <= 0 || offset < 0 || limit < 1 || limit > 200 {
		return nil, 0, ErrInvalidArgument
	}
	tables, db, err := s.repo.tables()
	if err != nil {
		return nil, 0, err
	}
	directory, err := s.repo.directoryTables()
	if err != nil {
		return nil, 0, err
	}
	query := db.WithContext(ctx).Table(tables.TenantMembers.TableName()+" AS membership").
		Joins("JOIN "+directory.Tenants.String()+" AS tenant ON tenant.id = membership.tenant_id AND tenant.status = 'active'").
		Where("membership.principal_id = ? AND membership.status = ?", principalID, MemberActive)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := []MyMembership{}
	err = query.Select("membership.tenant_id, tenant.name AS tenant_name, membership.status").Order("membership.tenant_id ASC").Offset(offset).Limit(limit).Scan(&items).Error
	return items, total, err
}

// LeaveTenant preserves the global browser session and all other memberships.
func (s *Service) LeaveTenant(ctx context.Context, principalID, tenantID guid.ID, cookie, proof string) (TenantMember, error) {
	if s.reauthenticate == nil || s.revokePersonalTokens == nil || s.revokeOAuth == nil {
		return TenantMember{}, ErrUnavailable
	}
	return s.setTenantMemberStatus(ctx, tenantID, principalID, MemberDisabled, func(repo *Repository) error {
		if err := s.reauthenticate(ctx, repo.writer(), principalID, cookie, proof, "tenant_leave", tenantID.String()); err != nil {
			return err
		}
		member, err := repo.GetMember(ctx, tenantID, principalID)
		if err != nil {
			return err
		}
		if member.Status != MemberActive {
			return ErrInvalidArgument
		}
		if err := s.revokePersonalTokens(ctx, repo.writer(), tenantID, principalID); err != nil {
			return err
		}
		return s.revokeOAuth(ctx, repo.writer(), tenantID, principalID)
	})
}
