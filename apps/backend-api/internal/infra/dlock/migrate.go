package dlock

import (
	"context"
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

func migrationSource() goosex.Source {
	return goosex.Source{FS: migrationsFS, TableName: "goose_dlock"}
}

// Migrate applies the lock schema using a module-private Goose version table.
func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("dlock: get sql database: %w", err)
	}
	return goosex.Up(ctx, sqlDB, migrationSource(), db.Dialector.Name())
}

func MigrateDown(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("dlock: get sql database: %w", err)
	}
	return goosex.Down(ctx, sqlDB, migrationsFS, db.Dialector.Name(), "goose_dlock")
}

func MigrateDownTo(ctx context.Context, db *gorm.DB, version int64) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("dlock: get sql database: %w", err)
	}
	return goosex.DownSourceTo(ctx, sqlDB, migrationSource(), db.Dialector.Name(), version)
}
