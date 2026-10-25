//go:build windows

package control

import (
	"os/exec"
	"syscall"
)

func hideServiceWindow(cmd *exec.Cmd) {
	// CREATE_NO_WINDOW also avoids inheriting an unsuitable CLI console handle.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
