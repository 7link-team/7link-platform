package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/iam/audit"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

func TestApprovedToleranceChangeFailureAtEachPersistenceBoundaryRollsBackBothSubjects(t *testing.T) {
	for _, stage := range []string{"query", "update", "recompute", "audit", "outbox"} {
		t.Run(stage, func(t *testing.T) {
			m, db, _, _ := harness(t)
			toleranceFixture(t, m, db, "REQUIREMENT", 10)
			toleranceFixture(t, m, db, "PROJECT", 3)
			before := collaborationSnapshot(t, m, db)
			failure := errors.New("tolerance persistence unavailable")
			switch stage {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("regression_tolerance", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Tolerance.String() {
						tx.AddError(failure)
					}
				}))
			case "update", "recompute":
				seen := 0
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("regression_tolerance", func(tx *gorm.DB) {
					seen++
					if stage == "update" || seen == 2 {
						tx.AddError(failure)
					}
				}))
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				require.NoError(t, db.Exec("CREATE TRIGGER fail_tolerance_event BEFORE INSERT ON g_domain_events BEGIN SELECT RAISE(ABORT, 'tolerance outbox unavailable'); END").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return m.applyApprovedToleranceChangeOn(testContext(t, 2), tx, 3, 10, 99, ApprovedChangeDelta{ScheduleDeltaDays: 4, CostDeltaMicros: 100, ScopeDeltaCriteria: 20}, "source_intake_id")
			})
			require.Error(t, err)
			if stage != "outbox" {
				require.ErrorIs(t, err, failure)
			}
			require.NoError(t, db.Callback().Query().Remove("regression_tolerance"))
			require.NoError(t, db.Callback().Update().Remove("regression_tolerance"))
			require.Equal(t, before, collaborationSnapshot(t, m, db))
		})
	}
}

func TestToleranceProjectionRejectsInvalidArithmeticWithoutChangingBudget(t *testing.T) {
	m, db, _, _ := harness(t)
	toleranceFixture(t, m, db, "REQUIREMENT", 10)
	before := collaborationSnapshot(t, m, db)
	for _, delta := range []ApprovedChangeDelta{{ScheduleDeltaDays: 36501}, {CostDeltaMicros: math.MaxInt64}, {ScopeDeltaCriteria: math.MaxInt64}, {ScheduleDeltaDays: -36500}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.WouldBreachOn(testContext(t, 2), tx, 3, 10, delta); return err }))
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			return m.applyApprovedToleranceChangeOn(testContext(t, 2), tx, 3, 10, 99, delta)
		}))
		require.Equal(t, before, collaborationSnapshot(t, m, db))
	}
	for _, at := range []time.Time{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)} {
		days := -1
		if at.Year() == 9999 {
			days = 1
		}
		_, err := shiftedTime(at, days)
		require.ErrorIs(t, err, ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.applyApprovedToleranceChangeOn(testContext(t, 2), tx, 3, 10, 0, ApprovedChangeDelta{})
	}), ErrInvalid)
}

func TestApprovedChangeAdjustsRequirementAndOnlyBreachedProjectDimensions(t *testing.T) {
	m, db, _, _ := harness(t)
	r := toleranceFixture(t, m, db, "REQUIREMENT", 10)
	p := toleranceFixture(t, m, db, "PROJECT", 3)
	delta := ApprovedChangeDelta{ScheduleDeltaDays: 4, ScopeDeltaCriteria: 1}
	before := collaborationSnapshot(t, m, db)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		snapshot, e := m.WouldBreachOn(testContext(t, 2), tx, 3, 10, delta)
		require.NoError(t, e)
		require.Len(t, snapshot.Breached, 2)
		for _, b := range snapshot.Breached {
			require.Equal(t, "TIME", b.Dimension)
		}
		return e
	}))
	require.Equal(t, before, collaborationSnapshot(t, m, db))
	failure := errors.New("outer rollback")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.applyApprovedToleranceChangeOn(testContext(t, 2), tx, 3, 10, 99, delta))
		return failure
	}), failure)
	require.Equal(t, before, collaborationSnapshot(t, m, db))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return m.applyApprovedToleranceChangeOn(testContext(t, 2), tx, 3, 10, 99, delta)
	}))
	q := query.BindTolerance(db, m.names)
	req, e := q.WithContext(t.Context()).Where(q.ID.Eq(r.ID)).First()
	require.NoError(t, e)
	project, e := q.WithContext(t.Context()).Where(q.ID.Eq(p.ID)).First()
	require.NoError(t, e)
	require.Equal(t, int64(11), *req.AppetiteScopeCriteria)
	require.Equal(t, int64(10), *project.AppetiteScopeCriteria)
	require.Equal(t, 2, req.BaselineNo)
	require.Equal(t, 2, project.BaselineNo)
	require.WithinDuration(t, req.CreatedAt.Add(14*24*time.Hour), req.AppetiteEndAt, time.Second)
	var count int64
	require.NoError(t, db.Table("audit_probe").Where("kind = ?", "delivery.tolerance_changed").Count(&count).Error)
	require.Equal(t, int64(2), count)
}

func TestApprovedChangeOpenExceptionBlocksBothSubjectsBeforeAnyWrite(t *testing.T) {
	m, db, _, _ := harness(t)
	toleranceFixture(t, m, db, "REQUIREMENT", 10)
	p := toleranceFixture(t, m, db, "PROJECT", 3)
	raisedException(t, m, db, p)
	before := collaborationSnapshot(t, m, db)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.applyApprovedToleranceChangeOn(testContext(t, 2), tx, 3, 10, 99, ApprovedChangeDelta{ScheduleDeltaDays: 4})
	}), ErrOpenException)
	require.Equal(t, before, collaborationSnapshot(t, m, db))
}

func TestToleranceChangeDoesNotReadOtherProjectsOrReportNonpositiveBreaches(t *testing.T) {
	m, db, _, _ := harness(t)
	toleranceFixture(t, m, db, "PROJECT", 3)
	q := query.BindTolerance(db, m.names)
	row, e := q.WithContext(t.Context()).First()
	require.NoError(t, e)
	row.ID++
	row.ProjectID = 999
	row.SubjectID = 999
	require.NoError(t, q.WithContext(t.Context()).Create(row))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		snapshot, e := m.WouldBreachOn(testContext(t, 2), tx, 3, 10, ApprovedChangeDelta{ScheduleDeltaDays: 0, ScopeDeltaCriteria: -1})
		require.NoError(t, e)
		require.Len(t, snapshot.Subjects, 1)
		require.Empty(t, snapshot.Breached)
		return e
	}))
}
