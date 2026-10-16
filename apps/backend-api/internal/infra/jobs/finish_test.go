package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func runningJob(t *testing.T, m *Module) model.Job {
	t.Helper()
	_, err := m.Enqueue(t.Context(), nil, EnqueueRequest{TenantID: 1, Kind: "jobs.work"})
	require.NoError(t, err)
	rows, err := m.claim(t.Context(), "default", "worker", 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	return rows[0]
}
func storedJob(t *testing.T, db *gorm.DB, id guid.ID) model.Job {
	t.Helper()
	var row model.Job
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", id).Take(&row).Error)
	return row
}

func TestHeartbeatAndSuccessfulFinishRejectStaleOwnership(t *testing.T) {
	m, db := queueHarness(t, 10)
	claimed := runningJob(t, m)
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", claimed.ID).Update("lease_expires_at_ms", 0).Error)
	updated, err := m.heartbeat(t.Context(), claimed)
	require.NoError(t, err)
	require.True(t, updated)
	row := storedJob(t, db, claimed.ID)
	require.Positive(t, *row.LeaseExpiresAtMS)
	require.Equal(t, claimed.Version+1, row.Version)
	stale := claimed
	stale.LeaseToken++
	updated, err = m.heartbeat(t.Context(), stale)
	require.NoError(t, err)
	require.False(t, updated)
	changed, err := m.finish(t.Context(), stale, m.handlers[claimed.Kind], nil, nil)
	require.NoError(t, err)
	require.False(t, changed)
	other := "other-worker"
	stale = claimed
	stale.LeaseOwner = &other
	changed, err = m.finish(t.Context(), stale, m.handlers[claimed.Kind], errors.New("old failure"), nil)
	require.NoError(t, err)
	require.False(t, changed)
	stale = claimed
	stale.TenantID = 2
	updated, err = m.heartbeat(t.Context(), stale)
	require.NoError(t, err)
	require.False(t, updated)
	changed, err = m.finish(t.Context(), claimed, m.handlers[claimed.Kind], nil, nil)
	require.NoError(t, err)
	require.True(t, changed)
	row = storedJob(t, db, claimed.ID)
	require.Equal(t, "SUCCEEDED", row.State)
	require.Nil(t, row.LeaseOwner)
	require.Nil(t, row.LeaseExpiresAtMS)
	require.NotNil(t, row.FinishedAt)
	require.Equal(t, 1, row.Attempts)
	require.Equal(t, claimed.Version+2, row.Version)
	updated, err = m.heartbeat(t.Context(), claimed)
	require.NoError(t, err)
	require.False(t, updated)
	changed, err = m.finish(t.Context(), claimed, m.handlers[claimed.Kind], nil, nil)
	require.NoError(t, err)
	require.False(t, changed)
}

func TestRetryHistoryAndExhaustionAreAtomic(t *testing.T) {
	m, db := queueHarness(t, 10)
	claimed := runningJob(t, m)
	require.NoError(t, db.Exec("CREATE TABLE dead_probe (job_id BIGINT PRIMARY KEY,version INTEGER)").Error)
	onDead := func(ctx context.Context, tx *gorm.DB, event DeadLetter) error {
		require.Equal(t, 8, event.Attempts)
		require.Equal(t, claimed.ID, event.JobID)
		require.Equal(t, guid.ID(1), event.TenantID)
		return tx.WithContext(ctx).Exec("INSERT INTO dead_probe VALUES (?,?)", event.JobID, event.Version).Error
	}
	for attempt := 1; attempt <= 8; attempt++ {
		failure := RetryAfter(0, errors.New("password=secret; payload="+strings.Repeat("private", 100)))
		changed, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], failure, onDead)
		require.NoError(t, err)
		require.True(t, changed)
		row := storedJob(t, db, claimed.ID)
		var history []failureRecord
		require.NoError(t, json.Unmarshal([]byte(row.ErrorHistory), &history))
		require.Len(t, history, min(attempt, 5))
		require.Equal(t, attempt, history[len(history)-1].Attempt)
		require.Equal(t, max(1, attempt-4), history[0].Attempt)
		require.NotContains(t, row.ErrorHistory, "secret")
		require.NotContains(t, row.LastError, "private")
		require.Equal(t, "HANDLER_ERROR", row.LastErrorCode)
		require.Nil(t, row.LeaseOwner)
		require.Nil(t, row.LeaseExpiresAtMS)
		if attempt < 8 {
			require.Equal(t, "PENDING", row.State)
			require.Nil(t, row.FinishedAt)
			rows, err := m.claim(t.Context(), "default", "worker", 1)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			claimed = rows[0]
		} else {
			require.Equal(t, "DEAD", row.State)
			require.NotNil(t, row.FinishedAt)
			var version int
			require.NoError(t, db.Table("dead_probe").Select("version").Where("job_id=?", row.ID).Scan(&version).Error)
			require.Equal(t, row.Version, version)
		}
	}
	changed, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], Permanent(errors.New("again")), onDead)
	require.NoError(t, err)
	require.False(t, changed)
	var count int64
	require.NoError(t, db.Table("dead_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestDeadLetterPublicationFailureRollsBackState(t *testing.T) {
	m, db := queueHarness(t, 10)
	claimed := runningJob(t, m)
	require.NoError(t, db.Exec("CREATE TABLE dead_probe (job_id BIGINT)").Error)
	failure := Permanent(errors.New("cannot complete"))
	changed, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], failure, nil)
	require.ErrorIs(t, err, ErrUnavailable)
	require.False(t, changed)
	publishErr := errors.New("event unavailable")
	changed, err = m.finish(t.Context(), claimed, m.handlers[claimed.Kind], failure, func(ctx context.Context, tx *gorm.DB, event DeadLetter) error {
		require.NoError(t, tx.WithContext(ctx).Exec("INSERT INTO dead_probe VALUES (?)", event.JobID).Error)
		return publishErr
	})
	require.ErrorIs(t, err, publishErr)
	require.False(t, changed)
	row := storedJob(t, db, claimed.ID)
	require.Equal(t, "RUNNING", row.State)
	require.Equal(t, claimed.Version, row.Version)
	require.JSONEq(t, "[]", row.ErrorHistory)
	var count int64
	require.NoError(t, db.Table("dead_probe").Count(&count).Error)
	require.Zero(t, count)
	changed, err = m.finish(t.Context(), claimed, m.handlers[claimed.Kind], failure, func(context.Context, *gorm.DB, DeadLetter) error { return nil })
	require.NoError(t, err)
	require.True(t, changed)
	row = storedJob(t, db, claimed.ID)
	require.Equal(t, "PERMANENT", row.LastErrorCode)
	require.Equal(t, "DEAD", row.State)
}

func TestTimeoutRetryPrecisionAndInvalidClaim(t *testing.T) {
	m, db := queueHarness(t, 10)
	claimed := runningJob(t, m)
	changed, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], RetryAfter(time.Nanosecond, context.DeadlineExceeded), nil)
	require.NoError(t, err)
	require.True(t, changed)
	row := storedJob(t, db, claimed.ID)
	require.Equal(t, "TIMEOUT", row.LastErrorCode)
	require.Equal(t, row.UpdatedAt.UnixMilli()+1, row.RunAtMS)
	for _, bad := range []model.Job{{}, {ID: 1, TenantID: -1}, {ID: 1, LeaseToken: 1}, {ID: 1, LeaseOwner: new(string), LeaseToken: 1}} {
		_, err = m.heartbeat(t.Context(), bad)
		require.ErrorIs(t, err, ErrInvalidRequest)
		_, err = m.finish(t.Context(), bad, Handler{}, nil, nil)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}
}

func TestFinishPreservesInvalidHistoryAndDependencyFailures(t *testing.T) {
	m, db := queueHarness(t, 10)
	claimed := runningJob(t, m)
	// JSON null and wrong element shapes are valid SQL JSON but invalid history.
	for _, history := range []string{"null", `[42]`} {
		require.NoError(t, db.Table("j_background_jobs").Where("id=?", claimed.ID).Update("error_history", history).Error)
		changed, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], errors.New("retry"), nil)
		require.Error(t, err)
		require.False(t, changed)
		require.Equal(t, "RUNNING", storedJob(t, db, claimed.ID).State)
	}
	unavailable := errors.New("guard failed")
	m.guard = func(context.Context, *gorm.DB, string) error { return unavailable }
	_, err := m.finish(t.Context(), claimed, m.handlers[claimed.Kind], errors.New("retry"), nil)
	require.ErrorIs(t, err, unavailable)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = m.heartbeat(ctx, claimed)
	require.Error(t, err)
	_, err = (*Module)(nil).finish(t.Context(), claimed, Handler{}, nil, nil)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestOldWorkerCannotRenewOrFinishReplacementAttempt(t *testing.T) {
	m, db := queueHarness(t, 10)
	old := runningJob(t, m)
	// Model recovery's state release; claim must issue a new fencing token even
	// when the same replica becomes the owner again.
	require.NoError(t, db.Table("j_background_jobs").Where("id=?", old.ID).Update("state", "PENDING").Error)
	rows, err := m.claim(t.Context(), "default", *old.LeaseOwner, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	replacement := rows[0]
	require.Greater(t, replacement.LeaseToken, old.LeaseToken)
	renewed, err := m.heartbeat(t.Context(), old)
	require.NoError(t, err)
	require.False(t, renewed)
	changed, err := m.finish(t.Context(), old, m.handlers[old.Kind], nil, nil)
	require.NoError(t, err)
	require.False(t, changed)
	current := storedJob(t, db, old.ID)
	require.Equal(t, "RUNNING", current.State)
	require.Equal(t, replacement.LeaseToken, current.LeaseToken)
	changed, err = m.finish(t.Context(), replacement, m.handlers[replacement.Kind], nil, nil)
	require.NoError(t, err)
	require.True(t, changed)
}
