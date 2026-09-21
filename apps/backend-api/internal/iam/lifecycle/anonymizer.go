package lifecycle

import (
	"context"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// AnonymizePrincipalData erases principal-owned data from every persisted IAM
// owner, including modules that are disabled at runtime.
func AnonymizePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, principal identity.Principal, now time.Time) error {
	if tx == nil {
		return identity.ErrUnavailable
	}
	owners := []struct {
		name, versions string
		version        int64
		erase          func(context.Context, *gorm.DB, string, guid.ID, time.Time) error
	}{
		{core.Name, "goose_iam", 13, core.ErasePrincipalData},
		{authn.Name, "goose_authn", 13, authn.ErasePrincipalData},
		{oauth.Name, "goose_oauth", 9, oauth.ErasePrincipalData},
		{federation.Name, "goose_federation", 5, federation.ErasePrincipalData},
		{provisioning.Name, "goose_provisioning", 5, provisioning.ErasePrincipalData},
		{organization.Name, "goose_organization", 9, organization.ErasePrincipalData},
		{governance.Name, "goose_governance", 4, governance.ErasePrincipalData},
	}
	for _, owner := range owners {
		var version struct {
			VersionID int64
			IsApplied bool
		}
		err := tx.WithContext(ctx).Table(prefix+owner.versions).Where("version_id = ?", owner.version).Order("id DESC").Take(&version).Error
		if err != nil || !version.IsApplied || version.VersionID != owner.version {
			return fmt.Errorf("%w: %s principal erasure migration is required", identity.ErrUnavailable, owner.name)
		}
	}
	for _, owner := range owners {
		if err := owner.erase(ctx, tx, prefix, principal.ID, now); err != nil {
			return fmt.Errorf("erase %s principal data: %w", owner.name, err)
		}
	}
	return nil
}
