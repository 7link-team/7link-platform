//go:build !windows

package executor

import "os/exec"

// Preserve runtime execution; preview fails closed without native ownership.
func startPortOwnedProcess(cmd *exec.Cmd) (*processPortAuthority, error) {
	return nil, cmd.Start()
}
