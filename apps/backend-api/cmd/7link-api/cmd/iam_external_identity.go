package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/maintenance"
	"github.com/spf13/cobra"
)

func init() {
	inventory := &cobra.Command{Use: "external-identity-inventory", Short: "Report legacy external identity ownership; exit 4 if unresolved",
		Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
			if err := loadExternalIdentityConfig(); err != nil {
				return err
			}
			report, err := maintenance.RunExternalIdentityInventory(command.Context(), platformAdminConfig.AppConfig)
			if err == nil || errors.Is(err, maintenance.ErrLegacyIdentityUnresolved) {
				if report == nil {
					if _, writeErr := fmt.Fprintln(command.OutOrStdout(), "[]"); writeErr != nil {
						return writeErr
					}
				} else if writeErr := json.NewEncoder(command.OutOrStdout()).Encode(report); writeErr != nil {
					return writeErr
				}
			}
			if errors.Is(err, maintenance.ErrLegacyIdentityUnresolved) {
				return withExitCode(exitConflict, err)
			}
			return err
		}}
	var principalID, evidenceFile string
	record := &cobra.Command{Use: "record-external-identity-evidence", Short: "Record independently reviewed legacy ownership evidence without trusting a link",
		Long: "Host operator maintenance command. Supply an independently reviewed ownership record, including credential/mailbox provenance, from a file. This records an audit event and permits independent account recovery/repair; it does not establish a trusted external binding or issue credentials.",
		Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
			id, err := guid.Parse(principalID)
			if err != nil || id.Zero() {
				return errors.New("a valid --principal-id is required")
			}
			file, err := os.Open(evidenceFile)
			if err != nil {
				return err
			}
			defer func() { _ = file.Close() }()
			contents, err := io.ReadAll(io.LimitReader(file, 4097))
			if err != nil {
				return err
			}
			if len(contents) > 4096 {
				return errors.New("ownership evidence exceeds 4096 bytes")
			}
			if err := loadExternalIdentityConfig(); err != nil {
				return err
			}
			if err := maintenance.RecordExternalIdentityEvidence(command.Context(), platformAdminConfig.AppConfig, id, string(contents)); err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "Ownership evidence recorded; binding remains untrusted until independent repair.")
			return err
		}}
	record.Flags().StringVar(&principalID, "principal-id", "", "principal whose ownership was independently reviewed")
	record.Flags().StringVar(&evidenceFile, "evidence-file", "", "file containing the independent ownership review record")
	quarantine := &cobra.Command{Use: "quarantine-external-identities", Short: "Quarantine legacy links and invalidate credentials after stopping old replicas",
		Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
			if err := loadExternalIdentityConfig(); err != nil {
				return err
			}
			return maintenance.QuarantineExternalIdentities(command.Context(), platformAdminConfig.AppConfig)
		}}
	iamCmd.AddCommand(inventory, record, quarantine)
}

func loadExternalIdentityConfig() error {
	if platformAdminConfigErr != nil {
		return platformAdminConfigErr
	}
	if err := platformAdminConfigurator.Load(&platformAdminConfig); err != nil {
		return err
	}
	applyBuildVersion(&platformAdminConfig.AppConfig)
	return nil
}
