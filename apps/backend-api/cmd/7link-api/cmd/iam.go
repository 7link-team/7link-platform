package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"7link.com/platform/internal/app"
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

var (
	platformAdminConfigurator = config.New()
	platformAdminConfig       app.PlatformAdminConfig
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
)

func init() {
	rootCmd.AddCommand(iamCmd)
	iamCmd.AddCommand(bootstrapPlatformAdminCmd, resetAdminPasswordCmd)
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
	report, err := (&app.App{}).RunPlatformAdminBootstrap(command.Context(), platformAdminConfig, password)
	if err != nil {
		return withExitCode(platformAdminExitCode(err), err)
	}
	writeBootstrapReport(command.OutOrStdout(), report)
	if report.Outcome == app.PlatformAdminAlreadyBootstrapped {
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
	// The IAM command group carries one flag set, the way the migrate group
	// does. The reset takes only the two settings it acts on, so a display name
	// or an email left over from a bootstrap run cannot be read as something
	// this command would change.
	report, err := (&app.App{}).RunPlatformAdminPasswordReset(command.Context(), app.PlatformAdminResetConfig{
		AppConfig: platformAdminConfig.AppConfig,
		PlatformAdminResetRequest: app.PlatformAdminResetRequest{
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

// errAlreadyBootstrapped carries the already-bootstrapped status out of RunE.
// It is not a failure; the report on standard output is the real answer.
var errAlreadyBootstrapped = errors.New("platform administrator already bootstrapped")

// platformAdminExitCode maps a failure onto the status that tells an operator
// what to do next: bootstrap first, fix the state, run migrations, or retry.
func platformAdminExitCode(err error) int {
	switch {
	case errors.Is(err, app.ErrPlatformAdminNotBootstrapped):
		return exitNotBootstrapped
	case errors.Is(err, app.ErrPlatformAdminConflict):
		return exitConflict
	case errors.Is(err, app.ErrPlatformAdminSchemaNotReady):
		return exitSchemaNotReady
	case errors.Is(err, app.ErrPlatformAdminUnavailable):
		return exitTransient
	default:
		return exitFailure
	}
}

func writeBootstrapReport(output io.Writer, report app.PlatformAdminReport) {
	// The password and its hash are absent by construction: neither ever leaves
	// the credential store.
	_, _ = fmt.Fprintf(output, "outcome: %s\nprincipal_id: %d\nlogin: %s\n",
		report.Outcome, report.PrincipalID, report.Login)
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
