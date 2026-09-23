package goosex

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var tableNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// VersionApplied reports the latest recorded state of one migration version.
// Goose owns this table, so its schema access stays inside this adapter instead
// of leaking framework column names into application services.
func VersionApplied(ctx context.Context, db *gorm.DB, source Source, version int64) (bool, error) {
	if db == nil {
		return false, errors.New("goose: nil database")
	}
	if version <= 0 {
		return false, fmt.Errorf("goose: version must be positive: %d", version)
	}
	if err := ValidatePrefix(source.Prefix); err != nil {
		return false, err
	}
	if !tableNamePattern.MatchString(source.TableName) {
		return false, fmt.Errorf("goose: invalid version table %q", source.TableName)
	}
	var state struct {
		VersionID int64
		IsApplied bool
	}
	err := db.WithContext(ctx).
		Table(source.VersionTable()).
		Where(map[string]any{"version_id": version}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).
		Take(&state).Error
	if err != nil {
		return false, fmt.Errorf("goose: read migration version %d: %w", version, err)
	}
	return state.VersionID == version && state.IsApplied, nil
}
