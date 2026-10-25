package fleet

import (
	"context"
	"errors"
	"sync"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
)

// A connection shares one output budget across all its streams. Reserving a
// bounded frame at a time applies backpressure to the runner's output POST;
// ordinary execution control and evidence uploads never acquire this budget.
type tunnelBandwidth struct {
	mu      sync.Mutex
	next    time.Time
	streams int
}

var ErrTunnelQuota = errors.New("fleet: tunnel stream quota exceeded")

func (m *Module) tunnelBudget(tenant, machine guid.ID) *tunnelBandwidth {
	b := m.terminalBroker
	b.mu.Lock()
	defer b.mu.Unlock()
	key := [2]guid.ID{tenant, machine}
	budget := b.budgets[key]
	if budget == nil {
		budget = &tunnelBandwidth{}
		b.budgets[key] = budget
	}
	return budget
}

func (m *Module) acquireTunnelStream(ctx context.Context, tenant, machine guid.ID) (func(), error) {
	if m.terminalBroker == nil {
		return nil, ErrUnavailable
	}
	db, err := m.writer()
	if err != nil {
		return nil, err
	}
	q := query.BindTerminalAccess(db, m.names)
	access, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine)).First()
	if err != nil {
		return nil, err
	}
	limit := access.TunnelMaxStreams
	if limit <= 0 {
		limit = 256
	}
	budget := m.tunnelBudget(tenant, machine)
	budget.mu.Lock()
	if budget.streams >= limit {
		budget.mu.Unlock()
		return nil, ErrTunnelQuota
	}
	budget.streams++
	budget.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { budget.mu.Lock(); budget.streams--; budget.mu.Unlock() }) }, nil
}

func (m *Module) openTerminalStream(ctx context.Context, session TerminalSession, columns, rows int) (bool, error) {
	release, err := m.acquireTunnelStream(ctx, session.TenantID, session.MachineID)
	if err != nil {
		return false, err
	}
	sandboxed, err := m.terminalBroker.open(ctx, session, columns, rows, release)
	if err != nil {
		release()
	}
	return sandboxed, err
}

func (b *tunnelBandwidth) reserve(now time.Time, size, kbps int) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.next.Before(now) {
		b.next = now
	}
	delay := b.next.Sub(now)
	b.next = b.next.Add(time.Duration(size) * 8 * time.Second / time.Duration(kbps*1024))
	return delay
}

func (m *Module) throttleTerminalOutput(ctx context.Context, c *terminalConnection, size int) error {
	return m.throttleTunnelOutput(ctx, c.tenant, c.machine, c.done, size, "terminal")
}

func (m *Module) throttleTunnelOutput(ctx context.Context, tenant, machine guid.ID, done <-chan struct{}, size int, kind string) error {
	if size <= 0 {
		return nil
	}
	db, err := m.writer()
	if err != nil {
		return err
	}
	q := query.BindTerminalAccess(db, m.names)
	access, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MachineID.Eq(machine)).First()
	if err != nil {
		return err
	}
	if (kind == "terminal" && !access.Enabled) || (kind == "preview" && !access.AllowRemotePreview) {
		return ErrTerminalAccessDisabled
	}
	kbps := access.TunnelBandwidthKbps
	if kbps <= 0 {
		kbps = 20000
	}
	b := m.terminalBroker
	if b == nil {
		return ErrUnavailable
	}
	budget := m.tunnelBudget(tenant, machine)
	delay := budget.reserve(time.Now(), size, kbps)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return ErrUnavailable
	}
}
