package wuid

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

func migrationSource() goosex.Source { return goosex.Source{FS: migrationsFS, TableName: "goose_wuid"} }

func Migrate(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("wuid: get sql database: %w", err)
	}
	return goosex.Up(ctx, sqlDB, migrationSource(), db.Dialector.Name())
}

func MigrateDown(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("wuid: get sql database: %w", err)
	}
	return goosex.Down(ctx, sqlDB, migrationsFS, db.Dialector.Name(), "goose_wuid")
}

func MigrateDownTo(ctx context.Context, db *gorm.DB, version int64) error {
	if db == nil {
		return ErrDatabaseUnavailable
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("wuid: get sql database: %w", err)
	}
	return goosex.DownSourceTo(ctx, sqlDB, migrationSource(), db.Dialector.Name(), version)
}
