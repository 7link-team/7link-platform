package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHTTPStopper struct {
	shutdownErr error
	closeErr    error
	closed      bool
}

func (s *fakeHTTPStopper) Shutdown(context.Context) error { return s.shutdownErr }
func (s *fakeHTTPStopper) Close() error {
	s.closed = true
	return s.closeErr
}

func TestStopHTTPServerGracefully(t *testing.T) {
	server := &fakeHTTPStopper{}
	require.NoError(t, stopHTTPServer(t.Context(), server))
	assert.False(t, server.closed)
}

func TestStopHTTPServerForcesCloseAfterShutdownFailure(t *testing.T) {
	shutdownErr := errors.New("shutdown timeout")
	closeErr := errors.New("close failed")
	server := &fakeHTTPStopper{shutdownErr: shutdownErr, closeErr: closeErr}

	err := stopHTTPServer(t.Context(), server)
	assert.ErrorIs(t, err, shutdownErr)
	assert.ErrorIs(t, err, closeErr)
	assert.True(t, server.closed)
}

type fakeGRPCStopper struct {
	stop     chan struct{}
	stopOnce sync.Once
	graceful bool
	forced   bool
}

func newFakeGRPCStopper() *fakeGRPCStopper {
	return &fakeGRPCStopper{stop: make(chan struct{})}
}

func (s *fakeGRPCStopper) GracefulStop() {
	if s.graceful {
		return
	}
	<-s.stop
}

func (s *fakeGRPCStopper) Stop() {
	s.forced = true
	s.stopOnce.Do(func() { close(s.stop) })
}

func TestStopGRPCServerGracefully(t *testing.T) {
	server := newFakeGRPCStopper()
	server.graceful = true
	require.NoError(t, stopGRPCServer(t.Context(), server))
	assert.False(t, server.forced)
}

func TestStopGRPCServerForcesStopAfterDeadline(t *testing.T) {
	server := newFakeGRPCStopper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := stopGRPCServer(ctx, server)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, server.forced)
}
