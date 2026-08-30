package audit

import (
	"context"
	"errors"
	"log/slog"
)

// Sink persists audit events. Sinks are called by the Recorder with batches and
// must be safe for sequential, single-goroutine use. Write returns an error when
// the sink could not persist the batch.
type Sink interface {
	// Write persists one batch of events. An empty batch is a no-op.
	Write(ctx context.Context, events []Event) error
}

// multiSink fans a batch out to every underlying sink. It reports an error only
// when no sink accepted the batch, so an event recorded by a healthy sink is
// not reported as failed just because the database is temporarily down.
// Individual sink failures are still logged so a silently failing database
// sink cannot hide behind the always-on log sink.
type multiSink []Sink

func (sinks multiSink) Write(ctx context.Context, events []Event) error {
	result := sinks.write(ctx, events)
	if result.delivered {
		return nil
	}
	return result.err
}

type sinkResult struct {
	delivered bool
	failures  uint64
	err       error
}

func (sinks multiSink) write(ctx context.Context, events []Event) sinkResult {
	if len(events) == 0 {
		return sinkResult{delivered: true}
	}
	var errs []error
	delivered := false
	var failures uint64
	for _, sink := range sinks {
		if sink == nil {
			continue
		}
		if err := sink.Write(ctx, events); err != nil {
			errs = append(errs, err)
			failures++
			slog.Warn("audit sink write failed", "error", err, "events", len(events))
		} else {
			delivered = true
		}
	}
	if len(errs) > 0 {
		return sinkResult{delivered: delivered, failures: failures, err: errors.Join(errs...)}
	}
	if !delivered {
		return sinkResult{err: errors.New("audit: no sink accepted the batch")}
	}
	return sinkResult{delivered: true}
}
