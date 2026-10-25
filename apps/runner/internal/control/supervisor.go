package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Daemon reconnects with bounded backoff, after daemonOnce has stopped all child
// processes. The protected journal resolves interrupted attempts as LOST; it
// never relaunches an attempt. Permanent credential/config/protocol errors stop.
func Daemon(ctx context.Context, store *Store, out io.Writer) error {
	return supervise(ctx, out, func() error { return daemonOnce(ctx, store, out) }, waitReconnect, time.Now)
}
func waitReconnect(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func reconnectable(err error) bool {
	if errors.Is(err, ErrConnection) {
		return true
	}
	var api *APIError
	return errors.As(err, &api) && (api.Status == 408 || api.Status == 409 || api.Status == 429 || api.Status >= 500)
}
func supervise(ctx context.Context, out io.Writer, run func() error, wait func(context.Context, time.Duration) error, now func() time.Time) error {
	delay := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		started := now()
		err := run()
		if err == nil || ctx.Err() != nil || !reconnectable(err) {
			return err
		}
		if now().Sub(started) >= time.Minute {
			delay = time.Second
		}
		if _, e := fmt.Fprintln(out, "Runner disconnected; local execution stopped. Reconnecting with bounded backoff."); e != nil {
			return e
		}
		if e := wait(ctx, delay); e != nil {
			return e
		}
		delay = min(delay*2, 30*time.Second)
	}
}
