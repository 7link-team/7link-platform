package audit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDoRecordsSuccessAndResult(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	result, err := Do(context.Background(), recorder, Spec{
		Entry:        KindJob,
		Operation:    "inventory.reconcile",
		ResourceType: "inventory",
	}, func(ctx context.Context) (int, error) {
		Annotate(ctx, Action("inventory.reconcile"), Resource("inventory", "wh-1"))
		return 7, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 7, result)

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, KindJob, event.Entry)
	assert.Equal(t, "inventory.reconcile", event.Action)
	assert.Equal(t, "wh-1", event.ResourceID)
	assert.Equal(t, OutcomeSuccess, event.Outcome)
	assert.GreaterOrEqual(t, event.DurationMS, float64(0))
}

func TestDoRecordsFailure(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	boom := errors.New("reconcile failed")
	_, err := Do(context.Background(), recorder, Spec{Operation: "x"}, func(context.Context) (int, error) {
		return 0, boom
	})
	assert.ErrorIs(t, err, boom)

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, OutcomeFailure, event.Outcome)
	assert.Contains(t, event.Error, "reconcile failed")
}

func TestDoRecordsGRPCStatus(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	_, err := Do(context.Background(), recorder, Spec{Operation: "x"}, func(context.Context) (int, error) {
		return 0, status.Error(codes.InvalidArgument, "bad input")
	})
	assert.Error(t, err)

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	assert.Equal(t, int(codes.InvalidArgument), event.StatusCode)
	assert.Contains(t, event.Error, "bad input")
}

func TestRunRecords(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	err := Run(context.Background(), recorder, Spec{Entry: KindSchedule, Operation: "cron.cleanup"}, func(context.Context) error {
		return nil
	})
	require.NoError(t, err)
	eventually(t, func() bool { return sink.count() == 1 })
	assert.Equal(t, KindSchedule, sink.snapshot()[0].Entry)
}

func TestDoWithNilRecorderStillRuns(t *testing.T) {
	result, err := Do(context.Background(), nil, Spec{Operation: "x"}, func(context.Context) (int, error) {
		return 5, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 5, result)
}

func TestDoStampsOccurredAtAtOperationStart(t *testing.T) {
	sink := &memorySink{}
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), sink)
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	started := time.Now().UTC()
	_, err := Do(context.Background(), recorder, Spec{Operation: "slow.op"}, func(context.Context) (int, error) {
		time.Sleep(5 * time.Millisecond)
		return 1, nil
	})
	require.NoError(t, err)
	finished := time.Now().UTC()

	eventually(t, func() bool { return sink.count() == 1 })
	event := sink.snapshot()[0]
	// occurred_at reflects the start, not the enqueue (end) time.
	assert.False(t, event.OccurredAt.IsZero())
	assert.True(t, event.OccurredAt.Equal(started) || event.OccurredAt.After(started))
	assert.True(t, event.OccurredAt.Before(finished))
	assert.GreaterOrEqual(t, event.DurationMS, float64(5))
}

func TestOpenSpecDefaults(t *testing.T) {
	entry := openSpec(context.Background(), Spec{})
	assert.Equal(t, KindInternal, entry.event.Entry)
	assert.Equal(t, AccessWrite, entry.event.Access)
	assert.NotEmpty(t, entry.event.Operation)
	assert.Equal(t, ActorAnonymous, entry.event.Actor.Type)

	// Explicit operation string is preserved.
	entry = openSpec(context.Background(), Spec{Operation: "named.op"})
	assert.Equal(t, "named.op", entry.event.Operation)
}

func TestAnnotateConcurrentWithFinalizeDoesNotRace(t *testing.T) {
	// A goroutine that keeps annotating the request context after the handler
	// returns must not race with the finalizer snapshotting the event. Run under
	// `go test -race` to verify the inflight mutex serializes both sides.
	recorder := NewAsyncRecorder("svc", "1", "inst", recorderConfig(), &memorySink{})
	recorder.Start()
	defer func() { require.NoError(t, recorder.Stop(context.Background())) }()

	const operations = 40
	const lateWrites = 30
	var wg sync.WaitGroup
	for range operations {
		wg.Go(func() {
			_, _ = Do(context.Background(), recorder, Spec{Entry: KindJob, Operation: "op"}, func(ctx context.Context) (struct{}, error) {
				go func() {
					for range lateWrites {
						Annotate(ctx, Metadata("late", "1"), Resource("order", "42"))
					}
				}()
				return struct{}{}, nil
			})
		})
	}
	wg.Wait()
}
