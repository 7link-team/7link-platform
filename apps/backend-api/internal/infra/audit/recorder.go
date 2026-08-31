package audit

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/telemetry"
	"github.com/google/uuid"
)

// Recorder is the audit service contract exposed to the application. Any module
// that performs work on behalf of a user records it through this interface.
type Recorder interface {
	// Record enqueues one event for asynchronous persistence. The event is
	// enriched with identity defaults (service, version, instance, trace/span
	// from the context) before enqueue. Record never blocks the caller on sink
	// I/O; when the queue is full the event is dropped or the call blocks,
	// depending on the configured overflow mode.
	Record(ctx context.Context, event Event)
}

// RecorderConfig tunes the asynchronous dispatch pipeline.
type RecorderConfig struct {
	Enabled       bool
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
	FlushTimeout  time.Duration
	OverflowBlock bool
	// IDGenerator produces the platform snowflake ID used as the event primary
	// key. The application injects guid.Source.Next via the module service
	// registry; when nil, a fixed worker-0 snowflake generator is used so the
	// recorder is usable standalone and in tests.
	IDGenerator func() (int64, error)
}

// AsyncRecorder is the concrete asynchronous dispatch pipeline. Events are
// enqueued by Record and written to the configured sinks in batches by a single
// background goroutine.
type AsyncRecorder struct {
	sinks multiSink
	cfg   RecorderConfig

	service  string
	version  string
	instance string

	mu    sync.RWMutex
	newID func() (int64, error)

	ch chan Event
	// stopCh is closed by Stop so producers blocked in overflow-block mode can
	// bail out instead of selecting on a channel that is about to close.
	stopCh chan struct{}
	// enqueueMu makes channel closure atomic with respect to producers. Stop
	// closes stopCh first so a producer blocked on a full queue can release its
	// read lock before the queue itself is closed.
	enqueueMu sync.RWMutex

	closed atomic.Bool

	queued  atomic.Int64
	dropped atomic.Uint64
	failed  atomic.Uint64
	success atomic.Uint64
	sinkErr atomic.Uint64

	startOnce  sync.Once
	stopOnce   sync.Once
	runCtx     context.Context
	runCancel  context.CancelFunc
	workerDone chan struct{}
	stopDone   chan struct{}
	stopErr    error
}

// NewAsyncRecorder builds a recorder with one or more sinks. At least one sink
// is required; the caller normally supplies the always-on slog sink. Non-positive
// tuning values are clamped to safe defaults so a zero-value config cannot
// produce a zero-size queue or a panicking ticker.
func NewAsyncRecorder(
	service, version, instance string,
	config RecorderConfig,
	sinks ...Sink,
) *AsyncRecorder {
	config.QueueSize = clampPositive(config.QueueSize, 4096)
	config.BatchSize = clampPositive(config.BatchSize, 200)
	config.FlushInterval = clampDuration(config.FlushInterval, time.Second)
	config.FlushTimeout = clampDuration(config.FlushTimeout, 5*time.Second)
	runCtx, runCancel := context.WithCancel(context.Background())
	return &AsyncRecorder{
		sinks:      multiSink(sinks),
		cfg:        config,
		service:    service,
		version:    version,
		instance:   instance,
		newID:      config.IDGenerator,
		ch:         make(chan Event, config.QueueSize),
		stopCh:     make(chan struct{}),
		runCtx:     runCtx,
		runCancel:  runCancel,
		workerDone: make(chan struct{}),
		stopDone:   make(chan struct{}),
	}
}

// SetIDGenerator replaces the snowflake ID generator. The application calls it
// during module service injection to bind the recorder to the wuid-managed
// guid.Source, so worker IDs are unique across replicas.
func (recorder *AsyncRecorder) SetIDGenerator(fn func() (int64, error)) {
	if fn == nil {
		return
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.newID = fn
}

// idGenerator returns the active snowflake generator, defaulting to a fixed
// worker-0 generator when none was configured.
func (recorder *AsyncRecorder) idGenerator() func() (int64, error) {
	recorder.mu.RLock()
	defer recorder.mu.RUnlock()
	if recorder.newID != nil {
		return recorder.newID
	}
	return defaultIDGenerator()
}

// defaultIDGenerator is a standalone worker-0 snowflake generator used before
// service injection and in tests. The application overrides it via
// SetIDGenerator so replicas do not share a worker ID.
func defaultIDGenerator() func() (int64, error) {
	generator, err := guid.New(0)
	if err != nil {
		return func() (int64, error) { return 0, err }
	}
	return generator.Next
}

func clampPositive(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func clampDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

// Start launches the background dispatcher. It is safe to call once; the
// dispatcher starts only on the first call.
func (recorder *AsyncRecorder) Start() {
	if recorder == nil || !recorder.cfg.Enabled || recorder.closed.Load() {
		return
	}
	recorder.startDispatcher()
}

func (recorder *AsyncRecorder) startDispatcher() {
	recorder.startOnce.Do(func() {
		go recorder.run()
	})
}

// Record enqueues an event. A missing or invalid caller ID is replaced with a
// platform snowflake and preserved as metadata.raw_event_id when supplied. If
// the managed generator is temporarily unavailable, a UUID keeps non-database
// sinks lossless; the BIGINT database sink rejects that event explicitly.
func (recorder *AsyncRecorder) Record(ctx context.Context, event Event) {
	if recorder == nil || !recorder.cfg.Enabled {
		return
	}
	if recorder.closed.Load() {
		recorder.dropped.Add(1)
		return
	}
	if !isSnowflakeID(event.ID) {
		if event.ID != "" {
			if event.Metadata == nil {
				event.Metadata = make(map[string]string, 1)
			}
			event.Metadata["raw_event_id"] = event.ID
		}
		id, err := recorder.idGenerator()()
		if err != nil || id <= 0 {
			event.ID = uuid.NewString()
			slog.Warn("audit event using UUID fallback: snowflake generation failed",
				"error", err,
				"event_id", event.ID,
			)
		} else {
			event.ID = strconv.FormatInt(id, 10)
		}
	}
	recorder.enrich(ctx, &event)

	recorder.enqueueMu.RLock()
	defer recorder.enqueueMu.RUnlock()
	if recorder.closed.Load() {
		recorder.dropped.Add(1)
		return
	}
	recorder.queued.Add(1)
	if sent := recorder.trySend(ctx, event); sent {
		return
	}
	recorder.queued.Add(-1)
	recorder.dropped.Add(1)
	if recorder.cfg.OverflowBlock || recorder.dropped.Load()%1024 == 1 {
		slog.Warn("audit event dropped",
			"dropped_total", recorder.dropped.Load(),
			"mode", recorder.overflowMode(),
		)
	}
}

func isSnowflakeID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

// trySend places the event on the queue, returning false when the queue is full
// (drop mode), the context is done, or shutdown has begun. A send racing a
// shutdown has begun. Channel closure is synchronized by enqueueMu.
func (recorder *AsyncRecorder) trySend(ctx context.Context, event Event) bool {
	if recorder.cfg.OverflowBlock {
		select {
		case recorder.ch <- event:
			return true
		case <-ctx.Done():
			return false
		case <-recorder.stopCh:
			return false
		}
	}
	select {
	case recorder.ch <- event:
		return true
	default:
		return false
	}
}

// overflowMode names the configured behavior for diagnostics.
func (recorder *AsyncRecorder) overflowMode() string {
	if recorder.cfg.OverflowBlock {
		return "block"
	}
	return "drop"
}

// enrich fills identity defaults that producers should not have to repeat.
func (recorder *AsyncRecorder) enrich(ctx context.Context, event *Event) {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.Service == "" {
		event.Service = recorder.service
	}
	if event.Version == "" {
		event.Version = recorder.version
	}
	if event.Instance == "" {
		event.Instance = recorder.instance
	}
	if event.TraceID == "" {
		event.TraceID = telemetry.TraceID(ctx)
	}
	if event.SpanID == "" {
		event.SpanID = telemetry.SpanID(ctx)
	}
	if event.Outcome == "" {
		event.Outcome = OutcomeSuccess
	}
	if event.Entry == "" {
		event.Entry = KindInternal
	}
}

// Stop drains and flushes pending events, then stops the dispatcher. It is safe
// to call once; subsequent calls return the result of the first. The context
// bounds the drain; a drain that exceeds the deadline returns an error and its
// unfinished flush is bounded internally by the flush timeout.
func (recorder *AsyncRecorder) Stop(ctx context.Context) error {
	if recorder == nil {
		return nil
	}
	recorder.stopOnce.Do(func() {
		recorder.closed.Store(true)
		close(recorder.stopCh)
		if recorder.cfg.Enabled {
			recorder.startDispatcher()
			recorder.enqueueMu.Lock()
			close(recorder.ch)
			recorder.enqueueMu.Unlock()

			if err := ctx.Err(); err != nil {
				recorder.stopErr = fmt.Errorf("audit drain: %w", err)
				recorder.runCancel()
				<-recorder.workerDone
			} else {
				select {
				case <-recorder.workerDone:
				case <-ctx.Done():
					recorder.stopErr = fmt.Errorf("audit drain: %w", ctx.Err())
					recorder.runCancel()
					<-recorder.workerDone
				}
			}
		}
		recorder.runCancel()
		close(recorder.stopDone)
	})
	<-recorder.stopDone
	return recorder.stopErr
}

// run drains the queue and flushes batches. It exits when the queue is closed
// after a final flush of whatever remains queued.
func (recorder *AsyncRecorder) run() {
	defer close(recorder.workerDone)
	batch := make([]Event, 0, recorder.cfg.BatchSize)
	ticker := time.NewTicker(recorder.cfg.FlushInterval)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(recorder.runCtx, recorder.cfg.FlushTimeout)
		defer cancel()
		result := recorder.sinks.write(ctx, batch)
		recorder.sinkErr.Add(result.failures)
		if !result.delivered {
			recorder.failed.Add(uint64(len(batch)))
			slog.Error("audit flush failed", "error", result.err, "count", len(batch))
		} else {
			recorder.success.Add(uint64(len(batch)))
		}
		recorder.queued.Add(-int64(len(batch)))
		batch = batch[:0]
	}

	for {
		select {
		case event, ok := <-recorder.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) >= recorder.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Stats exposes pipeline counters for the Prometheus collector and tests.
func (recorder *AsyncRecorder) Stats() RecorderStats {
	return RecorderStats{
		Queued:   recorder.queued.Load(),
		Dropped:  recorder.dropped.Load(),
		Failed:   recorder.failed.Load(),
		Success:  recorder.success.Load(),
		SinkErrs: recorder.sinkErr.Load(),
	}
}

// RecorderStats is a snapshot of the pipeline counters.
type RecorderStats struct {
	Queued   int64
	Dropped  uint64
	Failed   uint64
	Success  uint64
	SinkErrs uint64
}
