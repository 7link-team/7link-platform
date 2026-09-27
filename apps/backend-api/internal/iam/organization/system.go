package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/iam/organization/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) ensureSystemRoot(ctx context.Context) error {
	// Standalone module fixtures may omit the optional core menu capability.
	if m.bootstrap.SeedSystemMenus == nil {
		return nil
	}
	table := query.BindNames(m.runtime.TablePrefix()).Tenants.String()
	return m.runtime.Database().Write().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var root model.Tenant
		err := tx.Table(table).Where("slug = ?", core.SystemRootSlug).Take(&root).Error
		if err == nil {
			if root.ScopeKind != ScopeSystem || root.Status != TenantActive {
				return fmt.Errorf("invalid reserved system root")
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		id, err := m.ids.Next()
		if err != nil {
			return err
		}
		if id <= 1 {
			return fmt.Errorf("invalid generated system root ID")
		}
		now := organizationTime(time.Now())
		root = model.Tenant{ID: guid.ID(id), ScopeKind: ScopeSystem, Slug: core.SystemRootSlug, Name: "System", Status: TenantActive, Version: 1, CreatedAt: now, UpdatedAt: now}
		insert := tx.Table(table).Clauses(clause.OnConflict{DoNothing: true}).Create(&root)
		if insert.Error != nil {
			return insert.Error
		}
		// Read into a fresh model: GORM otherwise adds the candidate primary key.
		// Affected rows cannot distinguish MySQL no-op inserts with found rows.
		var stored model.Tenant
		if err := tx.Table(table).Clauses(clause.Locking{Strength: "UPDATE"}).Where("slug = ? AND scope_kind = ? AND status = ?", core.SystemRootSlug, ScopeSystem, TenantActive).Take(&stored).Error; err != nil {
			return err
		}
		if stored.ID != root.ID {
			return nil
		}
		if err := m.bootstrap.SeedSystemMenus(ctx, tx, root.ID, now); err != nil {
			return err
		}
		event := audit.NewDomainEvent(ctx, 0, "system_menu_bootstrapped", "system", root.ID)
		event.Detail["scope_kind"], event.Detail["scope_id"] = ScopeSystem, root.ID.String()
		return m.appender(ctx, tx, event)
	})
}
