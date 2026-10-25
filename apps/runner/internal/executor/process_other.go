//go:build !windows

package executor

import (
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
)

func controlProcessGroup(process *os.Process, paused bool) error {
	signal := syscall.SIGCONT
	if paused {
		signal = syscall.SIGSTOP
	}
	return syscall.Kill(-process.Pid, signal)
}

func hideWindow(*exec.Cmd) {}

func configureCancellation(cmd *exec.Cmd) *atomic.Bool {
	confirmed := new(atomic.Bool)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		confirmed.Store(err == nil)
		return err
	}
	return confirmed
}
