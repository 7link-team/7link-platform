package fleet

import (
	"7link.com/platform/internal/fleet/model"
	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAgentWorkerEmptySnapshotStorageFailuresPreserveProcessState(t *testing.T) {
	for _, operation := range []string{"query", "update"} {
		exercise := func(t *testing.T, failAt int) int {
			f := agentRebindHarness(t)
			before := f.agent(t)
			count := 0
			injected := errors.New("worker snapshot storage unavailable")
			hook := func(tx *gorm.DB) {
				if tx.Statement.Table != f.m.names.Agents.String() && tx.Statement.Table != f.m.names.Machines.String() && tx.Statement.Table != f.m.names.Sessions.String() {
					return
				}
				count++
				if count == failAt {
					tx.AddError(injected)
				}
			}
			if operation == "query" {
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("worker_storage_failure", hook))
			} else {
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("worker_storage_failure", hook))
			}
			err := f.db.Transaction(func(tx *gorm.DB) error {
				return f.m.ReportAgentPresenceOn(f.oldRunner, tx, 7, 1, AgentPresenceSnapshot{SessionID: f.oldSession.ID, ConnEpoch: f.oldSession.ConnEpoch})
			})
			require.NoError(t, f.db.Callback().Query().Remove("worker_storage_failure"))
			require.NoError(t, f.db.Callback().Update().Remove("worker_storage_failure"))
			if failAt == 0 {
				require.NoError(t, err)
				return count
			}
			require.ErrorIs(t, err, injected)
			require.Equal(t, before, f.agent(t))
			return count
		}
		count := exercise(t, 0)
		require.Positive(t, count)
		for point := 1; point <= count; point++ {
			t.Run(fmt.Sprintf("%s/%d", operation, point), func(t *testing.T) { exercise(t, point) })
		}
	}
}

func TestAgentWorkerSnapshotsRequireCurrentSessionExactBindingAndActualPID(t *testing.T) {
	m, db, human, runner, session, p, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.RegisterAgentOn(human, tx, 7, in); return err }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		profiles, err := m.AgentWorkersOn(runner, tx, 7, 1, session.ID, session.ConnEpoch)
		require.NoError(t, err)
		require.Equal(t, []AgentWorker{{AgentPrincipalID: 202, Runtime: "CLAUDE_CODE", Model: "sonnet", ConfigRevision: 1, LaunchVersion: 1}}, profiles)
		return nil
	}))
	apply := func(in AgentPresenceSnapshot) error {
		return db.Transaction(func(tx *gorm.DB) error { return m.ReportAgentPresenceOn(runner, tx, 7, 1, in) })
	}
	read := func() string {
		q := query.BindAgents(db, m.names)
		row, err := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).First()
		require.NoError(t, err)
		return row.ProcessStatus
	}
	snapshot := AgentPresenceSnapshot{SessionID: session.ID, ConnEpoch: session.ConnEpoch, Agents: []AgentPresence{{AgentPrincipalID: 202, PID: 1234, State: "RUNNING"}}}
	require.NoError(t, apply(snapshot))
	require.Equal(t, "RUNNING", read())
	for _, fault := range []string{"zero_pid", "unknown_agent", "stale_session", "duplicate", "outbox"} {
		bad := snapshot
		bad.Agents = append([]AgentPresence{}, snapshot.Agents...)
		switch fault {
		case "zero_pid":
			bad.Agents[0].PID = 0
		case "unknown_agent":
			bad.Agents[0].AgentPrincipalID = guid.ID(999)
		case "stale_session":
			bad.ConnEpoch++
		case "duplicate":
			bad.Agents = append(bad.Agents, bad.Agents[0])
		case "outbox":
			p.fail = true
			bad.Agents[0].State = "EXITED"
		}
		require.Error(t, apply(bad))
		require.Equal(t, "RUNNING", read())
		p.fail = false
	}
	snapshot.Agents = []AgentPresence{}
	require.NoError(t, apply(snapshot))
	require.Equal(t, "EXITED", read())
}

func TestAgentWorkerCrashWindowBackoffExhaustionAndHumanStart(t *testing.T) {
	m, db, human, runner, session, _, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
	m.agentRebindQuiet = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil }
	observed := []string{}
	m.processObserver = func(_ context.Context, _ *gorm.DB, tr AgentProcessTransition) error {
		observed = append(observed, tr.Kind)
		return nil
	}
	report := func(state string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			pid := 0
			if state == "RUNNING" {
				pid = 88
			}
			return m.ReportAgentPresenceOn(runner, tx, 7, 1, AgentPresenceSnapshot{SessionID: session.ID, ConnEpoch: session.ConnEpoch, Agents: []AgentPresence{{AgentPrincipalID: 202, PID: pid, State: state}}})
		})
	}
	read := func() *model.Agent {
		q := query.BindAgents(db, m.names)
		a, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).First()
		require.NoError(t, e)
		return a
	}
	for crash := 1; crash <= 4; crash++ {
		require.NoError(t, report("RUNNING"))
		require.NoError(t, report("EXITED"))
		a := read()
		require.Equal(t, "CRASHED", a.State)
		require.Equal(t, min(crash, 3), a.RestartCountInWindow)
		require.Equal(t, crash == 4, a.RestartExhausted)
		require.False(t, agentWorkerDesired(a, *a.ProcessStatusAt))
		delay := 10 * time.Second
		if crash == 2 {
			delay = time.Minute
		}
		if crash >= 3 {
			delay = 5 * time.Minute
		}
		require.Equal(t, crash < 4, agentWorkerDesired(a, a.ProcessStatusAt.Add(delay)))
		require.NoError(t, report("EXITED"))
		require.Equal(t, a.ProcessStatusAt, read().ProcessStatusAt, "repeated EXITED snapshots must not move the backoff deadline")
	}
	require.Contains(t, observed, "RESTART_EXHAUSTED")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.agentRunStateOn(human, tx, 7, 202, "ACTIVE")
		require.NoError(t, e)
		require.Equal(t, "STARTING", v.ProcessStatus)
		return nil
	}))
	a := read()
	require.Equal(t, "ACTIVE", a.State)
	require.False(t, a.RestartExhausted)
	require.Zero(t, a.RestartCountInWindow)
	require.NoError(t, report("RUNNING"))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.agentRunStateOn(human, tx, 7, 202, "STOPPED")
		require.NoError(t, e)
		require.Equal(t, "STOPPING", v.ProcessStatus)
		return nil
	}))
	// A final in-flight live snapshot does not kill the Runner or invent a crash.
	require.NoError(t, report("RUNNING"))
	require.Equal(t, "STOPPING", read().ProcessStatus)
	require.NoError(t, report("EXITED"))
	require.Equal(t, "STOPPED", read().State)
	require.Equal(t, "EXITED", read().ProcessStatus)
}

func TestAgentWorkerHTTPReportsOnlyCurrentActualServiceAccounts(t *testing.T) {
	m, db, human, _, session, _, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
	router := fleetBusinessRouter(t, m)
	headers := map[string]string{"Authorization": "Bearer runner"}
	path := fmt.Sprintf("/machines/1/agent-workers?session_id=%s&conn_epoch=%d", session.ID.String(), session.ConnEpoch)
	list := fleetHTTPRequest(router, "GET", path, "", headers)
	require.Equal(t, 200, list.Code, list.Body.String())
	require.Contains(t, list.Body.String(), "sonnet")
	body := fmt.Sprintf(`{"session_id":%q,"conn_epoch":%d,"agents":[{"agent_principal_id":"202","pid":881,"state":"RUNNING"}]}`, session.ID.String(), session.ConnEpoch)
	present := fleetHTTPRequest(router, "POST", "/machines/1/agent-presences", body, headers)
	require.Equal(t, 200, present.Code, present.Body.String())
	m.accounts = func(_ context.Context, _ *gorm.DB, _ guid.ID, p guid.ID, _ time.Time) (bool, error) {
		return p == 101, nil
	}
	denied := fleetHTTPRequest(router, "GET", path, "", headers)
	require.Equal(t, 403, denied.Code, denied.Body.String())
	denied = fleetHTTPRequest(router, "POST", "/machines/1/agent-presences", body, headers)
	require.Equal(t, 403, denied.Code, denied.Body.String())
}

func TestAgentWorkerSyncDefersProfilesWhosePrincipalWasNotPrelocked(t *testing.T) {
	m, db, human, runner, session, _, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
	m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return true, nil }
	inserted := false
	m.principals.Lock = func(_ context.Context, tx *gorm.DB, id guid.ID) error {
		if id == 202 && !inserted {
			inserted = true
			q := query.BindAgents(tx, m.names)
			row, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).First()
			if e != nil {
				return e
			}
			row.ID = 999
			row.PrincipalID = 303
			row.Name = "Concurrent profile"
			row.Handle = "concurrent-profile"
			return q.WithContext(t.Context()).Create(row)
		}
		return nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		profiles, e := m.AgentWorkersOn(runner, tx, 7, 1, session.ID, session.ConnEpoch)
		require.NoError(t, e)
		require.Equal(t, []AgentWorker{{AgentPrincipalID: 202, Runtime: "CLAUDE_CODE", Model: "sonnet", ConfigRevision: 1, LaunchVersion: 1}}, profiles)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		profiles, e := m.AgentWorkersOn(runner, tx, 7, 1, session.ID, session.ConnEpoch)
		require.NoError(t, e)
		require.Len(t, profiles, 2)
		return nil
	}))
}
func TestAgentWorkerSnapshotErrorsNeverChangePresence(t *testing.T) {
	for _, fault := range []string{"account_error", "account_missing", "principal_lock", "principal_missing", "audit", "publisher_missing", "too_many", "invalid_state", "negative_pid", "zero_id"} {
		t.Run(fault, func(t *testing.T) {
			m, db, human, runner, session, _, in := agentRegistrationHarness(t)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
			snapshot := AgentPresenceSnapshot{SessionID: session.ID, ConnEpoch: session.ConnEpoch, Agents: []AgentPresence{{AgentPrincipalID: 202, PID: 100, State: "RUNNING"}}}
			switch fault {
			case "account_error":
				m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) {
					return false, ErrUnavailable
				}
			case "account_missing":
				m.accounts = nil
			case "principal_missing":
				m.principals.Lock = nil
			case "principal_lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "audit":
				m.audit = nil
			case "publisher_missing":
				m.events = nil
			case "too_many":
				snapshot.Agents = make([]AgentPresence, 101)
			case "invalid_state":
				snapshot.Agents[0].State = "ACTIVE"
			case "negative_pid":
				snapshot.Agents[0].PID = -1
			case "zero_id":
				snapshot.Agents[0].AgentPrincipalID = 0
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.ReportAgentPresenceOn(runner, tx, 7, 1, snapshot) }))
			q := query.BindAgents(db, m.names)
			row, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).First()
			require.NoError(t, e)
			require.Equal(t, "NONE", row.ProcessStatus)
		})
	}
}

func TestAgentWorkerActualRevisionAndStaleLaunchFencing(t *testing.T) {
	m, db, human, runner, session, _, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
	q := query.BindAgents(db, m.names)
	var launch int
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		profiles, e := m.AgentWorkersOn(runner, tx, 7, 1, session.ID, session.ConnEpoch)
		require.NoError(t, e)
		launch = profiles[0].LaunchVersion
		return nil
	}))
	report := func(revision *int, version int, state string, attempted bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			pid := 0
			if state == "RUNNING" {
				pid = 88
			}
			return m.ReportAgentPresenceOn(runner, tx, 7, 1, AgentPresenceSnapshot{SessionID: session.ID, ConnEpoch: session.ConnEpoch, Agents: []AgentPresence{{AgentPrincipalID: 202, PID: pid, State: state, ConfigRevision: revision, LaunchVersion: version, Attempted: attempted}}})
		})
	}
	read := func() *model.Agent {
		row, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).First()
		require.NoError(t, e)
		return row
	}
	one := 1
	require.NoError(t, report(&one, launch, "RUNNING", false))
	require.Equal(t, 1, *read().RunningConfigRevision)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		name := "Updated resident profile"
		updated, err := m.updateAgentProfileOn(human, tx, 7, 202, AgentProfilePatch{Version: launch, Name: &name})
		require.NoError(t, err)
		require.Equal(t, 2, updated.ConfigRevision)
		return nil
	}))
	require.Equal(t, "RUNNING", read().ProcessStatus, "profile PATCH must not request a restart")
	require.NoError(t, report(&one, launch, "RUNNING", false))
	require.Equal(t, 1, *read().RunningConfigRevision, "old process must not claim newly edited config")
	require.NoError(t, report(&one, launch, "EXITED", true))
	require.Equal(t, "CRASHED", read().State, "resident old child exit must enter crash backoff")
	require.Equal(t, 1, read().RestartCountInWindow)
	require.NoError(t, report(&one, launch, "EXITED", true))
	require.Equal(t, 1, read().RestartCountInWindow, "old launch replay cannot count twice")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.rearmAgentOn(human, tx, 7, 202) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		profiles, err := m.AgentWorkersOn(runner, tx, 7, 1, session.ID, session.ConnEpoch)
		require.NoError(t, err)
		require.Len(t, profiles, 1)
		require.True(t, profiles[0].RestartRequested)
		require.Equal(t, 2, profiles[0].ConfigRevision)
		require.Greater(t, profiles[0].LaunchVersion, launch+1)
		return nil
	}))
	require.NoError(t, report(&one, launch, "RUNNING", false))
	require.Nil(t, read().RunningConfigRevision, "human rearm fences old children")
	require.NoError(t, report(&one, launch, "EXITED", true))
	require.Equal(t, "ACTIVE", read().State)
	require.Zero(t, read().RestartCountInWindow)
	two := 2
	require.NoError(t, report(&two, read().Version, "RUNNING", false))
	require.Equal(t, 2, *read().RunningConfigRevision)
	// Same-revision old generations stay fenced after replacement RUNNING.
	require.NoError(t, report(&two, launch+1, "EXITED", true))
	require.Equal(t, "ACTIVE", read().State)
	require.Zero(t, read().RestartCountInWindow)
	// Legacy reports have no child revision evidence: explicitly unknown.
	require.NoError(t, report(nil, 0, "RUNNING", false))
	require.Nil(t, read().RunningConfigRevision)
	future := 3
	require.ErrorIs(t, report(&future, read().Version, "RUNNING", false), ErrVersionConflict)
}

func TestAgentWorkerStartupFailuresUseBoundedBackoffAndReplayFence(t *testing.T) {
	m, db, human, runner, session, p, in := agentRegistrationHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.RegisterAgentOn(human, tx, 7, in); return e }))
	q := query.BindAgents(db, m.names)
	read := func() *model.Agent {
		row, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).First()
		require.NoError(t, e)
		return row
	}
	desired := func() []AgentWorker {
		var profiles []AgentWorker
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			profiles, e = m.AgentWorkersOn(runner, tx, 7, 1, session.ID, session.ConnEpoch)
			return e
		}))
		return profiles
	}
	for attempt := 1; attempt <= 4; attempt++ {
		profiles := desired()
		require.Len(t, profiles, 1)
		launch := profiles[0].LaunchVersion
		fail := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				return m.ReportAgentPresenceOn(runner, tx, 7, 1, AgentPresenceSnapshot{SessionID: session.ID, ConnEpoch: session.ConnEpoch, Agents: []AgentPresence{{AgentPrincipalID: 202, State: "EXITED", LaunchVersion: launch, Attempted: true}}})
			})
		}
		if attempt == 1 {
			p.fail = true
			require.Error(t, fail())
			require.Equal(t, "ACTIVE", read().State)
			require.Zero(t, read().RestartCountInWindow)
			p.fail = false
		}
		require.NoError(t, fail())
		row := read()
		require.Equal(t, "CRASHED", row.State)
		require.Equal(t, min(attempt, 3), row.RestartCountInWindow)
		require.Equal(t, attempt == 4, row.RestartExhausted)
		require.NoError(t, fail())
		require.Equal(t, row.Version, read().Version)
		require.Equal(t, row.RestartCountInWindow, read().RestartCountInWindow)
		require.Empty(t, desired(), "startup failure must not retry before backoff")
		delay := 10 * time.Second
		if attempt == 2 {
			delay = time.Minute
		}
		if attempt >= 3 {
			delay = 5 * time.Minute
		}
		_, e := q.WithContext(t.Context()).Where(q.PrincipalID.Eq(guid.ID(202))).Update(q.ProcessStatusAt, time.Now().UTC().Add(-delay-time.Second))
		require.NoError(t, e)
	}
	require.Empty(t, desired(), "fourth failed start exhausts automatic attempts")
}
