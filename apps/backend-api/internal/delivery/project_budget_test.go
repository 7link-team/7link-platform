package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestBudgetCapEditStorageAndIdentityFailuresRollBackCurrentCap(t *testing.T) {
	for _, fault := range []string{"read", "update", "id", "zero", "pause", "holds"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			injected := errors.New("budget edit unavailable")
			want := injected
			budgetCap := int64(100)
			switch fault {
			case "read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("budget-edit-failure", func(tx *gorm.DB) { tx.AddError(injected) }))
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("budget-edit-failure", func(tx *gorm.DB) { tx.AddError(injected) }))
			case "id":
				m.ids = unavailableActivityIDs{err: injected}
			case "zero":
				m.ids = draftIDSource(func() (int64, error) { return 0, nil })
				want = ErrUnavailable
			case "pause":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("budget_spent_micros", 100).Error)
				m.executionHolds.ChangeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) ([]ExecutionHoldFact, error) {
					return nil, injected
				}
			case "holds":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("holds", "{}").Error)
				want = ErrUnavailable
			}
			e := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.changeProjectBudgetOn(ctx, tx, 1, 99, 11, 1, projectBudgetBody{CapMicros: &budgetCap, NotifyPct: 80, Reason: "Human updates accounted project cap"})
				return e
			})
			require.ErrorIs(t, e, want)
			require.NoError(t, db.Callback().Query().Remove("budget-edit-failure"))
			row, e := query.BindProjects(db, m.service.names).WithContext(ctx).Take()
			require.NoError(t, e)
			require.Equal(t, 1, row.Version)
			require.Nil(t, row.BudgetCapMicros)
		})
	}
}

func TestProjectBudgetEditPreservesSpendAndUnrelatedHolds(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	p := query.BindProjects(db, m.service.names)
	_, err := p.WithContext(ctx).Where(p.ProjectID.Eq(guid.ID(99))).UpdateSimple(p.BudgetSpentMicros.Value(100), p.Holds.Value(`[{"kind":"MANUAL","reason":"keep me"}]`))
	require.NoError(t, err)
	capacity := int64(100)
	in := &projectBudgetInput{EntityID: "99", IfMatch: `"1"`, Key: "budget-cap-once", Body: projectBudgetBody{CapMicros: &capacity, NotifyPct: 80, Reason: "Accounted cap adjustment"}}
	first, err := m.changeProjectBudgetHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, int64(100), first.Body.Data.BudgetSpentMicros)
	require.Equal(t, "EXHAUSTED", first.Body.Data.BudgetState)
	require.Contains(t, string(first.Body.Data.Holds), "keep me")
	require.Contains(t, string(first.Body.Data.Holds), "BUDGET")
	replay, err := m.changeProjectBudgetHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	history, err := m.projectBudgetEventsHTTP(ctx, &stageListInput{EntityID: "99", Limit: 10})
	require.NoError(t, err)
	require.Len(t, history.Body.Data, 3)
	in.IfMatch = `"2"`
	in.Key = "budget-release"
	capacity = 200
	raised, err := m.changeProjectBudgetHTTP(ctx, in)
	require.NoError(t, err)
	require.Equal(t, "NORMAL", raised.Body.Data.BudgetState)
	require.NotContains(t, string(raised.Body.Data.Holds), "BUDGET")
	require.Contains(t, string(raised.Body.Data.Holds), "keep me")
	require.Equal(t, int64(100), raised.Body.Data.BudgetSpentMicros)
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = m.changeProjectBudgetHTTP(ctx, in)
	assertWorkStatus(t, err, 403)
}

func TestProjectBudgetEditFailuresRollBackCapHistoryAndEffects(t *testing.T) {
	for _, fault := range []string{"version", "autonomous", "negative", "reason", "cap_max", "below_spent", "audit", "outbox", "history"} {
		t.Run(fault, func(t *testing.T) {
			m, db, ctx := constitutionHarness(t)
			capacity := int64(100)
			version := 1
			body := projectBudgetBody{CapMicros: &capacity, NotifyPct: 80, Reason: "Accounted cap adjustment"}
			failure := errors.New("budget unavailable")
			switch fault {
			case "version":
				version = 2
			case "autonomous":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"team_mode": "AUTONOMOUS", "budget_cap_micros": 200}).Error)
				body.CapMicros = nil
			case "negative":
				capacity = -1
			case "cap_max":
				capacity = 1_000_000_000_000_001
			case "below_spent":
				require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Update("budget_spent_micros", 200).Error)
			case "reason":
				body.Reason = "short"
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			case "outbox":
				m.service.events = projectPublisher(func(context.Context, *gorm.DB, events.Event) (guid.ID, error) { return 0, failure })
			case "history":
				require.NoError(t, db.Migrator().DropTable(m.service.names.BudgetEvents.String()))
			}
			err := db.Transaction(func(tx *gorm.DB) error { _, e := m.changeProjectBudgetOn(ctx, tx, 1, 99, 11, version, body); return e })
			require.Error(t, err)
			row, err := query.BindProjects(db, m.service.names).WithContext(ctx).First()
			require.NoError(t, err)
			require.Equal(t, 1, row.Version)
			require.Equal(t, 1, row.BudgetCapVersion)
			if fault == "autonomous" {
				require.NotNil(t, row.BudgetCapMicros)
				require.Equal(t, int64(200), *row.BudgetCapMicros)
			} else {
				require.Nil(t, row.BudgetCapMicros)
			}
			if fault != "history" {
				count, err := query.BindBudgetEvents(db, m.service.names).WithContext(ctx).Count()
				require.NoError(t, err)
				require.Zero(t, count)
			}
		})
	}
}

func TestProjectBudgetBelowSpendRequiresExplicitConfirmation(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	q := query.BindProjects(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1)), q.ProjectID.Eq(guid.ID(99))).Update(q.BudgetSpentMicros, 200)
	require.NoError(t, err)
	capacity := int64(100)
	body := projectBudgetBody{CapMicros: &capacity, NotifyPct: 80, Reason: "Explicit cost confirmation"}
	err = db.Transaction(func(tx *gorm.DB) error { _, e := m.changeProjectBudgetOn(ctx, tx, 1, 99, 11, 1, body); return e })
	require.ErrorIs(t, err, ErrInvalidArgument)
	body.ConfirmBelowSpent = true
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.changeProjectBudgetOn(ctx, tx, 1, 99, 11, 1, body)
		require.Equal(t, "EXHAUSTED", v.BudgetState)
		require.Equal(t, int64(200), v.BudgetSpentMicros)
		return e
	}))
}

func TestProjectBudgetRuntimeEnforcementRequestsPauseAndRollsBackFailure(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	require.NoError(t, db.Exec("CREATE TABLE budget_control_probe (kind TEXT)").Error)
	m.executionHolds = ExecutionHolds{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{11}, nil
	}, ChangeOn: func(_ context.Context, tx *gorm.DB, tenant, project, work guid.ID, release bool) ([]ExecutionHoldFact, error) {
		require.False(t, release)
		require.Equal(t, guid.ID(99), project)
		require.Zero(t, work)
		return []ExecutionHoldFact{{ExecutionID: 100, State: "RUNNING", Control: "PAUSE_REQUESTED"}}, tx.Exec("INSERT INTO budget_control_probe VALUES ('pause_requested')").Error
	}}
	capacity := int64(100)
	q := query.BindProjects(db, m.service.names)
	_, err := q.WithContext(ctx).Where(q.TenantID.Eq(guid.ID(1)), q.ProjectID.Eq(guid.ID(99))).Update(q.BudgetSpentMicros, 100)
	require.NoError(t, err)
	body := projectBudgetBody{CapMicros: &capacity, NotifyPct: 80, Reason: "Current runtime budget pause"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.changeProjectBudgetOn(ctx, tx, 1, 99, 11, 1, body); return e }))
	var count int64
	require.NoError(t, db.Table("budget_control_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.enforceProjectBudgetOn(ctx, tx, 1, 99) }))
	failure := errors.New("runtime control unavailable")
	m.executionHolds.ChangeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, bool) ([]ExecutionHoldFact, error) {
		return nil, failure
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.enforceProjectBudgetOn(ctx, tx, 1, 99) }), failure)
	_, err = q.WithContext(ctx).Where(q.ProjectID.Eq(guid.ID(99))).Update(q.BudgetState, "NORMAL")
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.enforceProjectBudgetOn(ctx, tx, 1, 99) }))
}

func TestBudgetEnforcementJobPagesEveryCurrentExhaustedProjectAndPreservesFailures(t *testing.T) {
	m, db, ctx := constitutionHarness(t)
	q := query.BindProjects(db, m.service.names)
	project, e := q.WithContext(ctx).First()
	require.NoError(t, e)
	capacity := int64(100)
	project.BudgetCapMicros = &capacity
	project.BudgetSpentMicros = 100
	project.BudgetState = "EXHAUSTED"
	require.NoError(t, db.Table(m.service.names.Projects.String()).Where("project_id=?", 99).Updates(map[string]any{"budget_cap_micros": 100, "budget_spent_micros": 100, "budget_state": "EXHAUSTED"}).Error)
	rows := []*model.Project{}
	for i := 0; i < 100; i++ {
		snapshot := *project
		snapshot.ProjectID = guid.ID(200 + i)
		snapshot.Code = "P" + snapshot.ProjectID.String()
		rows = append(rows, &snapshot)
	}
	require.NoError(t, q.WithContext(ctx).CreateInBatches(rows, 50))
	seen := map[guid.ID]bool{}
	failure := errors.New("budget enforcement source unavailable")
	fail := false
	m.executionHolds = ExecutionHolds{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{11}, nil
	}, ChangeOn: func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID, release bool) ([]ExecutionHoldFact, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Zero(t, work)
		require.False(t, release)
		if fail {
			return nil, failure
		}
		require.False(t, seen[project], "keyset must not repeat projects")
		seen[project] = true
		return nil, nil
	}}
	handler := m.projectBudgetEnforceHandler()
	require.ErrorIs(t, handler.Run(ctx, jobs.Job{}), ErrInvalidArgument)
	require.NoError(t, handler.Run(ctx, jobs.Job{TenantID: 1}))
	require.Len(t, seen, 101)
	fail = true
	require.ErrorIs(t, handler.Run(ctx, jobs.Job{TenantID: 1}), failure)
	fail = false
	runtimeDB := m.runtime.Database().Write()
	require.NoError(t, runtimeDB.Callback().Query().Before("gorm:query").Register("budget-job-storage", func(tx *gorm.DB) { tx.AddError(failure) }))
	require.ErrorIs(t, handler.Run(ctx, jobs.Job{TenantID: 1}), failure)
	require.NoError(t, runtimeDB.Callback().Query().Remove("budget-job-storage"))
	m.executionHolds.PrincipalsOn = nil
	require.ErrorIs(t, handler.Run(ctx, jobs.Job{TenantID: 1}), ErrUnavailable)
}
