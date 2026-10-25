//go:build !windows

package executor

import (
	"errors"
	"testing"
)

func TestTerminalNativeOtherExplicitlyUnsupported(t *testing.T) {
	terminal, err := OpenTerminal(t.Context(), t.TempDir(), 80, 24)
	if terminal != nil {
		_ = terminal.Close()
		t.Fatal("unsupported platform opened an interactive terminal")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected explicit unsupported result, got %v", err)
	}
}
