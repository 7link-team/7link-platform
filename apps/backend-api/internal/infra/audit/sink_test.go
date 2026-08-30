package audit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// memorySink is the package's central Sink test double: it collects events for
// assertions and can be configured to fail every write to exercise the
// recorder's failure accounting. It lives next to the Sink contract it fakes.
type memorySink struct {
	mu     sync.Mutex
	events []Event
	fail   bool
}

func (sink *memorySink) Write(_ context.Context, events []Event) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.fail {
		return errSinkFailure
	}
	sink.events = append(sink.events, events...)
	return nil
}

func (sink *memorySink) snapshot() []Event {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]Event(nil), sink.events...)
}

func (sink *memorySink) count() int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return len(sink.events)
}

var errSinkFailure = errString("audit: test sink failure")

type errString string

func (err errString) Error() string { return string(err) }

// eventually polls fn until it returns true or the deadline passes. The audit
// pipeline is asynchronous, so assertions on flushed events must wait.
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestMultiSinkBehavior(t *testing.T) {
	ok := &memorySink{}
	fail := &memorySink{fail: true}
	// Any sink succeeding means the batch is delivered.
	assert.NoError(t, multiSink{ok, fail}.Write(context.Background(), []Event{{}}))
	// All sinks failing returns an error.
	assert.Error(t, multiSink{fail}.Write(context.Background(), []Event{{}}))
	// No sinks at all is an error (event cannot reach anywhere).
	assert.Error(t, multiSink{}.Write(context.Background(), []Event{{}}))
	// Empty batch is a no-op.
	assert.NoError(t, multiSink{}.Write(context.Background(), nil))
	assert.Equal(t, "audit: test sink failure", errSinkFailure.Error())
}

func TestMultiSinkReportsPartialFailures(t *testing.T) {
	result := multiSink{&memorySink{}, &memorySink{fail: true}}.write(context.Background(), []Event{{}})
	assert.True(t, result.delivered)
	assert.Equal(t, uint64(1), result.failures)
	assert.ErrorIs(t, result.err, errSinkFailure)
}
