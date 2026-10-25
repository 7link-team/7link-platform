package executor

import (
	"context"
	"testing"
)

func TestTerminalRejectsInvalidWorkspaceAndDimensions(t *testing.T) {
	for _, dir := range []string{"", "relative"} {
		if _, err := OpenTerminal(context.Background(), dir, 80, 24); err == nil {
			t.Fatal("accepted unpinned workspace")
		}
	}
	if _, err := OpenTerminal(context.Background(), t.TempDir(), 0, 24); err == nil {
		t.Fatal("accepted zero width")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenTerminal(ctx, t.TempDir(), 80, 24); err == nil {
		t.Fatal("accepted canceled context")
	}
}
