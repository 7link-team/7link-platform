package control

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestSupervisorBackoffCapAndPermanentFailures(t *testing.T) {
	var delays []time.Duration
	calls := 0
	fatal := errors.New("local journal invalid")
	err := supervise(t.Context(), io.Discard, func() error {
		calls++
		if calls == 9 {
			return fatal
		}
		return ErrConnection
	}, func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }, func() time.Time { return time.Time{} })
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second}
	if !errors.Is(err, fatal) || !reflect.DeepEqual(want, delays) {
		t.Fatal(err, delays)
	}
	for _, status := range []int{400, 401, 403, 404, 422} {
		if reconnectable(&APIError{Status: status}) {
			t.Fatal("permanent error retried", status)
		}
	}
	for _, status := range []int{408, 409, 429, 500, 503} {
		if !reconnectable(&APIError{Status: status}) {
			t.Fatal("transient error not retried", status)
		}
	}
}
func TestSupervisorCancellationAndStableReset(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(waitReconnect(ctx, time.Hour), context.Canceled) {
		t.Fatal("backoff ignores cancellation")
	}
	calls := 0
	now := time.Time{}
	delays := []time.Duration{}
	sentinel := errors.New("done")
	err := supervise(t.Context(), io.Discard, func() error {
		calls++
		if calls == 3 {
			now = now.Add(time.Minute)
		}
		return ErrConnection
	}, func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		if len(delays) == 3 {
			return sentinel
		}
		return nil
	}, func() time.Time { return now })
	if !errors.Is(err, sentinel) || !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second, time.Second}) {
		t.Fatal(err, delays)
	}
}
