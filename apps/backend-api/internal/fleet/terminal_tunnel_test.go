package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTerminalRunnerProtocolFailuresNeverForwardOrRecordUnorderedBytes(t *testing.T) {
	m, db, owner, row, _ := terminalFixture(t)
	bindTerminalRecordingFixture(t, m, db, owner)
	_, err := m.SetTerminalAccess(owner, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	b := newTerminalBroker()
	m.terminalBroker = b
	c, err := b.connect(7, 1, row.RunnerSessionID, row.ConnEpoch)
	require.NoError(t, err)
	runner := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 101, SubjectType: authn.SubjectTypeServiceAccount})
	result := make(chan error, 1)
	go func() { _, e := b.open(owner, terminalView(row), 80, 24); result <- e }()
	select {
	case frame := <-c.frames:
		require.Equal(t, "open", frame.Kind)
	case <-time.After(time.Second):
		t.Fatal("missing open")
	}
	require.NoError(t, m.terminalTunnelFrame(runner, c, terminalFrame{Kind: "opened", SessionID: row.ID.String(), Seq: 1}))
	require.NoError(t, <-result)
	t.Cleanup(func() { _ = b.close(terminalView(row), "TEST_COMPLETE"); b.disconnect(c) })
	for _, frame := range []terminalFrame{
		{Kind: "opened", SessionID: row.ID.String(), Seq: 1},
		{Kind: "output", SessionID: row.ID.String(), Seq: 3, Data: []byte("out of order")},
		{Kind: "output", SessionID: row.ID.String(), Seq: 2},
		{Kind: "output", SessionID: row.ID.String(), Seq: 2, Data: make([]byte, terminalFrameLimit+1)},
		{Kind: "closed", SessionID: row.ID.String(), Seq: 3},
		{Kind: "unknown", SessionID: row.ID.String(), Seq: 2},
		{Kind: "output", SessionID: "invalid", Seq: 2},
		{Kind: "output", SessionID: "999", Seq: 2},
	} {
		require.Error(t, m.terminalTunnelFrame(runner, c, frame))
	}
	other := &terminalConnection{}
	require.ErrorIs(t, m.terminalTunnelFrame(runner, other, terminalFrame{Kind: "output", SessionID: row.ID.String(), Seq: 2, Data: []byte("wrong transport")}), ErrMachineForbidden)
	for _, input := range []struct {
		seq  int64
		data []byte
	}{{0, []byte("x")}, {1, nil}, {1, make([]byte, terminalFrameLimit+1)}} {
		require.ErrorIs(t, m.terminalTunnelInput(owner, 7, row.ID, input.seq, input.data), ErrInvalidArgument)
	}
	require.Empty(t, c.frames)
	injected := errors.New("recording store unavailable")
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("terminal_input_recording_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == m.names.TerminalSessions.String() {
			tx.AddError(injected)
		}
	}))
	require.ErrorIs(t, m.terminalTunnelInput(owner, 7, row.ID, 1, []byte("must not execute")), injected)
	require.NoError(t, db.Callback().Query().Remove("terminal_input_recording_failure"))
	require.Empty(t, c.frames)
	s, err := b.lookup(row.ID)
	require.NoError(t, err)
	require.Zero(t, s.inputSeq)
	require.EqualValues(t, 1, s.outputSeq)
}

func TestTerminalSweepClosesInvalidatedCurrentAuthorityEvenIfStorageFails(t *testing.T) {
	for _, storageFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "closed", true: "storage-failed"}[storageFailure], func(t *testing.T) {
			m, db, owner, row, _ := terminalFixture(t)
			bindTerminalRecordingFixture(t, m, db, owner)
			_, err := m.SetTerminalAccess(owner, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, err)
			b := newTerminalBroker()
			m.terminalBroker = b
			m.terminal.transport.Close = func(_ context.Context, session TerminalSession, reason string) error { return b.close(session, reason) }
			c, err := b.connect(7, 1, row.RunnerSessionID, row.ConnEpoch)
			require.NoError(t, err)
			result := make(chan error, 1)
			go func() { _, e := b.open(owner, terminalView(row), 80, 24); result <- e }()
			<-c.frames
			s, err := b.lookup(row.ID)
			require.NoError(t, err)
			s.opened <- terminalFrame{Kind: "opened"}
			require.NoError(t, <-result)
			require.NoError(t, m.terminalTunnelSweep(c))
			m.terminal.execution = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (TerminalExecution, error) {
				return TerminalExecution{}, ErrMachineForbidden
			}
			if storageFailure {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("terminal_close_storage_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.TerminalSessions.String() {
						tx.AddError(errors.New("terminal update unavailable"))
					}
				}))
			}
			err = m.terminalTunnelSweep(c)
			if storageFailure {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, db.Callback().Update().Remove("terminal_close_storage_failure"))
			_, err = b.lookup(row.ID)
			require.ErrorIs(t, err, ErrMachineNotFound)
			select {
			case <-s.done:
			default:
				t.Fatal("authority loss retained native stream")
			}
			b.disconnect(c)
		})
	}
}

func TestTerminalOutputConnectionWindowReleasesAbsentAndDisconnectedViewers(t *testing.T) {
	b := newTerminalBroker()
	c, err := b.connect(7, 1, 2, 3)
	require.NoError(t, err)
	var streams []*terminalStream
	for i := 0; i < 17; i++ {
		s := &terminalStream{session: TerminalSession{ID: guid.ID(i + 1)}, connection: c, output: make(chan terminalFrame, 16), done: make(chan struct{})}
		b.streams[s.session.ID] = s
		streams = append(streams, s)
	}
	f := terminalFrame{Kind: "output", Data: make([]byte, terminalFrameLimit)}
	for _, s := range streams[:16] {
		for i := 0; i < 16; i++ {
			require.NoError(t, s.queueOutput(f))
		}
	}
	require.Len(t, c.pending, 256)
	require.Error(t, streams[16].queueOutput(f))
	// No viewer can consume a closed stream: discard its already-recorded queue.
	require.NoError(t, b.close(streams[0].session, "NO_VIEWER"))
	require.Len(t, c.pending, 240)
	require.NoError(t, streams[16].queueOutput(f))
	require.Len(t, c.pending, 241)
	// A live viewer retains final output after close and releases each slot when
	// written; disconnect releases the rest, including a partially sent frame.
	s := streams[1]
	s.bufferMu.Lock()
	s.watching = true
	s.bufferMu.Unlock()
	require.NoError(t, b.close(s.session, "PROCESS_EXIT"))
	require.Len(t, s.output, 16)
	require.Len(t, c.pending, 241)
	got := <-s.output
	require.Equal(t, f.Data, got.Data)
	s.releaseOutput()
	require.Len(t, c.pending, 240)
	s.closeOutput(true)
	require.Empty(t, s.output)
	require.Len(t, c.pending, 225)
	require.Error(t, s.queueOutput(f))
	for _, other := range streams {
		other.closeOutput(true)
	}
	require.Empty(t, c.pending)
}

func TestTerminalBrokerIsFencedBoundedAndNeverReplays(t *testing.T) {
	b := newTerminalBroker()
	require.False(t, b.available(7, 1))
	c, err := b.connect(7, 1, 2, 3)
	require.NoError(t, err)
	require.True(t, b.available(7, 1))
	require.False(t, b.available(8, 1))
	_, err = b.connect(7, 1, 2, 3)
	require.ErrorIs(t, err, ErrSessionActive)
	session := TerminalSession{ID: 10, TenantID: 7, MachineID: 1, ExecutionID: 20, RunnerSessionID: 2, ConnEpoch: 4, Workspace: "pinned"}
	_, err = b.open(t.Context(), session, 80, 24)
	require.ErrorIs(t, err, ErrSessionStale)
	session.ConnEpoch = 3
	result := make(chan error, 1)
	go func() { _, e := b.open(t.Context(), session, 80, 24); result <- e }()
	select {
	case f := <-c.frames:
		require.Equal(t, "open", f.Kind)
		require.Equal(t, "pinned", f.Workspace)
	case <-time.After(time.Second):
		t.Fatal("no open")
	}
	s, err := b.lookup(10)
	require.NoError(t, err)
	s.opened <- terminalFrame{Kind: "opened", Sandboxed: false}
	require.NoError(t, <-result)
	_, err = b.open(t.Context(), TerminalSession{ID: 11, TenantID: 7, MachineID: 1, ExecutionID: 20, RunnerSessionID: 2, ConnEpoch: 3}, 80, 24)
	require.ErrorIs(t, err, ErrSessionActive)
	for i := 0; i < cap(c.frames); i++ {
		require.NoError(t, sendTerminalFrame(t.Context(), c, terminalFrame{Kind: "ping"}))
	}
	require.Error(t, sendTerminalFrame(t.Context(), c, terminalFrame{Kind: "ping"}))
	active := b.disconnect(c)
	require.Len(t, active, 1)
	require.False(t, b.available(7, 1))
	require.NoError(t, b.close(session, "DISCONNECTED"))
	select {
	case <-s.done:
	default:
		t.Fatal("stream not reset")
	}
	c2, err := b.connect(7, 1, 2, 3)
	require.NoError(t, err)
	require.Empty(t, c2.frames, "reconnection cannot replay old frames")
	_, err = b.lookup(10)
	require.Error(t, err)
	b.disconnect(c2)
}

func TestTerminalBrokerCancelledOpenAndMachineLimit(t *testing.T) {
	b := newTerminalBroker()
	c, err := b.connect(7, 1, 2, 3)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = b.open(ctx, TerminalSession{ID: 1, TenantID: 7, MachineID: 1, ExecutionID: 1, RunnerSessionID: 2, ConnEpoch: 3}, 80, 24)
	require.Error(t, err)
	for i := 1; i <= 16; i++ {
		id := guid.ID(i)
		b.streams[id] = &terminalStream{session: TerminalSession{ID: id, TenantID: 7, ExecutionID: id}, connection: c, done: make(chan struct{})}
	}
	_, err = b.open(t.Context(), TerminalSession{ID: 100, TenantID: 7, MachineID: 1, ExecutionID: 100, RunnerSessionID: 2, ConnEpoch: 3, TunnelMaxStreams: 16}, 80, 24)
	require.ErrorIs(t, err, ErrSessionActive)
	for _, s := range b.disconnect(c) {
		require.NoError(t, b.close(s.session, "CLEANUP"))
	}
}

func TestTerminalBrokerRevokeAcknowledgementsAreIsolated(t *testing.T) {
	b := newTerminalBroker()
	c, err := b.connect(7, 1, 2, 3)
	require.NoError(t, err)
	for _, id := range []guid.ID{10, 11} {
		b.streams[id] = &terminalStream{session: TerminalSession{ID: id, TenantID: 7, MachineID: 1}, connection: c, done: make(chan struct{})}
	}
	require.NoError(t, b.close(TerminalSession{ID: 10}, "USER"))
	require.True(t, b.discardClosedFrame(c, terminalFrame{Kind: "closed", SessionID: "10", Seq: 3}))
	require.True(t, b.discardClosedFrame(c, terminalFrame{Kind: "opened", SessionID: "10", Seq: 1}))
	require.True(t, b.discardClosedFrame(c, terminalFrame{Kind: "rejected", SessionID: "10", Seq: 1}))
	require.True(t, b.discardClosedFrame(c, terminalFrame{Kind: "output", SessionID: "10", Seq: 2, Data: []byte("final")}))
	require.False(t, b.discardClosedFrame(c, terminalFrame{Kind: "input", SessionID: "10"}))
	require.False(t, b.discardClosedFrame(c, terminalFrame{Kind: "closed", SessionID: "99"}))
	_, err = b.lookup(11)
	require.NoError(t, err)
	select {
	case <-c.done:
		t.Fatal("revoking one terminal killed another")
	default:
	}
	for len(c.frames) < cap(c.frames) {
		c.frames <- terminalFrame{Kind: "ping"}
	}
	require.NoError(t, b.close(TerminalSession{ID: 11}, "USER"))
	select {
	case <-c.done:
	default:
		t.Fatal("dropped revoke did not disconnect native shell")
	}
	b.disconnect(c)
}

func TestTerminalMachineDisableClosesAnotherHumansShell(t *testing.T) {
	m, db, owner, row, _ := terminalFixture(t)
	_, err := m.SetTerminalAccess(owner, 7, 1, 1, true, "browser", "fresh")
	require.NoError(t, err)
	b := newTerminalBroker()
	m.terminalBroker = b
	c, err := b.connect(7, 1, row.RunnerSessionID, row.ConnEpoch)
	require.NoError(t, err)
	s := &terminalStream{session: terminalView(row), connection: c, done: make(chan struct{})}
	b.streams[row.ID] = s
	m.terminal.transport.Close = func(_ context.Context, v TerminalSession, reason string) error { return b.close(v, reason) }
	manager := authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	_, err = m.SetTerminalAccess(manager, 7, 1, 2, false, "", "")
	require.NoError(t, err)
	stored, err := query.BindTerminalSessions(db, m.names).WithContext(owner).First()
	require.NoError(t, err)
	require.Equal(t, "CLOSED", stored.State)
	require.Equal(t, "ACCESS_DISABLED", stored.CloseReason)
	require.Equal(t, guid.ID(11), stored.OpenedBy)
	select {
	case <-s.done:
	default:
		t.Fatal("disable left another Human's shell running")
	}
}

func TestTerminalPendingNativeOpenFailureAlwaysReleasesMachineQuota(t *testing.T) {
	for _, mode := range []string{"rejected", "canceled", "disconnected", "saturated"} {
		t.Run(mode, func(t *testing.T) {
			m, _, ctx, row, _ := terminalFixture(t)
			_, e := m.SetTerminalAccess(ctx, 7, 1, 1, true, "browser", "fresh")
			require.NoError(t, e)
			b := newTerminalBroker()
			m.terminalBroker = b
			c, e := b.connect(7, 1, row.RunnerSessionID, row.ConnEpoch)
			require.NoError(t, e)
			defer b.disconnect(c)
			waiting, cancel := context.WithCancel(ctx)
			defer cancel()
			if mode == "saturated" {
				for range cap(c.frames) {
					require.NoError(t, sendTerminalFrame(ctx, c, terminalFrame{Kind: "ping"}))
				}
			}
			result := make(chan error, 1)
			go func() { _, e := m.openTerminalStream(waiting, terminalView(row), 80, 24); result <- e }()
			if mode != "saturated" {
				select {
				case f := <-c.frames:
					require.Equal(t, "open", f.Kind)
				case <-time.After(time.Second):
					t.Fatal("native open missing")
				}
				stream, e := b.lookup(row.ID)
				require.NoError(t, e)
				switch mode {
				case "rejected":
					stream.opened <- terminalFrame{Kind: "rejected"}
				case "canceled":
					cancel()
				case "disconnected":
					c.once.Do(func() { close(c.done) })
				}
			}
			select {
			case e := <-result:
				require.Error(t, e)
			case <-time.After(time.Second):
				t.Fatal("pending native open retained quota")
			}
			_, e = b.lookup(row.ID)
			require.ErrorIs(t, e, ErrMachineNotFound)
			require.Zero(t, m.tunnelBudget(7, 1).streams)
		})
	}
}
