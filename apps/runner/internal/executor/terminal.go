package executor

import (
	"context"
	"io"
	"os"
	"path/filepath"
)

// Terminal is a real OS pseudoconsole. It does not claim filesystem isolation.
// Callers must drain Output while waiting; pseudoconsole shutdown can flush output.
type Terminal struct {
	Input  io.WriteCloser
	Output io.ReadCloser
	resize func(uint16, uint16) error
	close  func() error
	wait   func(context.Context) error
}

func (t *Terminal) Resize(columns, rows uint16) error {
	if columns == 0 || rows == 0 || columns > 500 || rows > 200 {
		return ErrUnsupported
	}
	return t.resize(columns, rows)
}
func (t *Terminal) Close() error                   { return t.close() }
func (t *Terminal) Wait(ctx context.Context) error { return t.wait(ctx) }

// OpenTerminal accepts a server-authorized existing workspace, never a shell path
// or caller-provided environment. Windows reports sandboxed=false at the API.
func OpenTerminal(ctx context.Context, workspace string, columns, rows uint16) (*Terminal, error) {
	if !filepath.IsAbs(workspace) || columns == 0 || rows == 0 || columns > 500 || rows > 200 {
		return nil, ErrUnsupported
	}
	dir, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return openTerminal(ctx, dir, columns, rows)
}
