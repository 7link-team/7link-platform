package federation

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReplayRegistryAllowsOneConcurrentWinner(t *testing.T) {
	now := time.Now().UTC()
	registry := newReplayRegistry(32)
	var winners atomic.Int32
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			if registry.claim("same-protocol-id", now.Add(time.Minute), now) {
				winners.Add(1)
			}
		}()
	}
	group.Wait()
	assert.EqualValues(t, 1, winners.Load())
}

func TestReplayRegistryFailsClosedAndReclaimsExpiredEntries(t *testing.T) {
	now := time.Now().UTC()
	registry := newReplayRegistry(1)
	assert.False(t, (*replayRegistry)(nil).claim("id", now.Add(time.Minute), now))
	assert.False(t, registry.claim("", now.Add(time.Minute), now))
	assert.False(t, registry.claim("expired", now, now))
	assert.True(t, registry.claim("first", now.Add(time.Minute), now))
	assert.False(t, registry.claim("second", now.Add(time.Minute), now))
	assert.True(t, registry.claim("second", now.Add(2*time.Minute), now.Add(time.Minute)))
	assert.False(t, registry.claim("second", now.Add(2*time.Minute), now.Add(time.Minute)))
	assert.NotNil(t, newReplayRegistry(0))
}
