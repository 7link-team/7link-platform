package executor

import (
	"context"
	"os"
	"testing"
)

func TestProcessPortAuthorityRequiresLiveBinding(t *testing.T) {
	controller := new(ProcessControl)
	if _, err := controller.OwnedLoopbackAddress(t.Context(), 80); err == nil {
		t.Fatal("unbound ownership accepted")
	}
	ports := &processPortAuthority{address: func(context.Context, uint16) (string, error) { return "127.0.0.1", nil }, release: func() {}}
	release := controller.bindOwned(&os.Process{Pid: 1}, ports)
	if _, err := controller.OwnedLoopbackAddress(t.Context(), 0); err == nil {
		t.Fatal("invalid port accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := controller.OwnedLoopbackAddress(ctx, 80); err == nil {
		t.Fatal("canceled ownership accepted")
	}
	ports.close()
	if _, err := controller.OwnedLoopbackAddress(t.Context(), 80); err == nil {
		t.Fatal("closed authority accepted")
	}
	release()
}
