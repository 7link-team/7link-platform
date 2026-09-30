package provisioning

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/iam/provisioning/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// ErrPrincipalRemoteProvisioned prevents losing the identifier needed to
// deprovision a live remote account through the existing outbound workflow.
var ErrPrincipalRemoteProvisioned = errors.New("deprovision remote principal accounts before anonymization")

// PrincipalErasureVersion is the schema version ErasePrincipalData needs: the
// later of this module's principal-erasure guard migration and the migration
// that created the newest table erasure touches. Raise it when erasure reaches
// a later table: the principallifecycle erasure-version test fails until then.
const PrincipalErasureVersion = 5

// ErasePrincipalData tombstones SCIM User mappings in the caller's transaction.
// The suspended principal must already be locked; Group mappings are untouched.
func ErasePrincipalData(ctx context.Context, tx *gorm.DB, prefix string, id guid.ID, now time.Time) error {
	if tx == nil || id <= 0 {
		return errors.New("invalid principal erasure transaction")
	}
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return err
	}
	names := query.BindNames(prefix)
	resource, targetResource := names.Resources, names.TargetResources
	var count int64
	if err := tx.WithContext(ctx).Table(targetResource.String()).
		Where("resource_type = 'User' AND resource_id = ? AND deleted_at IS NULL AND external_id <> ''", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrPrincipalRemoteProvisioned
	}
	for _, table := range []gormx.TableName{resource, targetResource} {
		updates := map[string]any{"external_id": "", "deleted_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}
		if table == resource {
			updates["external_key"] = "erased:" + id.String()
		}
		if err := tx.WithContext(ctx).Table(table.String()).Where("resource_type = 'User' AND resource_id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
