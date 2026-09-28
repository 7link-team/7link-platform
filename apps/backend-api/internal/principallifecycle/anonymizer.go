package principallifecycle

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
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

// AnonymizePrincipalData erases principal-owned data from every persisted IAM
// owner, including modules that are disabled at runtime. registry is the shared
// authorization registry core's platform-access check reads.
func AnonymizePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, registry *authz.Registry, principal identity.Principal, now time.Time) error {
	if tx == nil || registry == nil {
		return identity.ErrUnavailable
	}
	eraseCore := func(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, now time.Time) error {
		return core.ErasePrincipalData(ctx, tx, prefix, registry, id, now)
	}
	// The version tables are named literally rather than read from each
	// module's MigrationSource: reaching for the concrete Module type is a
	// cross-module reference the architecture rules forbid. The prefix is not
	// applied here either - goose.Source owns that, so the name is bound in one
	// audited place instead of being rebuilt at this call site.
	owners := []struct {
		name, versions string
		version        int64
		erase          func(context.Context, *gorm.DB, string, guid.ID, time.Time) error
	}{
		{core.Name, "goose_iam", 13, eraseCore},
		{authn.Name, "goose_authn", 13, authn.ErasePrincipalData},
		{oauth.Name, "goose_oauth", 9, oauth.ErasePrincipalData},
		{federation.Name, "goose_federation", 5, federation.ErasePrincipalData},
		{provisioning.Name, "goose_provisioning", 5, provisioning.ErasePrincipalData},
		{organization.Name, "goose_organization", 9, organization.ErasePrincipalData},
		{governance.Name, "goose_governance", 4, governance.ErasePrincipalData},
	}
	for _, owner := range owners {
		applied, err := goosex.VersionApplied(ctx, tx, goosex.Source{Prefix: prefix, TableName: owner.versions}, owner.version)
		if err != nil || !applied {
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
