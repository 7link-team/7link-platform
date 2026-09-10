package oauth

import (
	"context"
	"errors"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// revokeTenantGrantsOn shares the principal lock/transaction with tenant exit.
func (s *Service) revokeTenantGrantsOn(ctx context.Context, tx *gorm.DB, tenantID, principalID guid.ID) error {
	return s.revokePrincipalGrantsOn(ctx, tx, principalID, tenantID)
}

// A zero tenant means all applications for global closure processing.
func (s *Service) revokePrincipalGrantsOn(ctx context.Context, tx *gorm.DB, principalID, tenantID guid.ID) error {
	if s == nil || s.repo == nil || tx == nil {
		return ErrUnavailable
	}
	repo := s.repo.withDB(tx, true)
	tables, _, err := repo.tables()
	if err != nil {
		return err
	}
	// Include clients with only pending requests or historic tokens, not just
	// currently visible consents; rejoining must not revive any of these.
	var after guid.ID
	for {
		query := tables.Clients.WithContext(ctx).Select(tables.Clients.ID).Where(tables.Clients.ID.Gt(after)).Order(tables.Clients.ID).Limit(200)
		if tenantID > 0 {
			query = query.Where(tables.Clients.TenantID.Eq(tenantID))
		}
		clients, err := query.Find()
		if err != nil {
			return err
		}
		if len(clients) == 0 {
			break
		}
		for _, client := range clients {
			clientID := client.ID
			consent, err := repo.lockConsent(ctx, principalID, clientID)
			if err != nil {
				return err
			}
			if err := repo.revokeGrantedTokens(ctx, principalID, clientID, oauthTime(s.now())); err != nil {
				return err
			}
			if consent != nil {
				if err := repo.deleteConsent(ctx, principalID, clientID); err != nil {
					return err
				}
			}
			if _, err := tables.ConsentRequests.WithContext(ctx).Where(tables.ConsentRequests.PrincipalID.Eq(principalID), tables.ConsentRequests.ClientID.Eq(clientID), tables.ConsentRequests.ResolvedAt.IsNull()).Update(tables.ConsentRequests.ResolvedAt, oauthTime(s.now())); err != nil {
				return err
			}
			after = clientID
		}
	}
	event := audit.NewDomainEvent(ctx, tenantID, "oauth_membership_grants_revoked", "principal", principalID)
	return repo.appendAudit(ctx, event)
}

// lockGrantAdmission serializes new grant creation with tenant exit and global
// closure. A request authenticated before suspension cannot write a fresh grant
// after processing, including after a later administrator restoration.
func (repo *Repository) lockGrantAdmission(ctx context.Context, claims *authnext.Claims, tenantID guid.ID) error {
	db := repo.writer().WithContext(ctx)
	locked := db.Table(repo.table("iam_principals")).Where("id = ?", claims.PrincipalID).Update("updated_at", gorm.Expr("updated_at"))
	if locked.Error != nil {
		return locked.Error
	}
	// These must be current reads even when an outer consent transaction has
	// already established a MySQL REPEATABLE READ snapshot.
	if db.Dialector.Name() != "sqlite" {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	// Clauses materializes a statement; clone it for each table query so the
	// principal's id predicate cannot leak into membership/credential reads.
	db = db.Session(&gorm.Session{})
	var principal struct{ Status string }
	err := db.Table(repo.table("iam_principals")).Select("status").Where("id = ?", claims.PrincipalID).Take(&principal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInvalidRequest
	}
	if err != nil {
		return err
	}
	if principal.Status != "active" {
		return ErrInvalidRequest
	}
	var member struct{ Status string }
	err = db.Table(repo.table("iam_tenant_members")).Select("status").Where("tenant_id = ? AND principal_id = ?", tenantID, claims.PrincipalID).Take(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrInvalidRequest
	}
	if err != nil {
		return err
	}
	if member.Status != "active" {
		return ErrInvalidRequest
	}
	var credential struct{ CredentialVersion int64 }
	err = db.Table(repo.table("iam_credentials")).Select("credential_version").Where("principal_id = ?", claims.PrincipalID).Take(&credential).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if credential.CredentialVersion != claims.CredentialVersion {
		return ErrInvalidRequest
	}
	return nil
}
