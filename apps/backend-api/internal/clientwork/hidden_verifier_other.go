//go:build !windows

package clientwork

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func hideHiddenProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}

func isolateHiddenCommand(cmd *exec.Cmd, c HiddenVerifierConfig, root string) (func() error, error) {
	return nil, errors.New("verified OS isolation unavailable")
}

func runHiddenProcess(cmd *exec.Cmd, raw []byte) (bool, error) {
	cmd.Stdin = bytes.NewReader(raw)
	return true, cmd.Run()
}
