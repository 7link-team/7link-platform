package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestPlanningVersionLockAssignmentAndOwnerUnlock(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
		return delivery.PlanningWorkIdentity{State: "IN_PROGRESS", Type: "TASK", PrincipalID: 11}, nil
	}
	var version PlanningEntity
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		version, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "versions", 0, PlanningEntityInput{Name: "Release 1"})
		return e
	}))
	var assigned PlanningAssignment
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		assigned, e = m.assignPlanningOn(ctx, tx, 1, 99, 77, 0, PlanningAssignmentInput{VersionID: &version.ID})
		return e
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, version.ID, "versions", 1, "LOCKED", "", nil)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.assignPlanningOn(ctx, tx, 1, 99, 77, assigned.Version, PlanningAssignmentInput{})
		return e
	}), ErrVersionLocked)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.assignPlanningOn(ctx, tx, 1, 99, 78, 0, PlanningAssignmentInput{VersionID: &version.ID})
		return e
	}), ErrVersionLocked)
	m.projects.AuthorizeOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, p string) error {
		if p == "workflow_manage" {
			return ErrForbidden
		}
		return nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, version.ID, "versions", 2, "PLANNING", "", nil)
		return e
	}), ErrInvalid)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, version.ID, "versions", 2, "PLANNING", "scope correction", nil)
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.assignPlanningOn(ctx, tx, 1, 99, 77, 0, PlanningAssignmentInput{})
		return e
	}), ErrConflict)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.assignPlanningOn(ctx, tx, 1, 99, 77, 1, PlanningAssignmentInput{})
		return e
	}))
}

func TestPlanningCloseCarriesOnlyUnfinishedAndRollsBackOutboxFailure(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.planningWork = func(_ context.Context, _ *gorm.DB, _, _, id guid.ID) (delivery.PlanningWorkIdentity, error) {
		state := "IN_PROGRESS"
		if id == 78 {
			state = "ACCEPTED"
		}
		return delivery.PlanningWorkIdentity{State: state, Type: "TASK", PrincipalID: 11}, nil
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	nextEnd := end.Add(7 * 24 * time.Hour)
	var first, next PlanningEntity
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		first, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "iterations", 0, PlanningEntityInput{Name: "Sprint 1", StartsAt: &start, EndsAt: &end})
		if e != nil {
			return e
		}
		next, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "iterations", 0, PlanningEntityInput{Name: "Sprint 2", StartsAt: &end, EndsAt: &nextEnd})
		return e
	}))
	for _, id := range []guid.ID{77, 78, 79} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.assignPlanningOn(ctx, tx, 1, 99, id, 0, PlanningAssignmentInput{IterationID: &first.ID})
			return e
		}))
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, first.ID, "iterations", 1, "CLOSED", "", nil)
		return e
	}))
	q := query.BindPlanningCarryovers(db, m.names)
	receipts, e := q.WithContext(ctx).Order(q.WorkItemID).Find()
	require.NoError(t, e)
	require.Len(t, receipts, 2)
	require.Equal(t, guid.ID(77), receipts[0].WorkItemID)
	require.Equal(t, &next.ID, receipts[0].ToIterationID)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		done, e := m.assignmentOn(ctx, tx, 1, 99, 78)
		require.NoError(t, e)
		require.Equal(t, &first.ID, done.IterationID)
		return nil
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.assignPlanningOn(ctx, tx, 1, 99, 80, 0, PlanningAssignmentInput{IterationID: &first.ID})
		return e
	}), ErrIterationClosed)
	m.events = testPublisher{err: errors.New("outbox failed")}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "versions", 0, PlanningEntityInput{Name: "Must rollback"})
		return e
	}))
	v := query.BindVersions(db, m.names)
	count, e := v.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, count)
}

func TestPlanningEntityValidationAndFailedWritesPreserveStoredVersion(t *testing.T) {
	m, db, ctx := workHarness(t)
	start := m.now()
	end := start.Add(48 * time.Hour)
	zero := guid.ID(0)
	for _, tc := range []struct {
		kind string
		in   PlanningEntityInput
	}{
		{"unknown", PlanningEntityInput{Name: "valid"}},
		{"versions", PlanningEntityInput{Name: " "}},
		{"versions", PlanningEntityInput{Name: strings.Repeat("x", 121)}},
		{"versions", PlanningEntityInput{Name: "valid", Goal: strings.Repeat("x", 501)}},
		{"versions", PlanningEntityInput{Name: "valid", GoalID: &zero}},
		{"versions", PlanningEntityInput{Name: strings.Repeat("x", 81)}},
		{"iterations", PlanningEntityInput{Name: "valid"}},
		{"iterations", PlanningEntityInput{Name: strings.Repeat("x", 81), StartsAt: &start, EndsAt: &end}},
		{"milestones", PlanningEntityInput{Name: "valid"}},
	} {
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.savePlanningEntityOn(ctx, tx, 1, 99, 0, tc.kind, 0, tc.in)
			return e
		}), ErrInvalid)
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "versions", 1, PlanningEntityInput{Name: "valid"})
		return e
	}), ErrConflict)
	for _, mode := range []string{"id_error", "id_zero", "insert", "update", "outbox"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			fault := errors.New("planning storage unavailable")
			var entity PlanningEntity
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				entity, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "versions", 0, PlanningEntityInput{Name: "Original"})
				return e
			}))
			switch mode {
			case "id_error":
				m.ids = &testIDs{err: fault}
			case "id_zero":
				m.ids = &testIDs{n: -1}
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("planning_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Create().Remove("planning_storage")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("planning_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Versions.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Update().Remove("planning_storage")
			case "outbox":
				m.events = testPublisher{err: fault}
			}
			id := guid.ID(0)
			version := 0
			if mode == "update" || mode == "outbox" {
				id = entity.ID
				version = 1
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.savePlanningEntityOn(ctx, tx, 1, 99, id, "versions", version, PlanningEntityInput{Name: "Changed"})
				return e
			}))
			stored, e := m.planningEntityOn(ctx, db, 1, 99, entity.ID, "versions")
			require.NoError(t, e)
			require.Equal(t, "Original", stored.Name)
			require.Equal(t, 1, stored.Version)
			var count int64
			require.NoError(t, db.Table(m.names.Versions.String()).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestPlanningAssignmentSourceAndPersistenceFailuresAreAtomic(t *testing.T) {
	for _, mode := range []string{"source_missing", "source_denied", "read", "id_error", "id_zero", "insert", "update", "outbox", "missing_target"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			fault := errors.New("assignment unavailable")
			m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
				return delivery.PlanningWorkIdentity{State: "IN_PROGRESS", Type: "TASK"}, nil
			}
			version := 0
			if mode == "update" || mode == "outbox" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.assignPlanningOn(ctx, tx, 1, 99, 77, 0, PlanningAssignmentInput{})
					return e
				}))
				version = 1
			}
			switch mode {
			case "source_missing":
				m.planningWork = nil
			case "source_denied":
				m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
					return delivery.PlanningWorkIdentity{}, fault
				}
			case "id_error":
				m.ids = &testIDs{err: fault}
			case "id_zero":
				m.ids = &testIDs{n: -1}
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("assignment_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkPlanning.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Query().Remove("assignment_storage")
			case "insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("assignment_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkPlanning.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Create().Remove("assignment_storage")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("assignment_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkPlanning.String() {
						tx.AddError(fault)
					}
				}))
				defer db.Callback().Update().Remove("assignment_storage")
			case "outbox":
				m.events = testPublisher{err: fault}
			}
			in := PlanningAssignmentInput{}
			if mode == "missing_target" {
				id := guid.ID(999)
				in.MilestoneID = &id
			}
			require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.assignPlanningOn(ctx, tx, 1, 99, 77, version, in); return e }))
			if mode == "read" {
				require.NoError(t, db.Callback().Query().Remove("assignment_storage"))
			}
			var count int64
			require.NoError(t, db.Table(m.names.WorkPlanning.String()).Count(&count).Error)
			require.EqualValues(t, version, count)
			if version == 1 {
				row, e := m.assignmentOn(ctx, db, 1, 99, 77)
				require.NoError(t, e)
				require.Equal(t, 1, row.Version)
			}
		})
	}
}

func TestPlanningMilestoneRequiresCurrentAcceptedWorkAndAtomicStateWrites(t *testing.T) {
	for _, mode := range []string{"unbound", "empty", "source_error", "unfinished", "accepted", "query", "update", "outbox"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			due := m.now()
			var milestone PlanningEntity
			m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
				return delivery.PlanningWorkIdentity{Type: "TASK", State: "ACCEPTED"}, nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				milestone, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "milestones", 0, PlanningEntityInput{Name: "Release gate", DueAt: &due})
				return e
			}))
			if mode != "empty" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.assignPlanningOn(ctx, tx, 1, 99, 77, 0, PlanningAssignmentInput{MilestoneID: &milestone.ID})
					return e
				}))
			}
			switch mode {
			case "unbound":
				m.planningWork = nil
			case "source_error":
				m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
					return delivery.PlanningWorkIdentity{}, ErrUnavailable
				}
			case "unfinished":
				m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
					return delivery.PlanningWorkIdentity{State: "READY"}, nil
				}
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("milestone_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkPlanning.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("milestone_failure")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("milestone_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.Milestones.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Update().Remove("milestone_failure")
			case "outbox":
				m.events = testPublisher{err: ErrUnavailable}
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changePlanningStateOn(ctx, tx, 1, 99, milestone.ID, "milestones", 1, "ACHIEVED", "", nil)
				return e
			})
			stored, e := m.planningEntityOn(ctx, db, 1, 99, milestone.ID, "milestones")
			require.NoError(t, e)
			if mode == "accepted" {
				require.NoError(t, err)
				require.NotNil(t, stored.AchievedAt)
				require.Equal(t, 2, stored.Version)
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.changePlanningStateOn(ctx, tx, 1, 99, milestone.ID, "milestones", 2, "ACHIEVED", "", nil)
					return e
				}), ErrInvalid)
			} else {
				require.Error(t, err)
				require.Nil(t, stored.AchievedAt)
				require.Equal(t, 1, stored.Version)
			}
		})
	}
}

func TestPlanningCarryoverRejectsUnusableTargetsAndRollsBackReceiptFailure(t *testing.T) {
	for _, mode := range []string{"unbound", "self", "missing", "closed", "source", "workflow", "query", "receipt", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			start := m.now()
			end := start.Add(7 * 24 * time.Hour)
			nextEnd := end.Add(7 * 24 * time.Hour)
			m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
				return delivery.PlanningWorkIdentity{Type: "TASK", State: "IN_PROGRESS"}, nil
			}
			var first, next PlanningEntity
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				first, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "iterations", 0, PlanningEntityInput{Name: "First", StartsAt: &start, EndsAt: &end})
				if e != nil {
					return e
				}
				next, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "iterations", 0, PlanningEntityInput{Name: "Next", StartsAt: &end, EndsAt: &nextEnd})
				if e != nil {
					return e
				}
				_, e = m.assignPlanningOn(ctx, tx, 1, 99, 77, 0, PlanningAssignmentInput{IterationID: &first.ID})
				return e
			}))
			target := &next.ID
			switch mode {
			case "unbound":
				m.planningWork = nil
			case "self":
				target = &first.ID
			case "missing":
				id := guid.ID(999)
				target = &id
			case "closed":
				require.NoError(t, db.Table(m.names.Iterations.String()).Where("id=?", next.ID).Update("state", "CLOSED").Error)
			case "source":
				m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
					return delivery.PlanningWorkIdentity{}, ErrUnavailable
				}
			case "workflow":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("carry_workflow_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkflowConfigs.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("carry_workflow_failure")
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("carry_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.WorkPlanning.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("carry_failure")
			case "receipt":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("carry_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.PlanningCarryovers.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Create().Remove("carry_failure")
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changePlanningStateOn(ctx, tx, 1, 99, first.ID, "iterations", 1, "CLOSED", "", target)
				return e
			})
			if mode == "explicit" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			if mode == "query" {
				require.NoError(t, db.Callback().Query().Remove("carry_failure"))
			}
			assignment, e := m.assignmentOn(ctx, db, 1, 99, 77)
			require.NoError(t, e)
			if mode == "explicit" {
				require.Equal(t, target, assignment.IterationID)
			} else {
				require.Equal(t, &first.ID, assignment.IterationID)
				stored, e := m.planningEntityOn(ctx, db, 1, 99, first.ID, "iterations")
				require.NoError(t, e)
				require.Equal(t, "PLANNED", stored.State)
			}
		})
	}
}

func TestPlanningReleaseArchiveAndOptimisticStateBoundaries(t *testing.T) {
	m, db, ctx := workHarness(t)
	var release PlanningEntity
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		release, e = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "versions", 0, PlanningEntityInput{Name: "Release"})
		return e
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.savePlanningEntityOn(ctx, tx, 1, 99, release.ID, "versions", 99, PlanningEntityInput{Name: "Stale"})
		return e
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, release.ID, "versions", 99, "LOCKED", "", nil)
		return e
	}), ErrConflict)
	for i, state := range []string{"LOCKED", "RELEASED", "ARCHIVED"} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			var e error
			release, e = m.changePlanningStateOn(ctx, tx, 1, 99, release.ID, "versions", i+1, state, "", nil)
			return e
		}))
		require.Equal(t, state, release.State)
		if state == "LOCKED" {
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.savePlanningEntityOn(ctx, tx, 1, 99, release.ID, "versions", 2, PlanningEntityInput{Name: "Locked edit"})
				return e
			}), ErrVersionLocked)
		}
	}
	require.NotNil(t, release.ReleasedAt)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, release.ID, "versions", 4, "LOCKED", "", nil)
		return e
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, release.ID, "versions", 4, "ACTIVE", "", nil)
		return e
	}), ErrInvalid)
	_, e := m.planningEntityOn(ctx, db, 1, 99, 0, "versions")
	require.ErrorIs(t, e, ErrInvalid)
	m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "versions", 0, PlanningEntityInput{Name: "Denied"})
		return e
	}))
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changePlanningStateOn(ctx, tx, 1, 99, release.ID, "versions", 4, "PLANNING", "reason", nil)
		return e
	}))
}
