package jobs

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	defaultBackoffBase = 5 * time.Second
	defaultBackoffMax  = time.Hour
)

type permanentError struct{ cause error }

func (e *permanentError) Error() string { return e.cause.Error() }
func (e *permanentError) Unwrap() error { return e.cause }

// Permanent marks a handler failure as non-retryable, preserving its cause.
// A nil error remains nil rather than turning successful work into a failure.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{cause: err}
}

type retryAfterError struct {
	delay time.Duration
	cause error
}

func (e *retryAfterError) Error() string { return e.cause.Error() }
func (e *retryAfterError) Unwrap() error { return e.cause }

// RetryAfter overrides the next retry delay, without bypassing the attempt cap
// or a Permanent marker. Delays must be between zero and 24 hours inclusive.
// Invalid delays return ErrInvalidRequest with the original cause; they do not
// create an override, so ordinary bounded retry policy still applies.
func RetryAfter(delay time.Duration, err error) error {
	if err == nil {
		return nil
	}
	if delay < 0 || delay > 24*time.Hour {
		return fmt.Errorf("%w: retry delay outside [0,24h]: %w", ErrInvalidRequest, err)
	}
	return &retryAfterError{delay: delay, cause: err}
}

// retryDelay uses the queued job's frozen attempt cap, not a possibly updated
// handler default. false means there is no retry; the caller distinguishes a
// successful nil error from dead-letter failure and owns transactional state.
func retryDelay(handler Handler, job Job, err error) (time.Duration, bool) {
	if err == nil || job.Attempt >= job.MaxAttempts {
		return 0, false
	}
	var permanent *permanentError
	if errors.As(err, &permanent) {
		return 0, false
	}
	var requested *retryAfterError
	if errors.As(err, &requested) {
		return requested.delay, true
	}
	ceiling := backoffCeiling(handler.BackoffBase, handler.BackoffMax, job.Attempt)
	half := ceiling / 2
	return half + time.Duration(rand.Int64N(int64(ceiling-half)+1)), true
}

// Saturate before multiplication, so even the largest valid duration cannot
// overflow into a negative retry delay at a high attempt number.
func backoffCeiling(base, maximum time.Duration, attempt int) time.Duration {
	delay := base
	for n := 1; n < attempt && delay < maximum; n++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	return min(delay, maximum)
}
