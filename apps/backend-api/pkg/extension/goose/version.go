package goosex

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var tableNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// CurrentVersion reports the version a migration stream has reached, read
// through the caller's connection so it sees the caller's transaction. A stream
// whose version table does not exist has never been installed and reports 0.
// Goose owns this table, so its schema access stays inside this adapter instead
// of leaking framework column names into application services.
//
// The table's existence is checked before it is read: PostgreSQL aborts the
// enclosing transaction on a query against a missing table, so probing by
// error would leave the caller unable to continue. The check is GetTables, not
// HasTable, because HasTable reports a failed lookup as a missing table, which
// would read as "never installed" rather than as the error it is.
func CurrentVersion(ctx context.Context, db *gorm.DB, source Source) (int64, error) {
	if db == nil {
		return 0, errors.New("goose: nil database")
	}
	if err := ValidatePrefix(source.Prefix); err != nil {
		return 0, err
	}
	if !tableNamePattern.MatchString(source.TableName) {
		return 0, fmt.Errorf("goose: invalid version table %q", source.TableName)
	}
	db = db.WithContext(ctx)
	table := source.VersionTable()
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return 0, fmt.Errorf("goose: list tables: %w", err)
	}
	if !slices.Contains(tables, table) {
		return 0, nil
	}
	var rows []struct {
		VersionID int64
		IsApplied bool
	}
	err = db.Table(table).
		Select("version_id", "is_applied").
		Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).
		Find(&rows).Error
	if err != nil {
		return 0, fmt.Errorf("goose: read migration versions: %w", err)
	}
	// Newest row first: a version whose latest row is a rollback is not current,
	// however many older rows record it as applied.
	rolledBack := map[int64]bool{}
	for _, row := range rows {
		if !row.IsApplied {
			rolledBack[row.VersionID] = true
			continue
		}
		if !rolledBack[row.VersionID] {
			return row.VersionID, nil
		}
	}
	return 0, nil
}
