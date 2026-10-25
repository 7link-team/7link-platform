package fleet

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
)

func TestPreviewBrokerRejectsUnacknowledgedStreamsAndReleasesQuota(t *testing.T) {
	for _, mode := range []string{"rejected", "canceled", "disconnected", "stale", "quota", "identifier", "authority", "invalid-port"} {
		t.Run(mode, func(t *testing.T) {
			b, c, existing := previewBrokerFixture(t)
			defer func() { _ = existing.Close() }()
			grant := existing.grant
			acquired, released := 0, 0
			b.acquire = func(context.Context, guid.ID, guid.ID) (func(), error) {
				if mode == "quota" {
					return nil, ErrSessionActive
				}
				acquired++
				return func() { released++ }, nil
			}
			b.next = func() (int64, error) {
				if mode == "identifier" {
					return 0, ErrUnavailable
				}
				return 43, nil
			}
			if mode == "stale" {
				grant.ConnEpoch++
			}
			if mode == "invalid-port" {
				grant.Port = -1
			}
			if mode == "authority" {
				b.validate = func(context.Context, PreviewGrant) error { return ErrMachineForbidden }
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := b.create(ctx, grant, "open"); result <- err }()
			if mode == "rejected" || mode == "canceled" || mode == "disconnected" {
				select {
				case frame := <-c.frames:
					require.Equal(t, "open", frame.Kind)
					switch mode {
					case "rejected":
						require.NoError(t, b.receive(ctx, c, previewFrame{Kind: "rejected", ID: frame.ID}))
					case "canceled":
						cancel()
					case "disconnected":
						c.once.Do(func() { close(c.done) })
					}
				case <-time.After(time.Second):
					t.Fatal("open frame missing")
				}
			}
			select {
			case err := <-result:
				require.Error(t, err)
			case <-time.After(time.Second):
				t.Fatal("failed stream retained")
			}
			require.Equal(t, acquired, released)
			b.mu.Lock()
			require.Len(t, c.streams, 1)
			b.mu.Unlock()
		})
	}
}

func TestPreviewBrokerProbeAndProtocolBoundsPreserveOtherStreams(t *testing.T) {
	b, c, s := previewBrokerFixture(t)
	b.next = func() (int64, error) { return 43, nil }
	grant := s.grant
	grant.Port = 0
	ports := make(chan []int, 1)
	failed := make(chan error, 1)
	go func() {
		out, err := b.Ports(t.Context(), grant)
		if err != nil {
			failed <- err
		} else {
			ports <- out
		}
	}()
	frame := <-c.frames
	require.Equal(t, "probe", frame.Kind)
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "opened", ID: frame.ID, Ports: []int{0}}), ErrInvalidArgument)
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "opened", ID: frame.ID, Ports: make([]int, 65536)}), ErrInvalidArgument)
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "opened", ID: frame.ID, Ports: []int{3000, 4000}}))
	select {
	case out := <-ports:
		require.Equal(t, []int{3000, 4000}, out)
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("probe not completed")
	}
	require.Equal(t, "reset", (<-c.frames).Kind)
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "pong"}))
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: "absent"}), ErrMachineNotFound)
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: make([]byte, 16385)}), ErrInvalidArgument)
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id}), ErrInvalidArgument)
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "window", ID: s.id}), ErrVersionConflict)
	require.Error(t, b.receive(t.Context(), c, previewFrame{Kind: "unknown", ID: s.id}))
	for i := 0; i < 16; i++ {
		require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: []byte{1}}))
	}
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: []byte{2}}), ErrSessionActive)
	require.Len(t, c.pending, 16)
	require.NoError(t, b.ResetSession(t.Context(), s.grant.SessionID, "REVOKED"))
	require.Empty(t, c.pending)
	require.Empty(t, c.streams)
	require.True(t, b.Available(1, 2))
	c.once.Do(func() { close(c.done) })
	require.False(t, b.Available(1, 2))
	require.False(t, b.Available(2, 3))
}

func TestPreviewBrokerCurrentAuthorityFailureClosesStreamAndReleasesResources(t *testing.T) {
	b, c, s := previewBrokerFixture(t)
	require.NoError(t, s.CloseWrite())
	require.Equal(t, "fin", (<-c.frames).Kind)
	require.NoError(t, s.CloseWrite())
	injected := errors.New("current preview grant denied")
	b.validate = func(context.Context, PreviewGrant) error { return injected }
	require.ErrorIs(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: []byte("private")}), injected)
	require.Empty(t, c.streams)
	require.Empty(t, c.pending)
	require.Equal(t, "reset", (<-c.frames).Kind)
}

func previewBrokerFixture(t *testing.T) (*previewBroker, *previewConnection, *previewStream) {
	t.Helper()
	b := newPreviewBroker(func() (int64, error) { return 42, nil }, func(context.Context, PreviewGrant) error { return nil })
	c := &previewConnection{tenant: 1, machine: 2, session: 3, epoch: 4, frames: make(chan previewFrame, 256), done: make(chan struct{}), streams: make(map[string]*previewStream), closed: make(map[string]bool), pending: make(chan struct{}, 256)}
	b.connections[[2]guid.ID{1, 2}] = c
	result := make(chan *previewStream, 1)
	failed := make(chan error, 1)
	go func() {
		s, e := b.create(t.Context(), PreviewGrant{TenantID: 1, MachineID: 2, RunnerSessionID: 3, ConnEpoch: 4, ExecutionID: 5, Port: 8080}, "open")
		if e != nil {
			failed <- e
			return
		}
		result <- s
	}()
	select {
	case f := <-c.frames:
		require.Equal(t, "open", f.Kind)
		require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "opened", ID: f.ID}))
	case <-time.After(time.Second):
		t.Fatal("open not emitted")
	}
	select {
	case s := <-result:
		t.Cleanup(func() { _ = s.Close() })
		return b, c, s
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("open not acknowledged")
	}
	return nil, nil, nil
}

func TestPreviewBrokerBinaryFlowControlFinAndCleanup(t *testing.T) {
	b, c, s := previewBrokerFixture(t)
	payload := []byte{0, 255, 1, 0, 2}
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: payload}))
	var first [2]byte
	n, err := s.Read(first[:])
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, payload[:2], first[:])
	require.Len(t, c.pending, 1)
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "fin", ID: s.id}))
	remaining, err := io.ReadAll(s)
	require.NoError(t, err)
	require.Equal(t, payload[2:], remaining)
	require.Empty(t, c.pending)
	require.Equal(t, "window", (<-c.frames).Kind)
	require.Error(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: []byte{9}}))
	require.NoError(t, s.Close())
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: []byte{9}}), "late known closed data must not kill unrelated streams")
}

func TestPreviewBrokerCloseReleasesPartiallyReadBuffers(t *testing.T) {
	b, c, s := previewBrokerFixture(t)
	for i := 0; i < 16; i++ {
		require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: []byte{1, 2, 3}}))
	}
	var one [1]byte
	_, err := s.Read(one[:])
	require.NoError(t, err)
	require.Len(t, c.pending, 16)
	require.NoError(t, s.Close())
	require.Empty(t, c.pending)
	require.Empty(t, c.streams)
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "opened", ID: s.id}))
}

func TestPreviewBrokerWriteWaitsForWindowAndCancels(t *testing.T) {
	b, c, s := previewBrokerFixture(t)
	for i := 0; i < 16; i++ {
		n, err := s.Write([]byte{byte(i)})
		require.NoError(t, err)
		require.Equal(t, 1, n)
		require.Equal(t, "data", (<-c.frames).Kind)
	}
	result := make(chan error, 1)
	go func() { _, err := s.Write([]byte{17}); result <- err }()
	select {
	case <-result:
		t.Fatal("write exceeded credit window")
	case <-time.After(20 * time.Millisecond):
	}
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "window", ID: s.id}))
	require.NoError(t, <-result)
	require.Equal(t, "data", (<-c.frames).Kind)
	go func() { _, err := s.Write([]byte{18}); result <- err }()
	require.NoError(t, s.Close())
	require.Error(t, <-result)
}

func TestPreviewBrokerHalfClosePreservesResponse(t *testing.T) {
	b, c, s := previewBrokerFixture(t)
	_, err := s.Write([]byte("request"))
	require.NoError(t, err)
	require.Equal(t, "data", (<-c.frames).Kind)
	require.NoError(t, s.CloseWrite())
	require.Equal(t, "fin", (<-c.frames).Kind)
	_, err = s.Write([]byte("late"))
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "data", ID: s.id, Data: []byte("response")}))
	require.NoError(t, b.receive(t.Context(), c, previewFrame{Kind: "fin", ID: s.id}))
	data, err := io.ReadAll(s)
	require.NoError(t, err)
	require.Equal(t, "response", string(data))
}
