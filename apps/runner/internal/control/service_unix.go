//go:build !windows

package control

import "os/exec"

func hideServiceWindow(cmd *exec.Cmd) {}
