package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"time"

	"7link.com/platform/internal/app"
	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/maintenance"
	"7link.com/platform/pkg/config"
	"github.com/spf13/cobra"
)

// Exit statuses for bootstrap-platform-admin. Deployment scripts branch on
// these, so each one names an outcome that calls for a different response.
const (
	exitAlreadyBootstrapped = 3
	exitConflict            = 4
	exitSchemaNotReady      = 5
	exitTransient           = 6
	exitNotBootstrapped     = 7
)

// maxPasswordFileSize bounds what the command will read as a password. A
// pointed-at log or database file should fail as a mistake, not stream into
// memory.
const maxPasswordFileSize = 4096

// iamConfig is the single flag set the IAM command group carries, the way the
// migrate group does. Every subcommand below builds its own request from it and
// takes only the settings it acts on, so a value left over from another
// subcommand cannot be read as something this one would change.
type iamConfig struct {
	app.AppConfig                    `mapstructure:",squash"`
	maintenance.PlatformAdminRequest `mapstructure:",squash"`
	maintenance.TenantRepairRequest  `mapstructure:",squash"`
	maintenance.RewrapSecretsRequest `mapstructure:",squash"`
}

var (
	platformAdminConfigurator = config.New()
	platformAdminConfig       iamConfig
	platformAdminConfigErr    error

	// iamCmd groups the IAM operations that run without a server.
	iamCmd = &cobra.Command{
		Use:   "iam",
		Short: "Operate identity and access management state",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}

	// bootstrapPlatformAdminCmd creates the first platform administrator. It is
	// deliberately explicit: no server start path creates one implicitly, because
	// a deployment that grows an administrator on its own has no operator who can
	// say when that happened or who asked for it.
	bootstrapPlatformAdminCmd = &cobra.Command{
		Use:   "bootstrap-platform-admin",
		Short: "Create the first platform administrator",
		Long: "Create the first platform administrator.\n\n" +
			"The password is never accepted as a command-line argument. Supply it with\n" +
			"--password-file, whose file must not be readable by group or other, or on\n" +
			"standard input. Running the command again for the same login reports that\n" +
			"the platform is already bootstrapped and changes nothing.",
		Args: cobra.NoArgs,
		RunE: runBootstrapPlatformAdmin,
	}

	// resetAdminPasswordCmd replaces the platform administrator password from
	// the server itself. It is the only recovery path for the one account no
	// in-band flow can rescue, so it deliberately accepts a different kind of
	// proof: not a password or a mailbox, but the ability to run a command
	// where the service runs.
	resetAdminPasswordCmd = &cobra.Command{
		Use:   "reset-admin-password",
		Short: "Replace the platform administrator password",
		Long: "Replace the platform administrator password without the old one.\n\n" +
			"The password is never accepted as a command-line argument. Supply it with\n" +
			"--password-file, whose file must not be readable by group or other, or on\n" +
			"standard input.\n\n" +
			"The reset acts only on the login that holds full platform administration,\n" +
			"and only while that principal is active. It clears any lockout, ends every\n" +
			"session and recovery link the old password could still reach, and records\n" +
			"an audit event. It does not change multi-factor enrolment.",
		Args: cobra.NoArgs,
		RunE: runResetAdminPassword,
	}

	// repairTenantOwnerCmd installs the first owner on a tenant that has none.
	// Tenants created before tenant creation and owner bootstrap became one
	// transaction can hold no member and no role, and every tenant route
	// requires an active membership: such a tenant is unreachable from inside
	// the product, so it can only be repaired from outside it.
	repairTenantOwnerCmd = &cobra.Command{
		Use:   "repair-tenant-owner",
		Short: "Install the first owner on a tenant that has none",
		Long: "Install the first owner on a tenant that has none.\n\n" +
			"The command is create-only. It refuses any tenant that already has a\n" +
			"member or a role, so it can never hand a second owner to a tenant\n" +
			"somebody already administers, and it refuses a deleted tenant or an\n" +
			"inactive principal.\n\n" +
			"On success it writes the owner membership, the tenant Administrator role\n" +
			"and its grant, and the default menus, exactly as tenant creation does,\n" +
			"and records an audit event marking the tenant as repaired.",
		Args: cobra.NoArgs,
		RunE: runRepairTenantOwner,
	}

	// rewrapSecretsCmd finishes a key rotation. Adding a replacement key and
	// leaving the old one listed is safe but unfinished: every stored secret
	// still opens, and nobody can say which rows would break if the old key came
	// out. This command converts those rows, and reports on the ones it cannot
	// convert, so that taking the key out is a decision with evidence behind it.
	rewrapSecretsCmd = &cobra.Command{
		Use:   "rewrap-secrets",
		Short: "Reseal stored secrets under the active key and report what still needs a retired one",
		Long: "Reseal stored secrets under the active key.\n\n" +
			"Every value a retired key sealed is opened and sealed again under the key\n" +
			"the deployment seals with now. Each write matches the ciphertext it read,\n" +
			"so a secret somebody changes while the command runs is left alone rather\n" +
			"than overwritten, and counted as skipped.\n\n" +
			"Use --dry-run first. It reports the same counts and writes nothing.\n\n" +
			"The report also names the stores no rewrap can convert. Recovery codes,\n" +
			"recovery links and verification links are one-way digests of secrets this\n" +
			"deployment does not keep, so they can only be waited out or reissued. The\n" +
			"retired key stays load-bearing while any of them is still live, which is\n" +
			"why a run that reseals everything can still answer no.",
		Args: cobra.NoArgs,
		RunE: runRewrapSecrets,
	}

	listErasureObligationsCmd = &cobra.Command{
		Use:   "list-erasure-obligations",
		Short: "List verified external erasure obligations as NDJSON",
		Long: "List verified external erasure obligations as NDJSON.\n\n" +
			"The command reads only the S3-compatible object-lock store configured by\n" +
			"iam_audit.anchor. It does not connect to the application database, so its\n" +
			"answer survives restoring a database backup from before an erasure.",
		Args: cobra.NoArgs,
		RunE: runListErasureObligations,
	}
)

func init() {
	rootCmd.AddCommand(iamCmd)
	iamCmd.AddCommand(
		bootstrapPlatformAdminCmd, resetAdminPasswordCmd, repairTenantOwnerCmd, rewrapSecretsCmd,
		listErasureObligationsCmd,
	)
	platformAdminConfigurator.UseFlags(iamCmd.PersistentFlags())
	platformAdminConfigurator.UseConfigFileArgDefault()
	platformAdminConfigErr = platformAdminConfigurator.Bind(&platformAdminConfig, os.Args[1:]...)
}

func runBootstrapPlatformAdmin(command *cobra.Command, _ []string) error {
	if platformAdminConfigErr != nil {
		return platformAdminConfigErr
	}
	if err := platformAdminConfigurator.Load(&platformAdminConfig); err != nil {
		return err
	}
	applyBuildVersion(&platformAdminConfig.AppConfig)
	password, err := readBootstrapPassword(command, platformAdminConfig.PasswordFile)
	if err != nil {
		return err
	}
	report, err := maintenance.RunPlatformAdminBootstrap(command.Context(), maintenance.PlatformAdminConfig{
		AppConfig:            platformAdminConfig.AppConfig,
		PlatformAdminRequest: platformAdminConfig.PlatformAdminRequest,
	}, password)
	if err != nil {
		return withExitCode(platformAdminExitCode(err), err)
	}
	writeBootstrapReport(command.OutOrStdout(), report)
	if report.Outcome == maintenance.PlatformAdminAlreadyBootstrapped {
		// Nothing went wrong, but nothing was created either. A distinct status
		// lets an idempotent deployment step tell those apart without parsing
		// the message.
		return withExitCode(exitAlreadyBootstrapped, errAlreadyBootstrapped)
	}
	return nil
}

func runResetAdminPassword(command *cobra.Command, _ []string) error {
	if platformAdminConfigErr != nil {
		return platformAdminConfigErr
	}
	if err := platformAdminConfigurator.Load(&platformAdminConfig); err != nil {
		return err
	}
	applyBuildVersion(&platformAdminConfig.AppConfig)
	password, err := readBootstrapPassword(command, platformAdminConfig.PasswordFile)
	if err != nil {
		return err
	}
	// The reset takes only the two settings it acts on, so a display name or an
	// email left over from a bootstrap run cannot be read as something this
	// command would change.
	report, err := maintenance.RunPlatformAdminPasswordReset(command.Context(), maintenance.PlatformAdminResetConfig{
		AppConfig: platformAdminConfig.AppConfig,
		PlatformAdminResetRequest: maintenance.PlatformAdminResetRequest{
			Login:        platformAdminConfig.Login,
			PasswordFile: platformAdminConfig.PasswordFile,
		},
	}, password)
	if err != nil {
		return withExitCode(platformAdminExitCode(err), err)
	}
	writeBootstrapReport(command.OutOrStdout(), report)
	return nil
}

func runRepairTenantOwner(command *cobra.Command, _ []string) error {
	if platformAdminConfigErr != nil {
		return platformAdminConfigErr
	}
	if err := platformAdminConfigurator.Load(&platformAdminConfig); err != nil {
		return err
	}
	applyBuildVersion(&platformAdminConfig.AppConfig)
	report, err := maintenance.RunTenantOwnerRepair(command.Context(), maintenance.TenantRepairConfig{
		AppConfig:           platformAdminConfig.AppConfig,
		TenantRepairRequest: platformAdminConfig.TenantRepairRequest,
	})
	if err != nil {
		return withExitCode(platformAdminExitCode(err), err)
	}
	writeTenantRepairReport(command.OutOrStdout(), report)
	return nil
}

func runRewrapSecrets(command *cobra.Command, _ []string) error {
	if platformAdminConfigErr != nil {
		return platformAdminConfigErr
	}
	if err := platformAdminConfigurator.Load(&platformAdminConfig); err != nil {
		return err
	}
	applyBuildVersion(&platformAdminConfig.AppConfig)
	report, err := maintenance.RunSecretRewrap(command.Context(), maintenance.RewrapSecretsConfig{
		AppConfig:            platformAdminConfig.AppConfig,
		RewrapSecretsRequest: platformAdminConfig.RewrapSecretsRequest,
	})
	if err != nil {
		return withExitCode(platformAdminExitCode(err), err)
	}
	writeSecretRewrapReport(command.OutOrStdout(), report)
	return nil
}

func runListErasureObligations(command *cobra.Command, _ []string) error {
	if platformAdminConfigErr != nil {
		return platformAdminConfigErr
	}
	if err := platformAdminConfigurator.Load(&platformAdminConfig); err != nil {
		return err
	}
	obligations, err := maintenance.ListErasureObligations(command.Context(), platformAdminConfig.AppConfig)
	if err != nil {
		return err
	}
	return writeErasureObligations(command.OutOrStdout(), obligations)
}

func writeErasureObligations(output io.Writer, obligations []iamaudit.VerifiedErasureObligation) error {
	encoder := json.NewEncoder(output)
	for _, obligation := range obligations {
		if err := encoder.Encode(obligation); err != nil {
			return fmt.Errorf("write erasure obligation: %w", err)
		}
	}
	return nil
}

// errAlreadyBootstrapped carries the already-bootstrapped status out of RunE.
// It is not a failure; the report on standard output is the real answer.
var errAlreadyBootstrapped = errors.New("platform administrator already bootstrapped")

// platformAdminExitCode maps a failure onto the status that tells an operator
// what to do next: bootstrap first, fix the state, run migrations, or retry.
func platformAdminExitCode(err error) int {
	switch {
	case errors.Is(err, maintenance.ErrPlatformAdminNotBootstrapped):
		return exitNotBootstrapped
	case errors.Is(err, maintenance.ErrPlatformAdminConflict):
		return exitConflict
	case errors.Is(err, maintenance.ErrPlatformAdminSchemaNotReady):
		return exitSchemaNotReady
	case errors.Is(err, maintenance.ErrPlatformAdminUnavailable):
		return exitTransient
	case errors.Is(err, maintenance.ErrTenantRepairNotFound):
		return exitNotBootstrapped
	case errors.Is(err, maintenance.ErrTenantRepairConflict):
		return exitConflict
	case errors.Is(err, maintenance.ErrSecretRewrapUnavailable):
		return exitTransient
	default:
		return exitFailure
	}
}

func writeBootstrapReport(output io.Writer, report maintenance.PlatformAdminReport) {
	// The password and its hash are absent by construction: neither ever leaves
	// the credential store.
	_, _ = fmt.Fprintf(output, "outcome: %s\nprincipal_id: %d\nlogin: %s\n",
		report.Outcome, report.PrincipalID, report.Login)
}

// writeTenantRepairReport names what an operator has to check next: the tenant
// they meant, and the person who can now sign in and administer it.
func writeTenantRepairReport(output io.Writer, report maintenance.TenantRepairReport) {
	_, _ = fmt.Fprintf(output,
		"outcome: %s\ntenant_id: %d\ntenant_slug: %s\nowner_principal_id: %d\nowner_login: %s\n"+
			"administrator_role_id: %d\npermission_count: %d\nmenu_count: %d\n",
		report.Outcome, report.TenantID, report.TenantSlug, report.OwnerPrincipalID, report.OwnerLogin,
		report.AdministratorRoleID, report.PermissionCount, report.MenuCount)
}

// writeSecretRewrapReport separates the three things an operator conflates at
// their peril: what this run converted, what still depends on a retired key,
// and whether waiting will ever clear the remainder.
func writeSecretRewrapReport(output io.Writer, report maintenance.SecretRewrapReport) {
	mode := "rewrap"
	if report.DryRun {
		// Naming the mode in the report keeps a survey pasted into a ticket from
		// later being read as proof that a rewrap ran.
		mode = "survey"
	}
	_, _ = fmt.Fprintf(output,
		"mode: %s\nrewrapped: %d\npending: %d\nunreadable: %d\noutstanding: %d\n"+
			"retired_keys_still_needed: %t\n",
		mode, report.Rewrapped, report.Pending, report.Unreadable, report.Outstanding,
		report.RetiredKeysStillNeeded)
	writeSecretRewrapClearAfter(output, report)
	for _, moduleReport := range report.Modules {
		for _, sealed := range moduleReport.Sealed {
			_, _ = fmt.Fprintf(output,
				"sealed: %s %s total=%d current=%d retired=%d legacy=%d unreadable=%d"+
					" rewrapped=%d skipped=%d\n",
				moduleReport.Module, sealed.Store, sealed.Total, sealed.Current, sealed.Retired,
				sealed.Legacy, sealed.Unreadable, sealed.Rewrapped, sealed.Skipped)
		}
		for _, derived := range moduleReport.Derived {
			_, _ = fmt.Fprintf(output, "derived: %s %s outstanding=%d expires=%s reissue=%s\n",
				moduleReport.Module, derived.Store, derived.Outstanding,
				derivedExpiry(derived.Until), derived.Reissue)
		}
	}
}

// writeSecretRewrapClearAfter prints a date only when one exists. Nothing at
// all is better than a placeholder: an operator reading "clear_after: unknown"
// hears a scheduling problem, when the finding is that no schedule applies.
func writeSecretRewrapClearAfter(output io.Writer, report maintenance.SecretRewrapReport) {
	switch {
	case report.Outstanding == 0:
		return
	case report.ClearAfter.IsZero():
		_, _ = fmt.Fprintln(output,
			"clear_after: never (a derived store below has no expiry; it has to be reissued)")
	default:
		_, _ = fmt.Fprintf(output, "clear_after: %s\n", report.ClearAfter.Format(time.RFC3339))
	}
}

// derivedExpiry names the absence of an expiry rather than leaving the field
// blank, because a blank reads as "not computed" when it means "not ever".
func derivedExpiry(until time.Time) string {
	if until.IsZero() {
		return "never"
	}
	return until.Format(time.RFC3339)
}

// readBootstrapPassword takes the password from a file or standard input. Both
// paths keep it out of the process arguments, where any local user can read it
// and shell history keeps it.
func readBootstrapPassword(command *cobra.Command, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		password, err := io.ReadAll(io.LimitReader(command.InOrStdin(), maxPasswordFileSize+1))
		if err != nil {
			return "", fmt.Errorf("read password from standard input: %w", err)
		}
		return trimPasswordNewline(string(password))
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open password file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect password file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("password file %q is not a regular file", path)
	}
	if err := checkPasswordFileMode(path, info.Mode()); err != nil {
		return "", err
	}
	password, err := io.ReadAll(io.LimitReader(file, maxPasswordFileSize+1))
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	return trimPasswordNewline(string(password))
}

// checkPasswordFileMode refuses a password file other local users can read.
// Windows is skipped because Go synthesizes a permission mode there instead of
// reporting the access-control list, so the check would reject every file.
func checkPasswordFileMode(path string, mode fs.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf(
			"password file %q is readable by group or other (mode %04o); restrict it to 0600",
			path, mode.Perm(),
		)
	}
	return nil
}

// trimPasswordNewline removes exactly one trailing line break, so a file
// written by an ordinary editor yields the password the operator meant. Any
// other whitespace is kept: it may be part of the password.
func trimPasswordNewline(password string) (string, error) {
	if len(password) > maxPasswordFileSize {
		return "", fmt.Errorf("password exceeds %d bytes", maxPasswordFileSize)
	}
	password = strings.TrimSuffix(password, "\n")
	return strings.TrimSuffix(password, "\r"), nil
}
