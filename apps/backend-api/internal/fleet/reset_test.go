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

func resetHarness(t *testing.T) *inspectionFixture {
	t.Helper()
	f := inspectionHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, f.db.Table(f.m.names.Agents.String()).Create(map[string]any{"id": 201, "tenant_id": 7, "principal_id": 202, "name": "Reset agent", "handle": "reset-agent", "avatar_seed": 1, "system_prompt": "Private prompt", "runtime_kind": "CODEX", "model_id": "model", "model_family": "family", "created_by_principal_id": 11, "owner_principal_id": 11, "state": "STOPPED", "machine_id": 1, "created_at": now, "updated_at": now}).Error)
	require.NoError(t, f.db.Exec("CREATE TABLE reset_completion (id INTEGER PRIMARY KEY,state TEXT NOT NULL)").Error)
	require.NoError(t, f.m.bindResetCompletion(func(_ context.Context, tx *gorm.DB, tenant, agent, id guid.ID, state string) error {
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 202, agent)
		return tx.Exec("INSERT INTO reset_completion VALUES (?,?)", id, state).Error
	}))
	return f
}

func (f *inspectionFixture) enqueueReset(t *testing.T, id guid.ID) {
	t.Helper()
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.enqueueAgentResetOn(f.ctx, tx, 7, 202, id, "SESSION", 2, 1) }))
}

func (f *inspectionFixture) pullReset() ([]ResetCommandView, error) {
	var out []ResetCommandView
	err := f.db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = f.m.pullAgentResetOn(f.ctx, tx, 7, 1, f.session.ID, f.session.ConnEpoch)
		return e
	})
	return out, err
}

func (f *inspectionFixture) reportReset(id guid.ID, state, reason string) error {
	return f.db.Transaction(func(tx *gorm.DB) error {
		_, err := f.m.reportAgentResetOn(f.ctx, tx, 7, 1, id, f.session.ID, f.session.ConnEpoch, state, reason)
		return err
	})
}

func TestAgentResetDispatchFencesReplayAndScheduling(t *testing.T) {
	f := resetHarness(t)
	f.enqueueReset(t, 900)
	require.ErrorIs(t, f.reportReset(900, "DONE", ""), ErrVersionConflict, "receipt before pull")
	for range 2 {
		out, err := f.pullReset()
		require.NoError(t, err)
		require.Len(t, out, 1)
		require.EqualValues(t, 900, out[0].ID)
		require.EqualValues(t, 202, out[0].AgentPrincipalID)
		require.Equal(t, "RUNNING", out[0].State)
		require.Equal(t, 2, out[0].SessionEpoch)
	}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.ReserveSlotOn(f.ctx, tx, 7, 1, 1000); return err }), ErrVersionConflict)
	for _, state := range []string{"FAILED", "DONE"} {
		if state == "FAILED" {
			require.NoError(t, f.reportReset(900, state, "UNSUPPORTED_AGENT_RUNTIME"))
			require.NoError(t, f.reportReset(900, state, "UNSUPPORTED_AGENT_RUNTIME"))
		} else {
			require.ErrorIs(t, f.reportReset(900, state, ""), ErrVersionConflict)
		}
	}
	var count int64
	require.NoError(t, f.db.Table("reset_completion").Count(&count).Error)
	require.EqualValues(t, 1, count)
	out, err := f.pullReset()
	require.NoError(t, err)
	require.Empty(t, out)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { _, err := f.m.ReserveSlotOn(f.ctx, tx, 7, 1, 1000); return err }))
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.enqueueAgentResetOn(f.ctx, tx, 7, 202, 901, "FULL", 3, 2) }), ErrVersionConflict)
}

func TestAgentResetReceiptFailureRollsBackCompletion(t *testing.T) {
	for _, fault := range []string{"completion", "command_update", "completion_missing"} {
		t.Run(fault, func(t *testing.T) {
			f := resetHarness(t)
			f.enqueueReset(t, 900)
			_, err := f.pullReset()
			require.NoError(t, err)
			sentinel := errors.New("completion unavailable")
			switch fault {
			case "completion":
				f.m.resetComplete = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error { return sentinel }
			case "completion_missing":
				f.m.resetComplete = nil
			case "command_update":
				require.NoError(t, f.db.Exec("CREATE TRIGGER reset_fail BEFORE UPDATE ON "+f.m.names.ResetCommands.String()+" BEGIN SELECT RAISE(ABORT,'failure'); END").Error)
			}
			require.Error(t, f.reportReset(900, "DONE", ""))
			q := query.BindResetCommands(f.db, f.m.names)
			row, err := q.WithContext(t.Context()).Where(q.ID.Eq(guid.ID(900))).First()
			require.NoError(t, err)
			require.Equal(t, "RUNNING", row.State)
			var count int64
			require.NoError(t, f.db.Table("reset_completion").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestAgentResetRejectsStaleWrongTenantAndInvalidReceipts(t *testing.T) {
	f := resetHarness(t)
	f.enqueueReset(t, 900)
	_, err := f.pullReset()
	require.NoError(t, err)
	for _, test := range []struct {
		id            guid.ID
		state, reason string
	}{{0, "DONE", ""}, {900, "UNKNOWN", ""}, {900, "DONE", "RESET_FAILED"}, {900, "FAILED", ""}, {900, "FAILED", "private text"}} {
		require.ErrorIs(t, f.reportReset(test.id, test.state, test.reason), ErrInvalidArgument)
	}
	require.ErrorIs(t, f.reportReset(999, "DONE", ""), ErrMachineNotFound)
	for _, test := range []struct {
		tenant, machine, session guid.ID
		epoch                    int64
		ctx                      context.Context
	}{
		{7, 1, f.session.ID, f.session.ConnEpoch + 1, f.ctx},
		{8, 1, f.session.ID, f.session.ConnEpoch, f.ctx},
		{7, 1, f.session.ID, f.session.ConnEpoch, authn.WithClaims(t.Context(), &authn.Claims{TenantID: 7, PrincipalID: 999})},
	} {
		require.Error(t, f.db.Transaction(func(tx *gorm.DB) error {
			_, err := f.m.reportAgentResetOn(test.ctx, tx, test.tenant, test.machine, 900, test.session, test.epoch, "DONE", "")
			return err
		}))
	}
	require.NoError(t, f.db.Exec("UPDATE "+f.m.names.ResetCommands.String()+" SET conn_epoch=conn_epoch+1 WHERE id=900").Error)
	require.ErrorIs(t, f.reportReset(900, "DONE", ""), ErrSessionStale)
}

func TestAgentResetTimeoutReleasesQueueWithoutClaimingSuccess(t *testing.T) {
	for _, cause := range []string{"expiry", "reconnect", "revoked", "callback_failure"} {
		t.Run(cause, func(t *testing.T) {
			f := resetHarness(t)
			f.enqueueReset(t, 900)
			require.NoError(t, f.m.expireAgentResets(f.ctx, f.db, 7, time.Now()))
			switch cause {
			case "reconnect":
				require.NoError(t, f.db.Exec("UPDATE "+f.m.names.Machines.String()+" SET conn_epoch=conn_epoch+1 WHERE id=1").Error)
			case "revoked":
				require.NoError(t, f.db.Exec("UPDATE "+f.m.names.Machines.String()+" SET state='REVOKED',name_key=NULL WHERE id=1").Error)
			default:
				require.NoError(t, f.db.Exec("UPDATE "+f.m.names.ResetCommands.String()+" SET expires_at=? WHERE id=900", time.Now().Add(-time.Second)).Error)
			}
			if cause == "callback_failure" {
				f.m.resetComplete = nil
				require.ErrorIs(t, f.m.expireAgentResets(f.ctx, f.db, 7, time.Now()), ErrUnavailable)
				return
			}
			require.NoError(t, f.m.expireAgentResets(f.ctx, f.db, 7, time.Now()))
			require.NoError(t, f.m.expireAgentResets(f.ctx, f.db, 7, time.Now()))
			var state string
			require.NoError(t, f.db.Table("reset_completion").Select("state").Scan(&state).Error)
			require.Equal(t, "TIMED_OUT", state)
		})
	}
}

func TestAgentResetPrepareAndEnqueueFailClosed(t *testing.T) {
	for _, cause := range []string{"invalid", "no_transaction", "missing_agent", "foreign_tenant", "draft", "unassigned", "offline", "no_lock", "no_validator", "disabled", "no_completion", "database"} {
		t.Run(cause, func(t *testing.T) {
			f := resetHarness(t)
			agent, tenant, id := guid.ID(202), guid.ID(7), guid.ID(900)
			switch cause {
			case "invalid":
				id = 0
			case "missing_agent":
				agent = 999
			case "foreign_tenant":
				tenant = 8
			case "draft":
				require.NoError(t, f.db.Exec("UPDATE "+f.m.names.Agents.String()+" SET state='DRAFT'").Error)
			case "unassigned":
				require.NoError(t, f.db.Exec("UPDATE "+f.m.names.Agents.String()+" SET machine_id=NULL").Error)
			case "offline":
				require.NoError(t, f.db.Exec("UPDATE "+f.m.names.Machines.String()+" SET state='OFFLINE'").Error)
			case "no_lock":
				f.m.principals.Lock = nil
			case "no_validator":
				f.m.accounts = nil
			case "disabled":
				f.m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, nil }
			case "no_completion":
				f.m.resetComplete = nil
			case "database":
				require.NoError(t, f.db.Exec("DROP TABLE "+f.m.names.Agents.String()).Error)
			}
			if cause == "no_transaction" {
				require.Error(t, f.m.enqueueAgentResetOn(f.ctx, f.db, tenant, agent, id, "SESSION", 2, 1))
				return
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.enqueueAgentResetOn(f.ctx, tx, tenant, agent, id, "SESSION", 2, 1) }))
			var count int64
			require.NoError(t, f.db.Table(f.m.names.ResetCommands.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
	f := resetHarness(t)
	require.ErrorIs(t, f.m.bindResetCompletion(nil), ErrInvalidArgument)
	require.ErrorIs(t, f.m.bindResetCompletion(f.m.resetComplete), ErrInvalidArgument)
}

func TestAgentResetStorageFailuresNeverCommitPartialCommandsOrCompletion(t *testing.T) {
	for _, scenario := range []struct {
		name, operation, table, kind string
		skip                         int
	}{
		{"prepare-machine", "prepare", "machine", "query", 0},
		{"prepare-lock-machine", "prepare", "machine", "query", 1},
		{"prepare-slots", "prepare", "slots", "query", 0},
		{"enqueue-agent-recheck", "enqueue", "agent", "query", 1},
		{"enqueue-machine-recheck", "enqueue", "machine", "query", 2},
		{"enqueue-insert", "enqueue", "reset", "create", 0},
		{"pull-read", "pull", "reset", "query", 0},
		{"pull-claim", "pull", "reset", "update", 0},
		{"receipt-read", "report", "reset", "query", 0},
		{"timeout-scan", "expire", "reset", "query", 0},
		{"timeout-machine", "expire", "machine", "query", 0},
		{"timeout-recheck", "expire", "reset", "query", 1},
		{"timeout-update", "expire", "reset", "update", 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := resetHarness(t)
			if scenario.operation == "pull" || scenario.operation == "report" || scenario.operation == "expire" {
				f.enqueueReset(t, 900)
			}
			if scenario.operation == "report" {
				_, err := f.pullReset()
				require.NoError(t, err)
			}
			if scenario.operation == "expire" {
				require.NoError(t, f.db.Exec("UPDATE "+f.m.names.ResetCommands.String()+" SET expires_at=?", time.Now().Add(-time.Second)).Error)
			}
			tables := map[string]string{"reset": f.m.names.ResetCommands.String(), "agent": f.m.names.Agents.String(), "machine": f.m.names.Machines.String(), "slots": f.m.names.Slots.String()}
			fired, skip := false, scenario.skip
			failure := errors.New("reset storage unavailable")
			hook := func(tx *gorm.DB) {
				if fired || tx.Statement.Table != tables[scenario.table] {
					return
				}
				if skip > 0 {
					skip--
					return
				}
				fired = true
				_ = tx.AddError(failure)
			}
			switch scenario.kind {
			case "query":
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("reset_failure", hook))
			case "create":
				require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("reset_failure", hook))
			case "update":
				require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("reset_failure", hook))
			}
			var err error
			switch scenario.operation {
			case "prepare":
				err = f.db.Transaction(func(tx *gorm.DB) error { return f.m.prepareAgentResetOn(f.ctx, tx, 7, 202) })
			case "enqueue":
				err = f.db.Transaction(func(tx *gorm.DB) error { return f.m.enqueueAgentResetOn(f.ctx, tx, 7, 202, 900, "SESSION", 2, 1) })
			case "pull":
				_, err = f.pullReset()
			case "report":
				err = f.reportReset(900, "DONE", "")
			case "expire":
				err = f.m.expireAgentResets(f.ctx, f.db, 7, time.Now())
			}
			require.True(t, fired)
			require.ErrorIs(t, err, failure)
			var completions int64
			require.NoError(t, f.db.Table("reset_completion").Count(&completions).Error)
			require.Zero(t, completions)
			q := query.BindResetCommands(f.db, f.m.names)
			rows, e := q.WithContext(t.Context()).Find()
			require.NoError(t, e)
			if scenario.operation == "prepare" || scenario.operation == "enqueue" {
				require.Empty(t, rows)
			} else {
				require.Len(t, rows, 1)
				expected := "QUEUED"
				if scenario.operation == "report" {
					expected = "RUNNING"
				}
				require.Equal(t, expected, rows[0].State)
			}
		})
	}
}

func TestAgentResetCurrentIAMFailureCannotEnqueue(t *testing.T) {
	for _, operation := range []string{"lock", "account"} {
		t.Run(operation, func(t *testing.T) {
			f := resetHarness(t)
			failure := errors.New("current IAM unavailable")
			if operation == "lock" {
				f.m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return failure }
			} else {
				f.m.accounts = func(context.Context, *gorm.DB, guid.ID, guid.ID, time.Time) (bool, error) { return false, failure }
			}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.enqueueAgentResetOn(f.ctx, tx, 7, 202, 900, "SESSION", 2, 1) }), failure)
			var count int64
			require.NoError(t, f.db.Table(f.m.names.ResetCommands.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
