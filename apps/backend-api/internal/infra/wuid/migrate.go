package wuid

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	goosex "7link.com/platform/pkg/extension/goose"
	"gorm.io/gorm"
)

//go:embed sql/sqlite sql/mysql sql/postgres
var migrationsFS embed.FS

func (*Module) MigrationSource() goosex.Source {
	return migrationSource()
}

func migrationSource() goosex.Source { return goosex.Source{FS: migrationsFS, TableName: "goose_wuid"} }

func Migrate(ctx context.Context, db *gorm.DB) error {
	return runMigration(ctx, db, goosex.Migrate)
}

func MigrateUp(ctx context.Context, db *gorm.DB) error {
	return runMigration(ctx, db, goosex.MigrateUp)
}

func MigrateUpTo(ctx context.Context, db *gorm.DB, version int64) error {
	return runMigrationTo(ctx, db, version, goosex.MigrateUpTo)
}

func MigrateDown(ctx context.Context, db *gorm.DB) error {
	return runMigration(ctx, db, goosex.MigrateDown)
}

func MigrateDownTo(ctx context.Context, db *gorm.DB, version int64) error {
	return runMigrationTo(ctx, db, version, goosex.MigrateDownTo)
}

type migrationOperation func(context.Context, *sql.DB, goosex.Source, string) error

type migrationToOperation func(context.Context, *sql.DB, goosex.Source, string, int64) error

func runMigration(ctx context.Context, db *gorm.DB, operation migrationOperation) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("wuid: get sql database: %w", err)
	}
	return operation(ctx, sqlDB, migrationSource(), db.Dialector.Name())
}

func runMigrationTo(ctx context.Context, db *gorm.DB, version int64, operation migrationToOperation) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("wuid: get sql database: %w", err)
	}
	return operation(ctx, sqlDB, migrationSource(), db.Dialector.Name(), version)
}
