package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

const (
	BindingUnknown           = "unknown"
	BindingNewPrincipal      = "new_principal"
	BindingPrincipalVerified = "principal_verified"
)

var ErrIdentityOwnershipRequired = errors.New("independent account ownership proof is required")

func securityProviderFingerprint(provider providerRow) string {
	encoded, _ := json.Marshal([]string{provider.ProviderType, provider.Issuer, provider.ClientID})
	return identityOperationHash(string(encoded))
}

func (s *Service) bindingFingerprint(ctx context.Context, provider providerRow) (string, error) {
	fingerprint := securityProviderFingerprint(provider)
	if provider.ProviderType != ProviderSAML {
		return fingerprint, nil
	}
	metadata, err := s.samlIDPMetadata(ctx, provider)
	if err != nil {
		return "", err
	}
	// Bind SAML identity and signing trust, excluding metadata expiry/display.
	material := []any{fingerprint, metadata.EntityID}
	for _, descriptor := range metadata.IDPSSODescriptors {
		material = append(material, descriptor.KeyDescriptors)
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	return identityOperationHash(string(encoded)), nil
}

func trustedIdentityLink(link identityLinkRow, fingerprint string) bool {
	return (link.BindingProvenance == BindingNewPrincipal || link.BindingProvenance == BindingPrincipalVerified) &&
		link.SecurityFingerprint != "" && link.SecurityFingerprint == fingerprint && link.QuarantinedAt == nil
}

// lockProvider takes a write lock even on SQLite and returns a current read.
func (repo *Repository) lockProvider(ctx context.Context, providerID guid.ID) (providerRow, error) {
	if err := repo.writer().WithContext(ctx).Table(repo.names.Providers.String()).Where("id = ?", providerID).
		Update("updated_at", gorm.Expr("updated_at")).Error; err != nil {
		return providerRow{}, err
	}
	return repo.getProviderByID(ctx, providerID)
}

type LegacyIdentityInventory struct {
	PrincipalID         guid.ID `json:"principal_id"`
	LinkCount           int64   `json:"link_count"`
	UnresolvedLinkCount int64   `json:"unresolved_link_count"`
}

type LegacyIdentityMaintenance interface {
	InventoryLegacyIdentityLinks(context.Context) ([]LegacyIdentityInventory, error)
	RecordLegacyOwnershipEvidence(context.Context, guid.ID, string) error
	QuarantineLegacyIdentityLinks(context.Context) error
}

// InventoryLegacyIdentityLinks is a release gate input, never a trust backfill.
func (s *Service) InventoryLegacyIdentityLinks(ctx context.Context) ([]LegacyIdentityInventory, error) {
	rows := []LegacyIdentityInventory{}
	err := s.repo.writer().WithContext(ctx).Table(s.repo.names.IdentityLinks.String()).
		Select("principal_id, COUNT(*) AS link_count, SUM(CASE WHEN recovery_evidence = '' THEN 1 ELSE 0 END) AS unresolved_link_count").
		Where("binding_provenance = ? OR binding_provenance = ''", BindingUnknown).Group("principal_id").Order("principal_id").Scan(&rows).Error
	return rows, err
}

// RecordLegacyOwnershipEvidence records an operator's reviewed evidence. Callers
// must authorize the operator; this never makes a binding trusted or issues proof.
func (s *Service) RecordLegacyOwnershipEvidence(ctx context.Context, principalID guid.ID, evidence string) error {
	evidence = strings.TrimSpace(evidence)
	if principalID.Zero() || evidence == "" || len(evidence) > 4096 {
		return ErrIdentityOwnershipRequired
	}
	return s.repo.runInTx(ctx, func(repo *Repository) error {
		if repo.principals.Lock == nil {
			return ErrUnavailable
		}
		if err := repo.principals.Lock(ctx, repo.writer(), principalID); err != nil {
			return err
		}
		result := repo.writer().WithContext(ctx).Table(repo.names.IdentityLinks.String()).
			Where("principal_id = ? AND (binding_provenance = ? OR binding_provenance = '')", principalID, BindingUnknown).
			Update("recovery_evidence", evidence)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrIdentityOwnershipRequired
		}
		event := audit.NewDomainEvent(ctx, 0, "legacy_identity_ownership_evidence_recorded", "principal", principalID)
		event.Detail["evidence"] = evidence
		return s.audit(ctx, repo.writer(), event)
	})
}

func (s *Service) recoveryEligible(ctx context.Context, tx *gorm.DB, principalID guid.ID) error {
	var unresolved int64
	err := tx.WithContext(ctx).Table(s.repo.names.IdentityLinks.String()).
		Where("principal_id = ? AND (binding_provenance = ? OR binding_provenance = '') AND recovery_evidence = ''", principalID, BindingUnknown).
		Count(&unresolved).Error
	if err != nil {
		return err
	}
	if unresolved != 0 {
		return authnext.ErrInvalidRecovery
	}
	return nil
}

func (s *Service) proofSourceTrusted(ctx context.Context, tx *gorm.DB, principalID guid.ID, linkID, fingerprint string) error {
	providerID, subject, err := parseIdentityLinkID(linkID)
	if err != nil {
		return authnext.ErrInvalidCredentials
	}
	repo := s.repo.withDB(tx, true)
	provider, err := repo.lockProvider(ctx, providerID)
	if err != nil {
		return err
	}
	if provider.Status != ProviderActive {
		return authnext.ErrInvalidCredentials
	}
	current, err := s.bindingFingerprint(ctx, provider)
	if err != nil {
		return err
	}
	link, err := repo.identityLink(ctx, providerID, provider.TenantID, subject)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authnext.ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	if current != fingerprint || link.PrincipalID != principalID || !trustedIdentityLink(link, current) {
		return authnext.ErrInvalidCredentials
	}
	return repo.verifyPrincipalAndMember(ctx, provider.TenantID, principalID)
}

func (s *Service) invalidatePrincipalOn(ctx context.Context, repo *Repository, principalID guid.ID, now time.Time) error {
	if repo.principals.Lock == nil || s.revokeCredentials == nil || s.revokeOAuth == nil {
		return ErrUnavailable
	}
	if err := repo.principals.Lock(ctx, repo.writer(), principalID); err != nil {
		return err
	}
	if err := s.revokeCredentials(ctx, repo.writer(), principalID); err != nil {
		return err
	}
	if err := s.revokeOAuth(ctx, repo.writer(), principalID); err != nil {
		return err
	}
	return repo.writer().WithContext(ctx).Table(repo.names.IdentityLinkTransactions.String()).
		Where("principal_id = ? AND consumed_at IS NULL", principalID).Update("consumed_at", now).Error
}

// QuarantineLegacyIdentityLinks invalidates once before the server accepts
// traffic. Unknown links remain stored and unusable until independent repair.
func (s *Service) QuarantineLegacyIdentityLinks(ctx context.Context) error {
	var principals []guid.ID
	if err := s.repo.writer().WithContext(ctx).Table(s.repo.names.IdentityLinks.String()).
		Distinct("principal_id").Where("(binding_provenance = ? OR binding_provenance = '') AND quarantined_at IS NULL", BindingUnknown).
		Order("principal_id").Pluck("principal_id", &principals).Error; err != nil {
		return err
	}
	for _, principalID := range principals {
		if err := s.repo.runInTx(ctx, func(repo *Repository) error {
			now := s.now().UTC()
			if repo.principals.Lock == nil {
				return ErrUnavailable
			}
			if err := repo.principals.Lock(ctx, repo.writer(), principalID); err != nil {
				return err
			}
			var pending int64
			if err := repo.writer().WithContext(ctx).Table(repo.names.IdentityLinks.String()).
				Where("principal_id = ? AND (binding_provenance = ? OR binding_provenance = '') AND quarantined_at IS NULL", principalID, BindingUnknown).Count(&pending).Error; err != nil {
				return err
			}
			if pending == 0 {
				return nil
			}
			if err := s.invalidatePrincipalOn(ctx, repo, principalID, now); err != nil {
				return err
			}
			if err := repo.writer().WithContext(ctx).Table(repo.names.IdentityLinks.String()).
				Where("principal_id = ? AND (binding_provenance = ? OR binding_provenance = '') AND quarantined_at IS NULL", principalID, BindingUnknown).
				Update("quarantined_at", now).Error; err != nil {
				return err
			}
			return s.audit(ctx, repo.writer(), audit.NewDomainEvent(ctx, 0, "legacy_identity_quarantined", "principal", principalID))
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) InventoryLegacyIdentityLinks(ctx context.Context) ([]LegacyIdentityInventory, error) {
	if m == nil || m.service == nil {
		return nil, ErrUnavailable
	}
	return m.service.InventoryLegacyIdentityLinks(ctx)
}

func (m *Module) RecordLegacyOwnershipEvidence(ctx context.Context, id guid.ID, evidence string) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	return m.service.RecordLegacyOwnershipEvidence(ctx, id, evidence)
}

func (m *Module) QuarantineLegacyIdentityLinks(ctx context.Context) error {
	if m == nil || m.service == nil {
		return ErrUnavailable
	}
	return m.service.QuarantineLegacyIdentityLinks(ctx)
}
