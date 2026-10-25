//go:build windows

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

func controlProcessGroup(_ *os.Process, _ bool) error {
	// Thread enumeration races new threads/descendants, and NtSuspendProcess is
	// undocumented. Neither can truthfully acknowledge a process-tree hold.
	return fmt.Errorf("%w: process-group pause/resume is unavailable on Windows", ErrUnsupported)
}

func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

func configureCancellation(cmd *exec.Cmd) *atomic.Bool {
	confirmed := new(atomic.Bool)
	cmd.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		kill := exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		hideWindow(kill)
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		confirmed.Store(true)
		return nil
	}
	return confirmed
}
