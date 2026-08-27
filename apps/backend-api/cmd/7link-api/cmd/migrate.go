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
	migrateModule       string
	migrateTarget       int64

	// migrateCmd represents the migrate command.
	migrateCmd = &cobra.Command{
		Use:   "migrate <status|validate|up|down>",
		Short: "Inspect or change database migration state",
		Args:  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{
			string(app.MigrationStatus),
			string(app.MigrationValidate),
			"up",
			string(app.MigrationDown),
		},
		RunE: runMigrate,
	}
)

func init() {
	rootCmd.AddCommand(migrateCmd)
	migrateConfigurator.UseFlags(migrateCmd.Flags())
	migrateConfigurator.UseConfigFileArgDefault()
	migrateConfigErr = migrateConfigurator.Bind(&migrateConfig, os.Args[1:]...)
	migrateCmd.Flags().StringVar(&migrateModule, "module", "", "limit the operation to one module")
	migrateCmd.Flags().Int64Var(&migrateTarget, "to", -1, "target migration version for down")
}

func runMigrate(command *cobra.Command, args []string) error {
	if migrateConfigErr != nil {
		return migrateConfigErr
	}
	if err := migrateConfigurator.Load(&migrateConfig); err != nil {
		return err
	}
	action := app.MigrationAction(args[0])
	if action == "up" {
		action = app.MigrationUpOnly
	}
	request := app.MigrationRequest{Action: action, Module: migrateModule}
	if action == app.MigrationDown {
		request.To = migrateTarget
	}
	reports, err := app.RunMigrations(command.Context(), migrateConfig, request)
	writeMigrationReports(command.OutOrStdout(), reports)
	return err
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
