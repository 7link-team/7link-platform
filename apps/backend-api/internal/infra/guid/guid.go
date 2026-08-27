// Package guid generates globally unique, roughly time-ordered 64-bit IDs with
// Sonyflake. A leased WUID supplies the 16-bit worker component.
package guid

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"7link.com/platform/internal/infra/wuid"
	"github.com/sony/sonyflake"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

var ErrNotInitialized = errors.New("guid: generator not initialized")

type Generator struct {
	mu sync.Mutex

	sonyflake        *sonyflake.Sonyflake
	workerID         uint16
	workerGeneration uint64
	workerSource     func() (wuid.Lease, error)
}

func New(workerID uint16) (*Generator, error) {
	sonyflakeGenerator, err := newSonyflake(workerID)
	if err != nil {
		return nil, err
	}
	return &Generator{sonyflake: sonyflakeGenerator, workerID: workerID}, nil
}

func newSonyflake(workerID uint16) (*sonyflake.Sonyflake, error) {
	generator, err := sonyflake.New(sonyflake.Settings{
		StartTime: epoch,
		MachineID: func() (uint16, error) { return workerID, nil },
	})
	if err != nil {
		return nil, fmt.Errorf("guid: create sonyflake: %w", err)
	}
	return generator, nil
}

func newManagedGenerator(source func() (wuid.Lease, error)) *Generator {
	return &Generator{workerSource: source}
}

// Ready ensures a managed generator is bound to the current valid worker ID.
func (g *Generator) Ready() error {
	if g == nil {
		return ErrNotInitialized
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ensureReady()
}

func (g *Generator) ensureReady() error {
	if g.workerSource == nil {
		if g.sonyflake == nil {
			return ErrNotInitialized
		}
		return nil
	}
	lease, err := g.workerSource()
	if err != nil {
		return errors.Join(ErrNotInitialized, err)
	}
	if g.sonyflake != nil && g.workerID == lease.WorkerID && g.workerGeneration == lease.Generation {
		return nil
	}
	generator, err := newSonyflake(lease.WorkerID)
	if err != nil {
		return err
	}
	g.sonyflake = generator
	g.workerID = lease.WorkerID
	g.workerGeneration = lease.Generation
	return nil
}

func (g *Generator) Next() (int64, error) {
	if g == nil {
		return 0, ErrNotInitialized
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.ensureReady(); err != nil {
		return 0, err
	}
	id, err := g.sonyflake.NextID()
	if err != nil {
		return 0, err
	}
	if id > math.MaxInt64 {
		return 0, errors.New("guid: generated ID exceeds signed 64-bit storage")
	}
	return int64(id), nil
}

func (g *Generator) MustNext() int64 {
	id, err := g.Next()
	if err != nil {
		panic(err)
	}
	return id
}
