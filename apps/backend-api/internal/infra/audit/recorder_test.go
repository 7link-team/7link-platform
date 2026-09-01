package audit

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recorderConfig() RecorderConfig {
	return RecorderConfig{
		Enabled:       true,
		QueueSize:     16,
		BatchSize:     4,
		FlushInterval: 10 * time.Millisecond,
		FlushTimeout:  time.Second,
	}
}

func TestRecorderFlushesOnInterval(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	recorder.Record(context.Background(), Event{Operation: "op", Access: AccessWrite})
	recorder.Record(context.Background(), Event{Operation: "op", Access: AccessWrite})
	eventually(t, func() bool { return sink.count() == 2 })

	events := sink.snapshot()
	require.Len(t, events, 2)
	for _, event := range events {
		assert.NotEmpty(t, event.ID)
		assert.Equal(t, "svc", event.Service)
		assert.Equal(t, "1", event.Version)
		assert.Equal(t, "inst", event.Instance)
		assert.Equal(t, KindInternal, event.Entry) // defaulted
		assert.Equal(t, OutcomeSuccess, event.Outcome)
		assert.False(t, event.OccurredAt.IsZero())
	}
}

func TestRecorderFlushesOnBatchSize(t *testing.T) {
	sink := &memorySink{}
	config := recorderConfig()
	config.FlushInterval = time.Hour // force batch-size trigger
	recorder := NewAsyncRecorder("svc", "1", "inst", config, sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	for i := 0; i < 4; i++ {
		recorder.Record(context.Background(), Event{Operation: "op"})
	}
	eventually(t, func() bool { return sink.count() == 4 })
}

func TestRecorderEnrichUsesProvidedValues(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	recorder.Record(context.Background(), Event{
		ID: "given-id", Service: "other", Entry: KindJob, Outcome: OutcomeFailure,
	})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.True(t, isSnowflakeID(event.ID))
	assert.Equal(t, "given-id", event.Metadata["raw_event_id"])
	assert.Equal(t, "other", event.Service) // explicit wins
	assert.Equal(t, KindJob, event.Entry)
	assert.Equal(t, OutcomeFailure, event.Outcome)
}

func TestRecorderDropWhenDisabledOrClosed(t *testing.T) {
	// A deliberately disabled recorder is a no-op: it must not count drops, or
	// audit_events_dropped_total would look like events are being lost.
	config := recorderConfig()
	config.Enabled = false
	recorder := NewAsyncRecorder("svc", "1", "inst", config, &memorySink{})
	recorder.Record(context.Background(), Event{Operation: "op"})
	assert.Zero(t, recorder.Stats().Dropped)

	recorder = NewAsyncRecorder("svc", "1", "inst", recorderConfig(), &memorySink{})
	recorder.Start()
	require.NoError(t, recorder.Stop(context.Background()))
	recorder.Record(context.Background(), Event{Operation: "after-stop"})
	assert.Equal(t, uint64(1), recorder.Stats().Dropped)
	// Record after nil receiver is safe.
	var nilRecorder *AsyncRecorder
	assert.NotPanics(t, func() { nilRecorder.Record(context.Background(), Event{}) })
	// Stop on nil receiver is safe.
	assert.NoError(t, nilRecorder.Stop(context.Background()))
}

func TestRecorderOverflowDropsAndCounts(t *testing.T) {
	// The consumer is intentionally not started: the bounded channel then
	// behaves deterministically, and enqueue-side overflow is what this test
	// exercises.
	config := recorderConfig()
	config.QueueSize = 1
	config.BatchSize = 100
	config.FlushInterval = time.Hour
	recorder := NewAsyncRecorder("svc", "1", "inst", config, &memorySink{})
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	for i := 0; i < 5; i++ {
		recorder.Record(context.Background(), Event{Operation: "op"})
	}
	stats := recorder.Stats()
	assert.Equal(t, uint64(4), stats.Dropped)
	assert.Equal(t, int64(1), stats.Queued)
}

func TestRecorderOverflowBlockHonorsContext(t *testing.T) {
	config := recorderConfig()
	config.QueueSize = 1
	config.BatchSize = 100
	config.FlushInterval = time.Hour
	config.OverflowBlock = true
	recorder := NewAsyncRecorder("svc", "1", "inst", config, &memorySink{})
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	recorder.Record(context.Background(), Event{Operation: "first"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		recorder.Record(ctx, Event{Operation: "blocked"})
		close(done)
	}()
	// Cancel while the second send blocks on the full, un-drained queue.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked record did not unblock on context cancel")
	}
	assert.Equal(t, uint64(1), recorder.Stats().Dropped)
}

func TestRecorderFailureAccountingWhenSinkFails(t *testing.T) {
	sink := &memorySink{fail: true}
	config := recorderConfig()
	config.FlushInterval = 5 * time.Millisecond
	recorder := NewAsyncRecorder("svc", "1", "inst", config, sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	recorder.Record(context.Background(), Event{Operation: "op"})
	eventually(t, func() bool { return recorder.Stats().Failed == 1 })
	assert.Equal(t, uint64(1), recorder.Stats().SinkErrs)
	// Dropped stays zero; the event was accepted but persistence failed.
	assert.Zero(t, recorder.Stats().Dropped)
}

func TestRecorderCountsPartialSinkFailureAsDelivered(t *testing.T) {
	config := recorderConfig()
	config.BatchSize = 1
	recorder := NewAsyncRecorder("svc", "1", "inst", config, &memorySink{}, &memorySink{fail: true})
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	recorder.Record(context.Background(), Event{Operation: "op"})
	eventually(t, func() bool { return recorder.Stats().Success == 1 })
	stats := recorder.Stats()
	assert.Zero(t, stats.Failed)
	assert.Equal(t, uint64(1), stats.SinkErrs)
}

func TestRecorderStopDrainsPendingEvents(t *testing.T) {
	sink := &memorySink{}
	config := recorderConfig()
	config.BatchSize = 100
	config.FlushInterval = time.Hour // do not flush on ticker
	recorder := NewAsyncRecorder("svc", "1", "inst", config, sink)
	recorder.Start()

	for i := 0; i < 3; i++ {
		recorder.Record(context.Background(), Event{Operation: "op"})
	}
	require.NoError(t, recorder.Stop(context.Background()))
	assert.Equal(t, 3, sink.count())
	// A second Stop is a no-op.
	assert.NoError(t, recorder.Stop(context.Background()))
}

func TestRecorderStopRespectsContext(t *testing.T) {
	sink := &memorySink{fail: true}
	config := recorderConfig()
	config.FlushInterval = 5 * time.Millisecond
	recorder := NewAsyncRecorder("svc", "1", "inst", config, sink)
	recorder.Start()
	recorder.Record(context.Background(), Event{Operation: "op"})
	// An already-expired context cannot stall the drain indefinitely, but the
	// final flush still completes (bounded internally) before Stop returns.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	_ = recorder.Stop(ctx)
}

func TestRecorderClampsConfiguration(t *testing.T) {
	recorder := NewAsyncRecorder("svc", "1", "inst", RecorderConfig{}, &memorySink{})
	assert.Equal(t, 4096, recorder.cfg.QueueSize)
	assert.Equal(t, 200, recorder.cfg.BatchSize)
	assert.Equal(t, time.Second, recorder.cfg.FlushInterval)
	assert.Equal(t, 5*time.Second, recorder.cfg.FlushTimeout)
}

func TestRecorderAssignsSnowflakeIDs(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { assert.NoError(t, recorder.Stop(context.Background())) }()

	recorder.Record(context.Background(), Event{Operation: "op"})
	eventually(t, func() bool { return sink.count() == 1 })
	id := sink.snapshot()[0].ID
	// Snowflake IDs are positive decimal integers, not UUIDs.
	require.NotEmpty(t, id)
	parsed, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	assert.Positive(t, parsed)
	assert.NotContains(t, id, "-")
}

func TestRecorderUsesUUIDFallbackWithoutDroppingEvent(t *testing.T) {
	sink := &memorySink{}
	config := recorderConfig()
	config.IDGenerator = func() (int64, error) { return 0, errors.New("generator unavailable") }
	recorder := NewAsyncRecorder("svc", "1", "inst", config, sink)
	recorder.Start()
	defer func() { assert.NoError(t, recorder.Stop(context.Background())) }()

	recorder.Record(t.Context(), Event{ID: "external-id", Operation: "op"})
	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	_, err := uuid.Parse(event.ID)
	require.NoError(t, err)
	assert.Equal(t, "external-id", event.Metadata["raw_event_id"])
	assert.Zero(t, recorder.Stats().Dropped)
}

type cancelAwareSink struct {
	started  chan struct{}
	returned chan struct{}
	once     sync.Once
}

func (sink *cancelAwareSink) Write(ctx context.Context, _ []Event) error {
	sink.once.Do(func() { close(sink.started) })
	<-ctx.Done()
	close(sink.returned)
	return ctx.Err()
}

func TestRecorderStopCancelsFlushAndReturnsStableResult(t *testing.T) {
	sink := &cancelAwareSink{started: make(chan struct{}), returned: make(chan struct{})}
	config := recorderConfig()
	config.BatchSize = 1
	config.FlushTimeout = time.Hour
	recorder := NewAsyncRecorder("svc", "1", "inst", config, sink)
	recorder.Start()
	recorder.Record(t.Context(), Event{Operation: "op"})
	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("sink did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	firstErr := recorder.Stop(ctx)
	require.ErrorIs(t, firstErr, context.DeadlineExceeded)
	select {
	case <-sink.returned:
	default:
		t.Fatal("Stop returned before the flush worker exited")
	}
	secondErr := recorder.Stop(context.Background())
	assert.EqualError(t, secondErr, firstErr.Error())
	assert.Zero(t, recorder.Stats().Queued)
}

func TestRecorderStopBeforeStartDrainsQueue(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Record(t.Context(), Event{Operation: "queued"})
	require.NoError(t, recorder.Stop(context.Background()))
	assert.Equal(t, 1, sink.count())
	assert.Zero(t, recorder.Stats().Queued)
}

func TestRecorderConcurrentRecordAndStop(t *testing.T) {
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), &memorySink{})
	recorder.Start()
	producerCtx, cancelProducers := context.WithCancel(context.Background())
	var producers sync.WaitGroup
	for range 4 {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for {
				select {
				case <-producerCtx.Done():
					return
				default:
					recorder.Record(producerCtx, Event{Operation: "concurrent"})
				}
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	stopResult := make(chan error, 1)
	go func() { stopResult <- recorder.Stop(context.Background()) }()
	cancelProducers()
	producers.Wait()
	require.NoError(t, <-stopResult)
	assert.Zero(t, recorder.Stats().Queued)
}
