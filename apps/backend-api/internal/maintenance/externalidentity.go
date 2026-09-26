package maintenance

import (
	"context"
	"errors"
	"strings"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ErrLegacyIdentityUnresolved prevents release while ownership evidence is missing.
var ErrLegacyIdentityUnresolved = errors.New("legacy external identity ownership remains unresolved")

// RunExternalIdentityInventory reports the owner module's deployment gate.
// It does not start servers, quarantine accounts or promote any binding.
func RunExternalIdentityInventory(ctx context.Context, cfg app.AppConfig) ([]federation.LegacyIdentityInventory, error) {
	var inventory []federation.LegacyIdentityInventory
	err := app.RunPreparedMaintenance(ctx, cfg, func(services *module.Services, _ *gorm.DB, _ *dlock.Module) error {
		owner, err := module.Resolve[federation.LegacyIdentityMaintenance](services)
		if err != nil {
			return err
		}
		inventory, err = owner.InventoryLegacyIdentityLinks(ctx)
		return err
	})
	if err != nil {
		return nil, normalizeMaintenanceError(err)
	}
	for _, principal := range inventory {
		if principal.UnresolvedLinkCount > 0 {
			return inventory, ErrLegacyIdentityUnresolved
		}
	}
	return inventory, nil
}

// RecordExternalIdentityEvidence uses host operator authority, like the existing
// administrator recovery command. Evidence never makes an external link trusted.
func RecordExternalIdentityEvidence(ctx context.Context, cfg app.AppConfig, id guid.ID, evidence string) error {
	if id.Zero() || strings.TrimSpace(evidence) == "" || len(evidence) > 4096 {
		return federation.ErrIdentityOwnershipRequired
	}
	return app.RunPreparedMaintenance(ctx, cfg, func(services *module.Services, _ *gorm.DB, _ *dlock.Module) error {
		owner, err := module.Resolve[federation.LegacyIdentityMaintenance](services)
		if err != nil {
			return err
		}
		return owner.RecordLegacyOwnershipEvidence(ctx, id, evidence)
	})
}

// QuarantineExternalIdentities applies the owner cutover with old replicas stopped.
func QuarantineExternalIdentities(ctx context.Context, cfg app.AppConfig) error {
	return app.RunPreparedMaintenance(ctx, cfg, func(services *module.Services, _ *gorm.DB, _ *dlock.Module) error {
		owner, err := module.Resolve[federation.LegacyIdentityMaintenance](services)
		if err != nil {
			return err
		}
		return owner.QuarantineLegacyIdentityLinks(ctx)
	})
}
