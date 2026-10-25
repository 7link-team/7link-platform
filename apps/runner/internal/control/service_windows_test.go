//go:build windows

package control

import (
	"os/exec"
	"testing"
)

func TestServiceManagerHasNoWindow(t *testing.T) {
	cmd := exec.Command("powershell.exe")
	hideServiceWindow(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&0x08000000 == 0 {
		t.Fatal("service manager must neither create nor show a console window")
	}
}
