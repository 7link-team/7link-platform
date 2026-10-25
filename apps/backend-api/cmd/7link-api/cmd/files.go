package cmd

import (
	"encoding/json"
	"os"

	"7link.com/platform/internal/app"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/maintenance"
	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

var filesConfigurator = config.New()
var filesConfig app.AppConfig
var filesConfigErr error
var migrateObjectID string
var migrateFromBackend string
var filesCmd = &cobra.Command{Use: "files", Short: "Maintain immutable file storage", Args: cobra.NoArgs}
var filesMigrateObjectCmd = &cobra.Command{Use: "migrate-object", Short: "Verify and migrate one object to the configured active backend; retain source bytes", Args: cobra.NoArgs, RunE: runFileObjectMigration}

func init() {
	rootCmd.AddCommand(filesCmd)
	filesCmd.AddCommand(filesMigrateObjectCmd)
	filesMigrateObjectCmd.Flags().StringVar(&migrateObjectID, "object-id", "", "immutable object ID")
	filesMigrateObjectCmd.Flags().StringVar(&migrateFromBackend, "from-backend", "", "configured read backend holding the source")
	filesConfigurator.UseFlags(filesCmd.PersistentFlags())
	filesConfigurator.UseConfigFileArgDefault()
	filesConfigErr = filesConfigurator.Bind(&filesConfig, os.Args[1:]...)
}
func runFileObjectMigration(command *cobra.Command, _ []string) error {
	if filesConfigErr != nil {
		return filesConfigErr
	}
	id, err := guid.Parse(migrateObjectID)
	if err != nil {
		return err
	}
	if err := filesConfigurator.Load(&filesConfig); err != nil {
		return err
	}
	applyBuildVersion(&filesConfig)
	report, err := maintenance.RunFileMigration(command.Context(), filesConfig, id, migrateFromBackend)
	if err != nil {
		return err
	}
	return json.NewEncoder(command.OutOrStdout()).Encode(report)
}
