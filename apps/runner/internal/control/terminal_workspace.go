package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"7link.com/runner/internal/executor"
)

var errTerminalWorkspace = errors.New("terminal execution workspace unavailable")

const terminalWorkspaceLimit = 16

type terminalWorkspacesKey struct{}
type terminalAttemptKey struct{}
type terminalAttempt struct {
	id    string
	epoch int64
}
type terminalWorkspacePin struct {
	epoch          int64
	source, actual string
	lifetime       context.Context
	cancel         context.CancelFunc
	processControl *executor.ProcessControl
}
type terminalWorkspaces struct {
	mu   sync.Mutex
	pins map[string]terminalWorkspacePin
}

// Pins are local, ephemeral facts from the executor, never server-provided cwd.
func withTerminalWorkspaces(ctx context.Context) context.Context {
	if _, ok := ctx.Value(terminalWorkspacesKey{}).(*terminalWorkspaces); ok {
		return ctx
	}
	registry := &terminalWorkspaces{pins: make(map[string]terminalWorkspacePin)}
	ctx = context.WithValue(ctx, terminalWorkspacesKey{}, registry)
	context.AfterFunc(ctx, func() { clearTerminalWorkspaces(ctx) })
	return ctx
}

func terminalWorkspacePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errTerminalWorkspace
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errTerminalWorkspace
	}
	info, err := os.Stat(actual)
	if err != nil || !info.IsDir() {
		return "", errTerminalWorkspace
	}
	return filepath.Clean(actual), nil
}
func sameTerminalWorkspace(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func pinTerminalWorkspace(ctx context.Context, id string, epoch int64, source, actual string, controls ...*executor.ProcessControl) error {
	registry, ok := ctx.Value(terminalWorkspacesKey{}).(*terminalWorkspaces)
	if !ok || !validID(id) || epoch < 1 || ctx.Err() != nil || len(controls) > 1 {
		return errTerminalWorkspace
	}
	source, err := terminalWorkspacePath(source)
	if err != nil {
		return err
	}
	actual, err = terminalWorkspacePath(actual)
	if err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if ctx.Err() != nil {
		return errTerminalWorkspace
	}
	if pin, exists := registry.pins[id]; exists {
		if pin.epoch == epoch && sameTerminalWorkspace(pin.source, source) && sameTerminalWorkspace(pin.actual, actual) && pin.lifetime.Err() == nil && (len(controls) == 0 || pin.processControl == controls[0]) {
			return nil
		}
		return errTerminalWorkspace
	}
	if len(registry.pins) >= terminalWorkspaceLimit {
		return errTerminalWorkspace
	}
	lifetime, cancel := context.WithCancel(ctx)
	var control *executor.ProcessControl
	if len(controls) == 1 {
		control = controls[0]
	}
	registry.pins[id] = terminalWorkspacePin{epoch: epoch, source: source, actual: actual, lifetime: lifetime, cancel: cancel, processControl: control}
	return nil
}

func resolveTerminalWorkspace(ctx context.Context, id string, epoch int64, source string) (terminalWorkspacePin, error) {
	registry, ok := ctx.Value(terminalWorkspacesKey{}).(*terminalWorkspaces)
	if !ok || ctx.Err() != nil || !validID(id) || epoch < 1 {
		return terminalWorkspacePin{}, errTerminalWorkspace
	}
	source, err := terminalWorkspacePath(source)
	if err != nil {
		return terminalWorkspacePin{}, err
	}
	registry.mu.Lock()
	pin, exists := registry.pins[id]
	registry.mu.Unlock()
	if !exists || pin.epoch != epoch || pin.lifetime.Err() != nil || !sameTerminalWorkspace(pin.source, source) {
		return terminalWorkspacePin{}, errTerminalWorkspace
	}
	actual, err := terminalWorkspacePath(pin.actual)
	if err != nil || !sameTerminalWorkspace(actual, pin.actual) {
		return terminalWorkspacePin{}, errTerminalWorkspace
	}
	return pin, nil
}

func dropTerminalWorkspace(ctx context.Context, id string, epoch int64) {
	registry, ok := ctx.Value(terminalWorkspacesKey{}).(*terminalWorkspaces)
	if !ok {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if pin, exists := registry.pins[id]; exists && pin.epoch == epoch {
		pin.cancel()
		delete(registry.pins, id)
	}
}
func clearTerminalWorkspaces(ctx context.Context) {
	registry, ok := ctx.Value(terminalWorkspacesKey{}).(*terminalWorkspaces)
	if !ok {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for id, pin := range registry.pins {
		pin.cancel()
		delete(registry.pins, id)
	}
}
