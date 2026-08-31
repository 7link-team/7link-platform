package app

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeHTTPStopper struct {
	shutdownErr error
	closeErr    error
	shutdown    bool
	closed      bool
}

func (s *fakeHTTPStopper) Shutdown(context.Context) error {
	s.shutdown = true
	return s.shutdownErr
}
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

func TestStopHTTPServerImmediatelySkipsGracefulShutdown(t *testing.T) {
	server := &fakeHTTPStopper{}
	require.NoError(t, stopHTTPServerImmediately(server))
	assert.False(t, server.shutdown)
	assert.True(t, server.closed)

	want := errors.New("close failed")
	err := stopHTTPServerImmediately(&fakeHTTPStopper{closeErr: want})
	assert.ErrorIs(t, err, want)
}

type fakeGRPCStopper struct {
	stop           chan struct{}
	stopOnce       sync.Once
	graceful       bool
	gracefulCalled bool
	forced         bool
}

type shutdownLifecycleModule struct {
	quiesced bool
	stopped  bool
}

func (*shutdownLifecycleModule) ModuleName() string { return "shutdown-test" }
func (module *shutdownLifecycleModule) Start(context.Context) error {
	return nil
}
func (module *shutdownLifecycleModule) Quiesce(context.Context) error {
	module.quiesced = true
	return nil
}
func (module *shutdownLifecycleModule) Stop(context.Context) error {
	module.stopped = true
	return nil
}

func newFakeGRPCStopper() *fakeGRPCStopper {
	return &fakeGRPCStopper{stop: make(chan struct{})}
}

func (s *fakeGRPCStopper) GracefulStop() {
	s.gracefulCalled = true
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

func TestStopGRPCServerImmediatelySkipsGracefulStop(t *testing.T) {
	server := newFakeGRPCStopper()
	stopGRPCServerImmediately(server)
	assert.False(t, server.gracefulCalled)
	assert.True(t, server.forced)
}

func TestWaitForDrainHonorsDelayAndContext(t *testing.T) {
	started := time.Now()
	require.NoError(t, waitForDrain(t.Context(), 10*time.Millisecond))
	assert.GreaterOrEqual(t, time.Since(started), 10*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := waitForDrain(ctx, time.Minute)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NoError(t, waitForDrain(ctx, 0))
}

func TestShutdownSkipsDrainBeforeServersStart(t *testing.T) {
	app := &App{config: AppConfig{
		Shutdown: ShutdownConfig{Graceful: true, Timeout: time.Nanosecond, DrainDelay: time.Minute},
	}}
	assert.NoError(t, app.shutdown())
}

func TestImmediateShutdownSkipsQuiesceButCleansUpModules(t *testing.T) {
	lifecycle := &shutdownLifecycleModule{}
	app := &App{
		config:  AppConfig{Shutdown: ShutdownConfig{Graceful: false, Timeout: time.Second}},
		modules: wiredRegistry(t, lifecycle),
	}
	require.NoError(t, app.shutdown())
	assert.False(t, lifecycle.quiesced)
	assert.True(t, lifecycle.stopped)
}

func TestGracefulShutdownQuiescesServersModulesAndCancelsRootContext(t *testing.T) {
	lifecycle := &shutdownLifecycleModule{}
	rootCtx, cancel := context.WithCancel(context.Background())
	app := &App{
		config:     AppConfig{Shutdown: ShutdownConfig{Graceful: true, Timeout: time.Second}},
		modules:    wiredRegistry(t, lifecycle),
		ctx:        rootCtx,
		cancel:     cancel,
		httpServer: &http.Server{},
		grpcServer: grpc.NewServer(),
	}
	require.NoError(t, app.shutdown())
	assert.True(t, lifecycle.quiesced)
	assert.True(t, lifecycle.stopped)
	assert.ErrorIs(t, rootCtx.Err(), context.Canceled)
}
