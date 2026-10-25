package maintenance

import (
	"context"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/files"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// RunFileMigration reuses prepared maintenance: validates the schema and starts
// only ID infrastructure, with no HTTP listeners or background business jobs.
func RunFileMigration(ctx context.Context, config app.AppConfig, id guid.ID, from string) (report files.ObjectMigrationReport, resultErr error) {
	if id <= 0 || from == "" {
		return report, files.ErrInvalidKey
	}
	resultErr = app.RunPreparedMaintenance(ctx, config, func(services *module.Services, _ *gorm.DB, _ *dlock.Module) error {
		migrate, err := module.Resolve[files.ObjectMigrator](services)
		if err != nil {
			return err
		}
		report, err = migrate(ctx, id, from)
		return err
	})
	return report, resultErr
}
