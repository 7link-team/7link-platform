package audit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeConfigClampsInvalidValues(t *testing.T) {
	config := normalizeConfig(Config{
		Enabled:       true,
		MaxBodyBytes:  -1,
		QueueSize:     0,
		BatchSize:     -5,
		FlushInterval: 0,
		FlushTimeout:  0,
		Overflow:      "weird",
	})
	assert.Equal(t, 8192, config.MaxBodyBytes)
	assert.Equal(t, 4096, config.QueueSize)
	assert.Equal(t, 200, config.BatchSize)
	assert.Equal(t, time.Second, config.FlushInterval)
	assert.Equal(t, 5*time.Second, config.FlushTimeout)
	assert.Equal(t, overflowDrop, config.Overflow)
}

func TestNormalizeConfigKeepsValidValues(t *testing.T) {
	config := normalizeConfig(Config{
		Enabled:       true,
		MaxBodyBytes:  1024,
		QueueSize:     64,
		BatchSize:     16,
		FlushInterval: 200 * time.Millisecond,
		FlushTimeout:  time.Second,
		Overflow:      "block",
	})
	assert.Equal(t, 1024, config.MaxBodyBytes)
	assert.Equal(t, 64, config.QueueSize)
	assert.Equal(t, 16, config.BatchSize)
	assert.Equal(t, 200*time.Millisecond, config.FlushInterval)
	assert.Equal(t, overflowBlock, config.Overflow)
}

func TestNormalizeConfigDisabledLeavesValues(t *testing.T) {
	config := normalizeConfig(Config{Enabled: false, QueueSize: 7})
	assert.False(t, config.Enabled)
	assert.Equal(t, 7, config.QueueSize)
}
