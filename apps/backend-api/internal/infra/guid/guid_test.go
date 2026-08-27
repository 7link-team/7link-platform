package guid

import (
	"testing"

	"7link.com/platform/internal/infra/wuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratorProducesUniqueSignedIDs(t *testing.T) {
	generator, err := New(7)
	require.NoError(t, err)
	seen := make(map[int64]struct{}, 10000)
	for range 10000 {
		id, err := generator.Next()
		require.NoError(t, err)
		assert.Positive(t, id)
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate ID %d", id)
		}
		seen[id] = struct{}{}
	}
}

func TestGeneratorMustNext(t *testing.T) {
	generator, err := New(3)
	require.NoError(t, err)
	assert.Positive(t, generator.MustNext())
}

func TestManagedGeneratorFollowsWorkerIDGeneration(t *testing.T) {
	workerID, generation, available := uint16(3), uint64(1), true
	generator := newManagedGenerator(func() (wuid.Lease, error) {
		if !available {
			return wuid.Lease{}, wuid.ErrUnavailable
		}
		return wuid.Lease{WorkerID: workerID, Generation: generation}, nil
	})
	_, err := generator.Next()
	require.NoError(t, err)
	assert.Equal(t, uint16(3), generator.workerID)
	assert.Equal(t, uint64(1), generator.workerGeneration)
	previous := generator.sonyflake

	available = false
	_, err = generator.Next()
	assert.ErrorIs(t, err, wuid.ErrUnavailable)

	available, generation = true, 2
	_, err = generator.Next()
	require.NoError(t, err)
	assert.Equal(t, uint16(3), generator.workerID)
	assert.Equal(t, uint64(2), generator.workerGeneration)
	assert.NotSame(t, previous, generator.sonyflake)
}
