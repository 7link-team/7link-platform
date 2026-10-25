package executor

import (
	"context"
	"sync"
)

// Process ownership is established before runtime code can execute.
type processPortAuthority struct {
	mu      sync.Mutex
	address func(context.Context, uint16) (string, error)
	list    func(context.Context) ([]int, error)
	release func()
	closed  bool
}

func (c *ProcessControl) OwnedLoopbackPorts(ctx context.Context) ([]int, error) {
	if c == nil {
		return nil, ErrUnsupported
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process == nil || c.ports == nil {
		return nil, ErrUnsupported
	}
	p := c.ports
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.list == nil {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.list(ctx)
}

func (p *processPortAuthority) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.release()
	}
}

// OwnedLoopbackAddress rejects listeners outside this execution's native Job.
// Callers recheck while streaming and stop when the execution ends.
func (c *ProcessControl) OwnedLoopbackAddress(ctx context.Context, port uint16) (string, error) {
	if c == nil || port == 0 {
		return "", ErrUnsupported
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process == nil || c.ports == nil {
		return "", ErrUnsupported
	}
	p := c.ports
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return p.address(ctx, port)
}
