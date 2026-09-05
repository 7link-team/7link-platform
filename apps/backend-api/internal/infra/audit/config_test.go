package audit

import (
	"testing"
	"time"

	"7link.com/platform/pkg/appenv"
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

// TestNormalizeConfigCanonicalizesOverflow pins the written-back form. The
// recorder compares the value exactly, so an accepted-but-unnormalized string
// such as " BLOCK " would silently fall back to dropping events.
func TestNormalizeConfigCanonicalizesOverflow(t *testing.T) {
	cases := []struct {
		name     string
		overflow string
		expected string
	}{
		{name: "canonical block", overflow: "block", expected: overflowBlock},
		{name: "padded uppercase block", overflow: " BLOCK ", expected: overflowBlock},
		{name: "mixed case block", overflow: "Block", expected: overflowBlock},
		{name: "padded drop", overflow: "\tdrop\n", expected: overflowDrop},
		{name: "unknown mode", overflow: "weird", expected: overflowDrop},
		{name: "empty", overflow: "", expected: overflowDrop},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := normalizeConfig(Config{Enabled: true, Overflow: testCase.overflow})
			assert.Equal(t, testCase.expected, config.Overflow)
		})
	}
}

// TestModuleSelectsBlockingOverflowForPaddedValue proves the normalization
// reaches the recorder rather than stopping at the config struct.
func TestModuleSelectsBlockingOverflowForPaddedValue(t *testing.T) {
	sink := &memorySink{}
	auditModule := NewModule(appenv.NewRuntime(appenv.NewIdentity("svc", "1")), Config{
		Enabled:  true,
		Overflow: " BLOCK ",
	}, WithSinks(sink))
	assert.Equal(t, overflowBlock, auditModule.cfg.Overflow)
	assert.True(t, auditModule.recorder.cfg.OverflowBlock)
}
