package cmd

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"7link.com/platform/internal/app"
	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

var (
	migrateConfigurator = config.New()
	migrateConfig       app.MigrationConfig
	migrateConfigErr    error

	// migrateCmd represents the migrate command.
	migrateCmd = &cobra.Command{
		Use:   "migrate",
		Short: "Inspect or change database migration state",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}

	migrateAutoCmd     = newMigrateActionCommand(app.MigrationAuto, "Reconcile to the embedded migration version")
	migrateStatusCmd   = newMigrateActionCommand(app.MigrationStatus, "Show migration state")
	migrateValidateCmd = newMigrateActionCommand(app.MigrationValidate, "Validate migration state")
	migrateUpCmd       = newMigrateActionCommand(app.MigrationUp, "Apply all pending migrations")
	migrateUpToCmd     = newMigrateActionCommand(app.MigrationUpTo, "Apply migrations through the target version")
	migrateDownCmd     = newMigrateActionCommand(app.MigrationDown, "Roll back the latest migration")
	migrateDownToCmd   = newMigrateActionCommand(app.MigrationDownTo, "Roll back to the target version")
)

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateCmd.AddCommand(
		migrateAutoCmd,
		migrateStatusCmd,
		migrateValidateCmd,
		migrateUpCmd,
		migrateUpToCmd,
		migrateDownCmd,
		migrateDownToCmd,
	)
	migrateConfigurator.UseFlags(migrateCmd.PersistentFlags())
	migrateConfigurator.UseConfigFileArgDefault()
	migrateConfigErr = migrateConfigurator.Bind(&migrateConfig, os.Args[1:]...)
}

func newMigrateActionCommand(action app.MigrationAction, description string) *cobra.Command {
	return &cobra.Command{
		Use:   string(action),
		Short: description,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if migrateConfigErr != nil {
				return migrateConfigErr
			}
			if err := migrateConfigurator.Load(&migrateConfig); err != nil {
				return err
			}
			applyBuildVersion(&migrateConfig.AppConfig)
			migrateConfig.Action = action
			reports, err := (&app.App{}).RunMigrations(command.Context(), migrateConfig)
			writeMigrationReports(command.OutOrStdout(), reports)
			return err
		},
	}
}

func writeMigrationReports(output io.Writer, reports []app.MigrationReport) {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "MODULE\tCURRENT\tTARGET\tSTATE\tPENDING\tUNKNOWN")
	for _, report := range reports {
		_, _ = fmt.Fprintf(writer, "%s\t%d\t%d\t%s\t%v\t%v\n",
			report.Module,
			report.Status.Current,
			report.Status.Target,
			report.Status.State,
			report.Status.Pending,
			report.Status.Unknown,
		)
	}
	_ = writer.Flush()
}
