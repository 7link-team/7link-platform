package jobs

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetryMarkersPreserveCauseAndAttemptCap(t *testing.T) {
	cause := errors.New("temporary failure")
	handler, err := normalizeHandler("jobs", catalogHandler("jobs.work"))
	require.NoError(t, err)
	job := Job{Attempt: 1, MaxAttempts: 8}
	for _, failure := range []error{Permanent(cause), fmt.Errorf("wrapped: %w", Permanent(cause)), RetryAfter(time.Minute, Permanent(cause)), Permanent(RetryAfter(time.Minute, cause)), errors.Join(RetryAfter(time.Minute, cause), Permanent(errors.New("permanent")))} {
		require.ErrorIs(t, failure, cause)
		_, retry := retryDelay(handler, job, failure)
		require.False(t, retry)
	}
	require.Equal(t, cause.Error(), Permanent(cause).Error())
	require.Equal(t, cause.Error(), RetryAfter(time.Minute, cause).Error())
	for _, delay := range []time.Duration{0, time.Nanosecond, 2 * time.Minute, 24 * time.Hour} {
		failure := fmt.Errorf("handler: %w", RetryAfter(delay, cause))
		require.ErrorIs(t, failure, cause)
		actual, retry := retryDelay(handler, job, failure)
		require.True(t, retry)
		require.Equal(t, delay, actual)
		job.Attempt = 8
		_, retry = retryDelay(handler, job, failure)
		require.False(t, retry, "an override cannot extend the frozen attempt cap")
		job.Attempt = 1
	}
	require.NoError(t, Permanent(nil))
	require.NoError(t, RetryAfter(time.Minute, nil))
	_, retry := retryDelay(handler, job, nil)
	require.False(t, retry)
	handler.MaxAttempts = 1
	_, retry = retryDelay(handler, job, cause)
	require.True(t, retry, "new handler settings must not retroactively shorten an enqueued job")
}

func TestRetryInvalidDelayUsesOrdinaryBoundedPolicy(t *testing.T) {
	handler, err := normalizeHandler("jobs", catalogHandler("jobs.work"))
	require.NoError(t, err)
	cause := errors.New("failed")
	for _, delay := range []time.Duration{-1, 24*time.Hour + 1, time.Duration(math.MaxInt64)} {
		failure := RetryAfter(delay, cause)
		require.ErrorIs(t, failure, ErrInvalidRequest)
		require.ErrorIs(t, failure, cause)
		actual, retry := retryDelay(handler, Job{Attempt: 1, MaxAttempts: 8}, failure)
		require.True(t, retry)
		require.GreaterOrEqual(t, actual, 2500*time.Millisecond)
		require.LessOrEqual(t, actual, 5*time.Second)
	}
}

func TestRetryEqualJitterAndExponentialSaturation(t *testing.T) {
	handler, err := normalizeHandler("jobs", catalogHandler("jobs.work"))
	require.NoError(t, err)
	cause := errors.New("failed")
	for attempt := 1; attempt <= 50; attempt++ {
		expected := time.Hour
		if attempt <= 10 {
			expected = 5 * time.Second * time.Duration(1<<(attempt-1))
		}
		require.Equal(t, expected, backoffCeiling(handler.BackoffBase, handler.BackoffMax, attempt))
		for range 64 {
			delay, retry := retryDelay(handler, Job{Attempt: attempt, MaxAttempts: 51}, cause)
			require.True(t, retry)
			require.GreaterOrEqual(t, delay, expected/2)
			require.LessOrEqual(t, delay, expected)
		}
	}
	_, retry := retryDelay(handler, Job{Attempt: 8, MaxAttempts: 8}, cause)
	require.False(t, retry)
	for _, tc := range []struct {
		base, max time.Duration
		attempt   int
		want      time.Duration
	}{
		{5 * time.Second, time.Hour, 1, 5 * time.Second},
		{time.Hour, time.Hour, 50, time.Hour},
		{time.Duration(math.MaxInt64/2 + 1), time.Duration(math.MaxInt64), 2, time.Duration(math.MaxInt64)},
		{time.Millisecond, time.Duration(math.MaxInt64), math.MaxInt, time.Duration(math.MaxInt64)},
	} {
		require.Equal(t, tc.want, backoffCeiling(tc.base, tc.max, tc.attempt))
	}
	handler.BackoffBase = time.Duration(math.MaxInt64)
	handler.BackoffMax = time.Duration(math.MaxInt64)
	for range 10 {
		delay, retry := retryDelay(handler, Job{Attempt: 2, MaxAttempts: 3}, cause)
		require.True(t, retry)
		require.GreaterOrEqual(t, delay, time.Duration(math.MaxInt64/2))
		require.LessOrEqual(t, delay, time.Duration(math.MaxInt64))
	}
}
