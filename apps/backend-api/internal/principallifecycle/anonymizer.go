package principallifecycle

import (
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/module"
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
	"7link.com/platform/internal/notification"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

type erasureOwner struct {
	name     string
	source   func() goosex.Source
	required int64
	erase    func(context.Context, *gorm.DB, string, guid.ID, time.Time) error
}

// AnonymizePrincipalData erases principal-owned data from every persisted IAM
// owner, including modules that are disabled at runtime. registry is the shared
// authorization registry core's platform-access check reads.
//
// Each owner publishes its own migration stream and the version its erasure
// needs, so nothing here restates what an owner knows about its schema. An
// owner whose stream was never installed holds no records and is skipped; one
// that is installed but behind its erasure version fails the whole erasure
// before anything is written.
func AnonymizePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, registry *authz.Registry, principal identity.Principal, now time.Time) error {
	if tx == nil || registry == nil {
		return identity.ErrUnavailable
	}
	eraseCore := func(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, now time.Time) error {
		return core.ErasePrincipalData(ctx, tx, prefix, registry, id, now)
	}
	owners := []erasureOwner{
		{core.Name, core.MigrationSource, core.PrincipalErasureVersion, eraseCore},
		{authn.Name, authn.MigrationSource, authn.PrincipalErasureVersion, authn.ErasePrincipalData},
		{oauth.Name, oauth.MigrationSource, oauth.PrincipalErasureVersion, oauth.ErasePrincipalData},
		{federation.Name, federation.MigrationSource, federation.PrincipalErasureVersion, federation.ErasePrincipalData},
		{provisioning.Name, provisioning.MigrationSource, provisioning.PrincipalErasureVersion, provisioning.ErasePrincipalData},
		{organization.Name, organization.MigrationSource, organization.PrincipalErasureVersion, organization.ErasePrincipalData},
		{governance.Name, governance.MigrationSource, governance.PrincipalErasureVersion, governance.ErasePrincipalData},
		{notification.Name, notification.MigrationSource, notification.PrincipalErasureVersion, notification.ErasePrincipalData},
	}
	installed := make([]erasureOwner, 0, len(owners))
	for _, owner := range owners {
		source := owner.source()
		source.Prefix = prefix
		version, err := goosex.CurrentVersion(ctx, tx, source)
		if err != nil {
			return fmt.Errorf("read %s migration version: %w", owner.name, err)
		}
		if version == 0 {
			continue
		}
		if version < owner.required {
			return fmt.Errorf("%w: %s principal erasure needs migration %d, schema is at %d",
				identity.ErrUnavailable, owner.name, owner.required, version)
		}
		installed = append(installed, owner)
	}
	for _, owner := range installed {
		if err := owner.erase(ctx, tx, prefix, principal.ID, now); err != nil {
			return fmt.Errorf("erase %s principal data: %w", owner.name, err)
		}
	}
	return nil
}

// ProvideAnonymizer registers the principal lifecycle capability.
func ProvideAnonymizer(services *module.Services, rt *appenv.Runtime) error {
	anonymize := identity.PrincipalAnonymizer(func(ctx context.Context, tx *gorm.DB, principal identity.Principal, now time.Time) error {
		if rt == nil {
			return identity.ErrUnavailable
		}
		guard, err := module.Resolve[core.PrincipalPlatformAccessGuard](services)
		if err != nil {
			return identity.ErrUnavailable
		}
		registrar, err := module.Resolve[*authz.Registrar](services)
		if err != nil || registrar.Registry() == nil {
			return identity.ErrUnavailable
		}
		if err := guard(ctx, tx, principal.ID); err != nil {
			return err
		}
		return AnonymizePrincipalData(ctx, tx, rt.TablePrefix(), registrar.Registry(), principal, now)
	})
	return module.Provide[identity.PrincipalAnonymizer](services, anonymize)

}
