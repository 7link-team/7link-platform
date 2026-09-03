package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/core/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RevisionStore operates on the core module's IAM policy revision table. The
// prefix must be the same deployment prefix used by the module migration.
type RevisionStore struct {
	table string
}

// NewRevisionStore validates prefix before it can become part of a SQL table
// name. Identifiers cannot be represented by bind parameters.
func NewRevisionStore(prefix string) (*RevisionStore, error) {
	if err := goosex.ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	return &RevisionStore{table: prefix + model.PolicyRevision{}.TableName()}, nil
}

// Current returns the tenant's revision, or zero when no policy mutation has
// created a revision row yet.
func (store *RevisionStore) Current(ctx context.Context, db *gorm.DB, tenantID guid.ID) (int64, error) {
	if err := store.validate(db, tenantID); err != nil {
		return 0, fmt.Errorf("invalid policy revision query: %w", err)
	}
	var row model.PolicyRevision
	err := db.WithContext(ctx).Table(store.table).
		Select("revision").Where("tenant_id = ?", tenantID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get IAM policy revision: %w", err)
	}
	return row.Revision, nil
}

// Lock serializes policy mutations for one tenant without advancing its
// revision. The caller must pass a transaction when the lock must cover more
// than this method call.
func (store *RevisionStore) Lock(ctx context.Context, db *gorm.DB, tenantID guid.ID) error {
	if err := store.validate(db, tenantID); err != nil {
		return fmt.Errorf("invalid policy revision lock: %w", err)
	}
	database := db.WithContext(ctx).Table(store.table)
	row := model.PolicyRevision{
		TenantID:  tenantID,
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := database.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("lock IAM policy revision: ensure row: %w", err)
	}
	if err := database.Where("tenant_id = ?", tenantID).
		UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
		return fmt.Errorf("lock IAM policy revision: %w", err)
	}
	return nil
}

// Bump atomically increments the tenant revision and records updatedAt. A
// single upsert prevents concurrent writers from losing increments.
func (store *RevisionStore) Bump(ctx context.Context, db *gorm.DB, tenantID guid.ID, updatedAt time.Time) error {
	if err := store.validate(db, tenantID); err != nil {
		return fmt.Errorf("invalid policy revision bump: %w", err)
	}
	if updatedAt.IsZero() {
		return errors.New("invalid policy revision bump: updated time is required")
	}
	updatedAt = updatedAt.UTC().Truncate(time.Millisecond)
	row := model.PolicyRevision{TenantID: tenantID, Revision: 1, UpdatedAt: updatedAt}
	err := db.WithContext(ctx).Table(store.table).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"revision": gorm.Expr(
				"? + 1", clause.Column{Table: store.table, Name: "revision"},
			),
			"updated_at": updatedAt,
		}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("advance IAM policy revision: %w", err)
	}
	return nil
}

func (store *RevisionStore) validate(db *gorm.DB, tenantID guid.ID) error {
	if store == nil {
		return errors.New("store is nil")
	}
	if db == nil {
		return errors.New("database is nil")
	}
	if tenantID <= 0 {
		return errors.New("tenant ID must be positive")
	}
	return nil
}
