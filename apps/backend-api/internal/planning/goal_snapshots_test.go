package planning

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/planning/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestGoalSnapshotPeriodBoundariesRemainImmutable(t *testing.T) {
	h := setup(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	h.m.now = func() time.Time { return now }
	goal := goalTree(t, h)
	change := func(current int64) {
		t.Helper()
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			goal, err = h.m.changeGoalOn(claims(t, 2), tx, 1, goal.ID, PlanningGoalChange{Version: goal.Version, ExpectedContentHash: goal.ContentHash, Action: "MANUAL", Key: "delivery", Current: current, Evidence: "Actual evidence"})
			return err
		}))
	}
	change(2)
	now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	change(6)
	report := reportDraft(t, h)
	read := func() []GoalKRChange {
		t.Helper()
		var out []GoalKRChange
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			r, _, err := h.m.reportOn(claims(t, 2), tx, 1, report.ID, "report_read", false)
			if err != nil {
				return err
			}
			out, err = h.m.reportKRChangesOn(claims(t, 2), tx, 1, r)
			return err
		}))
		return out
	}
	out := read()
	require.Len(t, out, 1)
	require.EqualValues(t, 2, *out[0].Start)
	require.EqualValues(t, 6, *out[0].End)
	require.EqualValues(t, 4, *out[0].Delta)
	now = time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	change(9)
	closed := read()
	require.EqualValues(t, 6, *closed[0].End)
	require.EqualValues(t, 4, *closed[0].Delta)
	// Direct mutation of the current row cannot rewrite the persisted history.
	var body PlanningGoalInput
	require.NoError(t, json.Unmarshal(goal.Content, &body))
	body.KeyResults[0].Current = 10
	encoded, _, err := hashJSON(body)
	require.NoError(t, err)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("tenant_id=? AND id=?", 1, goal.ID).Update("content", encoded).Error)
	require.Equal(t, closed, read())
}
func TestGoalSnapshotMissingStartAndAuditRollback(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	report := reportDraft(t, h)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		r, _, err := h.m.reportOn(claims(t, 2), tx, 1, report.ID, "report_read", false)
		if err != nil {
			return err
		}
		out, err := h.m.reportKRChangesOn(claims(t, 2), tx, 1, r)
		require.Len(t, out, 1)
		require.Nil(t, out[0].Start)
		require.Nil(t, out[0].Delta)
		require.Contains(t, out[0].MissingSources, "KR_PERIOD_START")
		return err
	}))
	var before int64
	require.NoError(t, h.db.Table(h.m.names.GoalSnapshots.String()).Count(&before).Error)
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit offline") }
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.changeGoalOn(claims(t, 2), tx, 1, g.ID, PlanningGoalChange{Version: g.Version, ExpectedContentHash: g.ContentHash, Action: "MANUAL", Key: "delivery", Current: 7})
		return err
	}))
	var after int64
	require.NoError(t, h.db.Table(h.m.names.GoalSnapshots.String()).Count(&after).Error)
	require.Equal(t, before, after)
	var stored model.Goal
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("tenant_id=? AND id=?", 1, g.ID).First(&stored).Error)
	require.Equal(t, g.Version, stored.Version)
}

func TestReportKRHistoryFailsClosedOnCorruptSnapshotAndStorage(t *testing.T) {
	for _, mode := range []string{"query", "snapshots", "snapshot_content", "goal_content", "period"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			r := reportDraft(t, h)
			var report model.Report
			require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).First(&report).Error)
			switch mode {
			case "query", "snapshots":
				table := h.m.names.Goals.String()
				if mode == "snapshots" {
					table = h.m.names.GoalSnapshots.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("kr_history_read", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "snapshot_content":
				require.NoError(t, h.db.Table(h.m.names.GoalSnapshots.String()).Where("goal_id=?", g.ID).Update("content", "[").Error)
			case "goal_content":
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", "[").Error)
			case "period":
				report.Content = "["
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.reportKRChangesOn(claims(t, 2), tx, 1, &report); return e })
			require.Error(t, err)
			require.NoError(t, h.db.Callback().Query().Remove("kr_history_read"))
		})
	}
}
