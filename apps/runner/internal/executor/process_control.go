package executor

import (
	"os"
	"sync"
)

// ProcessControl controls only the execution's existing process group. Processes
// that deliberately leave that group are outside this containment mechanism.
// A controller belongs to one attempt and is never serialized into a plan.
type ProcessControl struct {
	mu      sync.Mutex
	process *os.Process
	paused  bool
	ports   *processPortAuthority
}

func (c *ProcessControl) bind(process *os.Process) func() {
	return c.bindOwned(process, nil)
}
func (c *ProcessControl) bindOwned(process *os.Process, ports *processPortAuthority) func() {
	if c == nil {
		return func() {}
	}
	c.mu.Lock()
	c.process = process
	c.ports = ports
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		c.process = nil
		c.ports = nil
		c.paused = false
		c.mu.Unlock()
	}
}

// SetPaused returns true only after the native operation succeeds. An attempt
// still preparing its process returns false; the next heartbeat can retry.
func (c *ProcessControl) SetPaused(paused bool) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process == nil || c.paused == paused {
		return false, nil
	}
	if err := controlProcessGroup(c.process, paused); err != nil {
		return false, err
	}
	c.paused = paused
	return true, nil
}

func (c *ProcessControl) IsPaused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}
