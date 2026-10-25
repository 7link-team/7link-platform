package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/infra/guid"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestReportQualityActualNumeratorsDeduplicateAndPreserveHistory(t *testing.T) {
	h := newResultFixture(t)
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	start := at.Add(-12 * time.Hour)
	end := start.Add(24 * time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityObservations.String()).Where("singleton=1").Update("observed_since", start.AddDate(0, 0, -20)).Error)
	var original model.WorkItem
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).First(&original).Error)
	ids := []guid.ID{}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < 20; i++ {
			work := original
			work.ID = guid.ID(8000 + i)
			work.Number = 8000 + i
			work.CreatedAt = start.AddDate(0, 0, -10)
			work.State = "NEEDS_HUMAN_REVIEW"
			if err := tx.Table(h.m.service.names.WorkItems.String()).Create(&work).Error; err != nil {
				return err
			}
			ids = append(ids, work.ID)
			rounds := 1
			if i == 14 {
				rounds = 2
			} else if i > 14 {
				rounds = 3
			}
			for retry := 0; retry < 2; retry++ {
				if err := h.m.recordReportL3On(h.ctx, tx, &work, "INTERNAL_QA", at); err != nil {
					return err
				}
				if err := h.m.recordReportQualityOn(h.ctx, tx, 1, 99, work.ID, "INTERNAL_QA_PASSED", 0, rounds, at); err != nil {
					return err
				}
			}
		}
		for retry := 0; retry < 2; retry++ {
			for i, kind := range []string{"ESCAPE_HUMAN", "ESCAPE_CLIENT"} {
				if err := h.m.recordReportQualityOn(h.ctx, tx, 1, 99, ids[0], kind, guid.ID(9000+i), 0, at); err != nil {
					return err
				}
			}
		}
		return nil
	}))
	read := func(from, to time.Time, work []guid.ID) *ReportQualityMetrics {
		t.Helper()
		var out *ReportQualityMetrics
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = h.m.reportQualityOn(h.ctx, tx, 1, 99, work, from, to)
			return err
		}))
		return out
	}
	out := read(start, end, ids)
	require.NotNil(t, out)
	require.EqualValues(t, 20, out.L3Entered)
	require.EqualValues(t, 20, out.InternalQACompleted)
	require.EqualValues(t, 14, out.InternalFirstPass)
	require.EqualValues(t, 31, out.InternalRoundsSum)
	require.InDelta(t, .1, *out.EscapeRate, 1e-9)
	require.InDelta(t, .7, *out.InternalFirstPassRate, 1e-9)
	require.InDelta(t, 1.55, *out.AvgInternalRounds, 1e-9)
	empty := read(end, end.AddDate(0, 0, 1), ids)
	require.Zero(t, empty.InternalQACompleted)
	require.Nil(t, empty.EscapeRate)
	require.Nil(t, empty.InternalFirstPassRate)
	require.Nil(t, empty.AvgInternalRounds)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id IN ?", ids).Updates(map[string]any{"internal_round_count": 99, "state": "REWORK"}).Error)
	require.Equal(t, out, read(start, end, ids))
	// Permission-filtered source scope cannot consume a different work item.
	scoped := read(start, end, ids[1:])
	require.EqualValues(t, 19, scoped.L3Entered)
	require.Zero(t, scoped.EscapesHuman+scoped.EscapesClient)
	require.Nil(t, read(start.AddDate(0, 0, -30), end, ids))
	// A legacy first observed success cannot prove a lifetime first success.
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", ids[0]).Update("created_at", start.AddDate(0, 0, -40)).Error)
	require.Nil(t, read(start, end, ids))
}
func TestReportQualityRealResultHookAndRollback(t *testing.T) {
	h := newResultFixture(t)
	_, err := h.submit()
	require.NoError(t, err)
	var n int64
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityReceipts.String()).Where("kind=?", "L3_ENTERED").Count(&n).Error)
	require.EqualValues(t, 1, n)
	failure := errors.New("source transaction rollback")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		if err := h.m.recordReportQualityOn(h.ctx, tx, 1, 99, h.work, "INTERNAL_QA_PASSED", 0, 2, time.Now().UTC()); err != nil {
			return err
		}
		return failure
	}), failure)
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityReceipts.String()).Where("kind=?", "INTERNAL_QA_PASSED").Count(&n).Error)
	require.Zero(t, n)
}

func TestReportMemberQualityPinsActualResultActorAndPreservesUnknown(t *testing.T) {
	h := newResultFixture(t)
	submitted, err := h.submit()
	require.NoError(t, err)
	at := time.Now().UTC()
	start, end := at.Add(-time.Hour), at.Add(time.Hour)
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityObservations.String()).Where("singleton=1").Update("observed_since", start.Add(-time.Hour)).Error)
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("created_at", start).Error)
	read := func(subject guid.ID, ids []guid.ID) *ReportQualityMetrics {
		t.Helper()
		var out *ReportQualityMetrics
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.reportMemberQualityOn(h.ctx, tx, 1, 99, subject, ids, start, end)
			return e
		}))
		return out
	}
	var receipt model.ReportQualityReceipt
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityReceipts.String()).Where("work_item_id=?", h.work).First(&receipt).Error)
	require.NotNil(t, receipt.ExecutorPrincipalID)
	require.Equal(t, submitted.ActorID, *receipt.ExecutorPrincipalID)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.recordReportQualityOn(h.ctx, tx, 1, 99, h.work, "INTERNAL_QA_PASSED", 0, 2, at, submitted.ID)
	}))
	actual := read(submitted.ActorID, []guid.ID{h.work})
	require.NotNil(t, actual)
	require.EqualValues(t, 1, actual.L3Entered)
	require.EqualValues(t, 1, actual.InternalQACompleted)
	require.InDelta(t, 2, *actual.AvgInternalRounds, 1e-9)
	// Ownership and current assignments cannot rewrite historical authorship.
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("accountable_principal_id", guid.ID(9876)).Error)
	require.Equal(t, actual, read(submitted.ActorID, []guid.ID{h.work}))
	other := read(9876, []guid.ID{h.work})
	require.NotNil(t, other)
	require.Zero(t, other.L3Entered)
	require.Nil(t, other.EscapeRate)
	scoped := read(submitted.ActorID, nil)
	require.NotNil(t, scoped)
	require.Zero(t, scoped.InternalQACompleted)
	// Missing identity remains unknown, never backfilled from today's owner.
	require.NoError(t, h.db.Table(h.m.service.names.ReportQualityReceipts.String()).Where("id=?", receipt.ID).Update("executor_principal_id", nil).Error)
	require.Nil(t, read(submitted.ActorID, []guid.ID{h.work}))
	require.Nil(t, read(9876, []guid.ID{h.work}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		out, e := h.m.reportMemberQualityOn(h.ctx, tx, 2, 99, submitted.ActorID, []guid.ID{h.work}, start, end)
		require.NotNil(t, out)
		require.Zero(t, out.L3Entered)
		return e
	}))
}
