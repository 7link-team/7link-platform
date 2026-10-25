//go:build !windows

package executor

import "context"

// A Linux shell without the required landlock/bubblewrap filesystem sandbox is
// not a safe fallback. Other targets explicitly decline interactive terminals.
func openTerminal(context.Context, string, uint16, uint16) (*Terminal, error) {
	return nil, ErrUnsupported
}
