package clientwork

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Exercise the real native job/input boundary without claiming OS-account or
// configured-model acceptance. The child is this test binary, never a shell.
func TestWindowsVerifierNativeJobDeliversExactInputAndReportsExit(t *testing.T) {
	const childKey = "VERIFIER_NATIVE_JOB_CHILD"
	if mode := os.Getenv(childKey); mode != "" {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil || string(raw) != "private case\x00with exact bytes\n" {
			os.Exit(21)
		}
		_, _ = os.Stdout.Write(raw)
		if mode == "failure" {
			os.Exit(9)
		}
		os.Exit(0)
	}
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsVerifierNativeJobDeliversExactInputAndReportsExit$")
			hideHiddenProcess(cmd)
			cmd.Env = append(os.Environ(), childKey+"="+mode)
			var output bytes.Buffer
			cmd.Stdout = &output
			raw := []byte("private case\x00with exact bytes\n")
			terminated, err := runHiddenProcess(cmd, raw)
			require.True(t, terminated, "job termination must be confirmed before private cleanup")
			require.Equal(t, raw, output.Bytes(), "stdin must arrive unmodified after job assignment")
			require.NotNil(t, cmd.ProcessState)
			if mode == "failure" {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 9, exit.ExitCode())
			} else {
				require.NoError(t, err)
				require.True(t, cmd.ProcessState.Success())
			}
		})
	}
}

func TestWindowsVerifierIsolationRejectsMissingAndInvalidIdentityBeforeStart(t *testing.T) {
	const passwordKey = "VERIFIER_TEST_ISOLATION_PASSWORD"
	t.Setenv(passwordKey, "local-test-placeholder")
	for _, config := range []HiddenVerifierConfig{
		{},
		{OSUser: "unconfigured"},
		{OSUser: "unconfigured", PasswordEnv: "VERIFIER_UNSET_ISOLATION_PASSWORD"},
		{OSUser: "invalid\x00user", PasswordEnv: passwordKey},
		{OSUser: "unconfigured", OSDomain: "invalid\x00domain", PasswordEnv: passwordKey},
	} {
		cmd := exec.CommandContext(context.Background(), "must-never-start.exe")
		hideHiddenProcess(cmd)
		cleanup, err := isolateHiddenCommand(cmd, config, t.TempDir())
		require.Error(t, err)
		require.Equal(t, "verified OS isolation unavailable", err.Error())
		require.Nil(t, cleanup)
		require.Nil(t, cmd.Process)
		require.Zero(t, cmd.SysProcAttr.Token)
	}
}

func TestWindowsVerifierNativeProcessRejectsPreboundInputAndMissingExecutable(t *testing.T) {
	cmd := exec.Command("missing-verifier-command.exe")
	hideHiddenProcess(cmd)
	cmd.Stdin = strings.NewReader("already bound")
	terminated, err := runHiddenProcess(cmd, []byte("private input"))
	require.Error(t, err)
	require.True(t, terminated)
	require.Nil(t, cmd.Process)
	cmd = exec.Command("missing-verifier-command.exe")
	hideHiddenProcess(cmd)
	terminated, err = runHiddenProcess(cmd, []byte("private input"))
	require.Error(t, err)
	require.True(t, terminated)
	require.Nil(t, cmd.Process)
	ctx, cancel := context.WithCancel(t.Context())
	cmd = exec.CommandContext(ctx, "missing-verifier-command.exe")
	hideHiddenProcess(cmd)
	require.ErrorIs(t, cmd.Cancel(), os.ErrProcessDone)
	cancel()
}
