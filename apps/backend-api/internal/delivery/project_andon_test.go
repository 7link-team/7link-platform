package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectAndonRuntimeAndOutboxFailuresCannotLeavePartialHolds(t *testing.T) {
	for _, release := range []bool{false, true} {
		for _, broken := range []string{"runtime", "outbox"} {
			t.Run(broken+map[bool]string{false: "_pull", true: "_release"}[release], func(t *testing.T) {
				m, db, ctx, _ := stageHarness(t)
				m.andonAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
				}
				version := 1
				call := func() error {
					return db.Transaction(func(tx *gorm.DB) error {
						_, err := m.projectAndonOn(ctx, tx, 1, 99, 11, projectAndonBody{Version: version, Reason: "Human investigates an actual safety anomaly"}, release)
						return err
					})
				}
				if release {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
						_, err := m.projectAndonOn(ctx, tx, 1, 99, 11, projectAndonBody{Version: version, Reason: "Human identifies an actual safety anomaly"}, false)
						return err
					}))
					version++
				}
				q := query.BindProjects(db, m.service.names)
				before, err := q.WithContext(ctx).First()
				require.NoError(t, err)
				failure := errors.New("required participant unavailable")
				if broken == "runtime" {
					m.executionHolds.ChangeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) ([]ExecutionHoldFact, error) {
						return nil, failure
					}
				} else {
					m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
				}
				require.ErrorIs(t, call(), failure)
				after, err := q.WithContext(ctx).First()
				require.NoError(t, err)
				require.Equal(t, before, after, "control state and version must roll back together")
			})
		}
	}
}

func TestProjectAndonPreservesOtherHoldsAndUsesRealProjectEvents(t *testing.T) {
	m, db, ctx, work := stageHarness(t)
	m.andonAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	m.service.events = realDeliveryPublisher(t, db)
	original := `[{"kind":"BUDGET","source":"threshold","extra":{"unchanged":true}}]`
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("holds", original).Error)
	runtimeRelease := true
	m.executionHolds = ExecutionHolds{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) { return nil, nil }, ChangeOn: func(_ context.Context, _ *gorm.DB, _, _, id guid.ID, release bool) ([]ExecutionHoldFact, error) {
		require.Equal(t, work.ID, id)
		runtimeRelease = release
		return []ExecutionHoldFact{}, nil
	}}
	call := func(version int, release bool) (projectAndonView, error) {
		var out projectAndonView
		e := db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = m.projectAndonOn(ctx, tx, 1, 99, 11, projectAndonBody{Version: version, Reason: "Investigate project safety anomaly"}, release)
			return e
		})
		return out, e
	}
	pull, err := call(1, false)
	require.NoError(t, err)
	require.True(t, pull.Held)
	require.Positive(t, int64(pull.PullID))
	require.False(t, runtimeRelease)
	f, found, err := m.projectAndonFactOn(ctx, db, 1, 99)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, pull.PullID, f.OccurrenceID)
	release, err := call(2, true)
	require.NoError(t, err)
	require.False(t, release.Held)
	require.Equal(t, pull.PullID, release.PullID)
	require.False(t, runtimeRelease, "budget hold cannot resume runtime")
	q := query.BindProjects(db, m.service.names)
	p, err := q.WithContext(ctx).First()
	require.NoError(t, err)
	require.JSONEq(t, original, p.Holds)
	var rows []struct{ EventType, SubjectKind, Data string }
	require.NoError(t, db.Table("domain_events").Where("event_type IN ?", []string{"project.andon_pulled", "project.andon_cleared"}).Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	for _, r := range rows {
		require.Equal(t, "project", r.SubjectKind)
		var data map[string]any
		require.NoError(t, json.Unmarshal([]byte(r.Data), &data))
		require.Equal(t, pull.PullID.String(), data["pull_id"])
	}
	_, found, err = m.projectAndonFactOn(ctx, db, 1, 99)
	require.NoError(t, err)
	require.False(t, found)
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = call(3, false)
	require.Error(t, err)
	p, err = q.WithContext(ctx).First()
	require.NoError(t, err)
	require.Equal(t, 3, p.Version)
	require.JSONEq(t, original, p.Holds)
}

func TestWorkAndonClearCannotResumeUpstreamHold(t *testing.T) {
	m, db, ctx, work, upstream := dependencyHarness(t)
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	require.NoError(t, db.Table(m.service.names.Dependencies.String()).Create(map[string]any{"tenant_id": 1, "project_id": 99, "work_item_id": work.ID, "depends_on_id": upstream.ID}).Error)
	runtimeRelease := false
	m.executionHolds.ChangeOn = func(_ context.Context, _ *gorm.DB, _, _, _ guid.ID, release bool) ([]ExecutionHoldFact, error) {
		runtimeRelease = release
		return nil, nil
	}
	call := func(id guid.ID, version int, release bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.changeAndonOn(ctx, tx, 1, 99, id, 11, 11, andonBody{Version: version, Reason: "Investigate dependency quality anomaly"}, release)
			return e
		})
	}
	require.NoError(t, call(upstream.ID, upstream.Version, false))
	require.NoError(t, call(work.ID, work.Version, false))
	require.NoError(t, call(work.ID, work.Version+1, true))
	require.False(t, runtimeRelease)
}

func TestProjectAndonStorageFailuresPreserveExactHoldsAndVersion(t *testing.T) {
	m, db, ctx, _ := stageHarness(t)
	m.andonAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	failure := errors.New("andon storage unavailable")
	rollback := errors.New("rollback successful hold probe")
	for _, release := range []bool{false, true} {
		version := 1
		if release {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.projectAndonOn(ctx, tx, 1, 99, 11, projectAndonBody{Version: 1, Reason: "Investigate actual project anomaly"}, false)
				return e
			}))
			version = 2
		}
		before, e := query.BindProjects(db, m.service.names).WithContext(ctx).First()
		require.NoError(t, e)
		calls, failAt := 0, 0
		probe := func(tx *gorm.DB) {
			calls++
			if calls == failAt {
				tx.AddError(failure)
			}
		}
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("project-hold-source", probe))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("project-hold-source", probe))
		run := func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				_, e := m.projectAndonOn(ctx, tx, 1, 99, 11, projectAndonBody{Version: version, Reason: "Investigate actual project anomaly"}, release)
				if e != nil {
					return e
				}
				return rollback
			})
		}
		require.ErrorIs(t, run(), rollback)
		total := calls
		require.Greater(t, total, 1)
		for i := 1; i <= total; i++ {
			calls, failAt = 0, i
			require.ErrorIs(t, run(), failure)
		}
		require.NoError(t, db.Callback().Query().Remove("project-hold-source"))
		require.NoError(t, db.Callback().Update().Remove("project-hold-source"))
		after, e := query.BindProjects(db, m.service.names).WithContext(ctx).First()
		require.NoError(t, e)
		require.Equal(t, before, after)
	}
}
