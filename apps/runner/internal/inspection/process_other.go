//go:build !windows

package inspection

import (
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}

func configureProbeCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
