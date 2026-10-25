package delivery

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"time"
)

func dependencyHarness(t *testing.T) (*Module, *gorm.DB, context.Context, WorkView, WorkView) {
	t.Helper()
	m, db, ctx, first := assignmentHarness(t, true)
	_, _, _, body := workHTTPHarness(t)
	var second workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; second, e = m.createWorkOn(ctx, tx, 1, 99, 11, body); return e }))
	return m, db, ctx, first.WorkView, second.WorkView
}
func TestDependenciesCycleCASReadinessAndHistory(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	var updated WorkView
	call := func(id, dependency guid.ID, version int, remove bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var e error
			updated, e = m.changeDependencyOn(ctx, tx, 1, 99, id, 11, 11, dependency, version, remove)
			return e
		})
	}
	require.NoError(t, call(work.ID, predecessor.ID, work.Version, false))
	require.Equal(t, "BLOCKED", updated.State)
	require.Equal(t, work.Version+1, updated.Version)
	require.ErrorIs(t, call(predecessor.ID, work.ID, predecessor.Version, false), ErrDependencyCycle)
	require.ErrorIs(t, call(work.ID, predecessor.ID, work.Version, false), ErrWorkVersion)
	rows, err := m.dependenciesOn(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Satisfied)
	require.NoError(t, call(work.ID, predecessor.ID, work.Version+1, true))
	require.Equal(t, "READY", updated.State)
	rows, err = m.dependenciesOn(ctx, db, 1, 99, work.ID, 11)
	require.NoError(t, err)
	require.Empty(t, rows)
	q := query.BindWorkTransitions(db, m.service.names)
	n, err := q.WithContext(ctx).Where(q.ReasonCode.In("DEPENDENCY_ADDED", "DEPENDENCY_REMOVED")).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
}

func TestDependenciesReportClosedCyclePath(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, 0, false)
		return err
	}))
	err := db.Transaction(func(tx *gorm.DB) error {
		_, err := m.changeDependencyOn(ctx, tx, 1, 99, predecessor.ID, 11, 11, work.ID, 0, false)
		return err
	})
	var cycle *dependencyCycleError
	require.ErrorAs(t, err, &cycle)
	require.Equal(t, []string{fmt.Sprintf("WI-%d", predecessor.Number), fmt.Sprintf("WI-%d", work.Number), fmt.Sprintf("WI-%d", predecessor.Number)}, cycle.Cycle)
	count, err := query.BindDependencies(db, m.service.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestDependencyCyclePathUsesShortestPathAndTerminatesOnExistingCycles(t *testing.T) {
	edges := []*model.WorkItemDependency{
		{WorkItemID: 2, DependsOnID: 3}, {WorkItemID: 2, DependsOnID: 4},
		{WorkItemID: 3, DependsOnID: 5}, {WorkItemID: 5, DependsOnID: 1},
		{WorkItemID: 4, DependsOnID: 1}, {WorkItemID: 5, DependsOnID: 2},
	}
	require.Equal(t, []guid.ID{1, 2, 4, 1}, dependencyCyclePath(edges, 1, 2))
	require.Nil(t, dependencyCyclePath(edges, 99, 2))
}

// The maximum DAG has 5000 nodes with up to 50 direct predecessors each.
// This measures the full in-memory graph construction and search, not DB I/O.
func BenchmarkDependencyCycleMaximumGraph(b *testing.B) {
	edges := make([]*model.WorkItemDependency, 0, 250000)
	for id := guid.ID(1); id <= 5000; id++ {
		for next := id + 1; next <= 5000 && next <= id+50; next++ {
			edges = append(edges, &model.WorkItemDependency{WorkItemID: id, DependsOnID: next})
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := dependencyCyclePath(edges, 5000, 1)
		if len(path) < 3 || path[0] != 5000 || path[len(path)-1] != 5000 {
			b.Fatal("missing closed path")
		}
	}
}

// Exercise the real query, prefix/tenant predicates and graph traversal against
// a disk-backed SQLite database. This is a measurement, not a claimed SLO.
func TestDependencyCycleDatabaseMeasurement(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	w := query.BindWorkItems(db, m.service.names)
	original, err := w.WithContext(ctx).Where(w.ID.Eq(predecessor.ID)).First()
	require.NoError(t, err)
	const nodes = 1000
	works := make([]*model.WorkItem, 0, nodes)
	ids := make([]guid.ID, nodes)
	for n := 0; n < nodes; n++ {
		row := *original
		row.ID = guid.ID(100000 + n)
		row.Number = 100 + n
		ids[n] = row.ID
		works = append(works, &row)
	}
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).CreateInBatches(works, 100).Error)
	edges := make([]*model.WorkItemDependency, 0, nodes*50)
	for n, id := range ids {
		for next := n + 1; next < nodes && next <= n+50; next++ {
			edges = append(edges, &model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: id, DependsOnID: ids[next]})
		}
	}
	require.NoError(t, db.Table(m.service.names.Dependencies.String()).CreateInBatches(edges, 500).Error)
	for sample := 0; sample < 3; sample++ {
		started := time.Now()
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			path, e := m.dependencyCycleOn(ctx, tx, 1, 99, ids[len(ids)-1], ids[0])
			if e != nil {
				return e
			}
			require.GreaterOrEqual(t, len(path), 3)
			require.Equal(t, ids[len(ids)-1], path[0])
			require.Equal(t, path[0], path[len(path)-1])
			foreign, e := m.dependencyCycleOn(ctx, tx, 2, 99, ids[len(ids)-1], ids[0])
			require.NoError(t, e)
			require.Empty(t, foreign)
			unrelated, e := m.dependencyCycleOn(ctx, tx, 1, 99, work.ID, ids[0])
			require.NoError(t, e)
			require.Empty(t, unrelated)
			return nil
		}))
		t.Logf("SQLite disk-backed cycle+tenant-isolation+acyclic query sample=%d nodes=%d edges=%d elapsed=%s", sample+1, nodes, len(edges), time.Since(started))
	}
}
func TestDependenciesPreserveClaimedWorkAndRollback(t *testing.T) {
	for _, scenario := range []string{"claimed", "audit", "outbox", "self", "foreign", "duplicate", "missing_remove"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, work, predecessor := dependencyHarness(t)
			if scenario == "claimed" {
				detail, e := m.readWork(ctx, db, 1, 99, work.ID, 11)
				require.NoError(t, e)
				claim := assignmentClaim(t, m, db, ctx, detail, false)
				work = claim.WorkItem
			}
			remove := false
			switch scenario {
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
					return 0, errors.New("outbox unavailable")
				})
			case "self":
				predecessor.ID = work.ID
			case "foreign":
				predecessor.ID = 999
			case "missing_remove":
				remove = true
			case "duplicate":
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, work.Version, false)
					return e
				}))
				work.Version++
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				updated, e := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, work.Version, remove)
				if e == nil && scenario == "claimed" {
					require.Equal(t, "CLAIMED", updated.State)
				}
				return e
			})
			if scenario == "claimed" || scenario == "duplicate" || scenario == "missing_remove" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			q := query.BindDependencies(db, m.service.names)
			count, e := q.WithContext(ctx).Count()
			require.NoError(t, e)
			if scenario == "claimed" || scenario == "duplicate" {
				require.EqualValues(t, 1, count)
			} else {
				require.Zero(t, count)
			}
		})
	}
}

func TestDependenciesRejectCancelledPredecessor(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	q := query.BindWorkItems(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.ID.Eq(predecessor.ID)).Update(q.State, "CANCELLED") //nolint:misspell // Frozen business state.
	require.NoError(t, err)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, work.Version, false)
		return e
	}))
	count, err := query.BindDependencies(db, m.service.names).WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestDependenciesDirectLimitAndArchivedCleanup(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	w := query.BindWorkItems(db, m.service.names)
	original, err := w.WithContext(ctx).Where(w.ID.Eq(predecessor.ID)).First()
	require.NoError(t, err)
	q := query.BindDependencies(db, m.service.names)
	for n := 1; n <= 50; n++ {
		row := *original
		row.ID += guid.ID(n)
		row.Number += n
		require.NoError(t, w.WithContext(ctx).Create(&row))
		require.NoError(t, q.WithContext(ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, DependsOnID: row.ID}))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, 0, false)
		return e
	}), ErrDependencyLimit)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 50, count)
	now := time.Now().UTC()
	_, err = w.WithContext(ctx).Where(w.ID.Eq(predecessor.ID+1)).UpdateSimple(w.State.Value("CANCELLED"), w.ArchivedAt.Value(now)) //nolint:misspell // Archived work must be canceled.
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID+1, 0, true)
		return e
	}))
	count, err = q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 49, count)
}

func TestDependencyMutationStorageFailuresCannotLeaveEdgesOrState(t *testing.T) {
	m, db, ctx, work, predecessor := dependencyHarness(t)
	failure := errors.New("dependency storage unavailable")
	rollback := errors.New("rollback successful dependency probe")
	for _, remove := range []bool{false, true} {
		if remove {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var e error
				work, e = m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, work.Version, false)
				return e
			}))
		}
		before, e := m.readWork(ctx, db, 1, 99, work.ID, 11)
		require.NoError(t, e)
		calls, failAt := 0, 0
		probe := func(tx *gorm.DB) {
			calls++
			if calls == failAt {
				tx.AddError(failure)
			}
		}
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("dependency-storage-failure", probe))
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("dependency-storage-failure", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("dependency-storage-failure", probe))
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("dependency-storage-failure", probe))
		run := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeDependencyOn(ctx, tx, 1, 99, work.ID, 11, 11, predecessor.ID, work.Version, remove)
				if e != nil {
					return e
				}
				return rollback
			})
		}
		require.ErrorIs(t, run(), rollback)
		total := calls
		require.Greater(t, total, 4)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, run(), failure, "remove=%v step=%d", remove, i)
		}
		require.NoError(t, db.Callback().Query().Remove("dependency-storage-failure"))
		require.NoError(t, db.Callback().Create().Remove("dependency-storage-failure"))
		require.NoError(t, db.Callback().Update().Remove("dependency-storage-failure"))
		require.NoError(t, db.Callback().Delete().Remove("dependency-storage-failure"))
		after, e := m.readWork(ctx, db, 1, 99, work.ID, 11)
		require.NoError(t, e)
		require.Equal(t, before, after)
	}
}
