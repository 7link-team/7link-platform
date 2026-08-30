package audit

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

// ErrDatabaseUnavailable is returned when a migration target database is nil.
var ErrDatabaseUnavailable = errors.New("audit: writable database unavailable")

//go:embed sql/sqlite sql/mysql sql/postgres
var migrationsFS embed.FS

// MigrationSource returns the audit module's migration source.
func (*Module) MigrationSource() goosex.Source {
	return migrationSource()
}

func migrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_audit"}
}

func Migrate(ctx context.Context, db *gorm.DB) error {
	return runMigration(ctx, db, goosex.Migrate)
}

func MigrateUp(ctx context.Context, db *gorm.DB) error {
	return runMigration(ctx, db, goosex.MigrateUp)
}

func MigrateDown(ctx context.Context, db *gorm.DB) error {
	return runMigration(ctx, db, goosex.MigrateDown)
}

type migrationOperation func(context.Context, *sql.DB, goosex.Source, string) error

func runMigration(ctx context.Context, db *gorm.DB, operation migrationOperation) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("audit: get sql database: %w", err)
	}
	return operation(ctx, sqlDB, migrationSource(), db.Dialector.Name())
}
