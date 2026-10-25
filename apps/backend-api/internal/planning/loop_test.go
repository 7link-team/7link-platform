package planning

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/internal/planning/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLoopAuditFailureRollsBackDefinitionAndScheduler(t *testing.T) {
	for _, activate := range []bool{false, true} {
		t.Run(map[bool]string{false: "draft", true: "active"}[activate], func(t *testing.T) {
			h := loopHarness(t)
			failure := errors.New("audit unavailable")
			h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.createLoopOn(claims(t, 2), tx, 1, testLoopDefinition(), activate)
				return e
			}), failure)
			var n int64
			require.NoError(t, h.db.Table(h.m.names.LoopContracts.String()).Count(&n).Error)
			require.Zero(t, n)
			require.NoError(t, h.db.Table("scheduler_probe").Count(&n).Error)
			require.Zero(t, n)
		})
	}
	h := loopHarness(t)
	v := createTestLoop(t, h, true)
	failure := errors.New("audit unavailable")
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, v.Version, "PAUSED")
		return e
	}), failure)
	r, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
	require.NoError(t, e)
	require.Equal(t, "ACTIVE", r.State)
	require.Equal(t, v.Version, r.Version)
	var n int64
	require.NoError(t, h.db.Table("scheduler_probe").Where("state = ?", "PAUSED").Count(&n).Error)
	require.Zero(t, n)
}

func TestLoopAuthorityUsesActualAgentOwnerAndRequiresTransaction(t *testing.T) {
	h := loopHarness(t)
	r := &model.LoopContract{TenantID: 1, AgentID: 7, OwnerPrincipalID: 6}
	require.ErrorIs(t, h.m.loopAuthority(claims(t, 6), h.db, 1, r, true), ErrInvalid)
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.loopAuthority(claims(t, 6), tx, 1, r, true) }), ErrForbidden)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.loopAuthority(claims(t, 2), tx, 1, r, true) }))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.loopAuthority(claims(t, 7), tx, 1, r, false) }))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.loopAuthority(claims(t, 7), tx, 1, r, true) }), ErrForbidden)
}

func testLoopDefinition() LoopDefinition {
	project := guid.ID(3)
	interval := 15
	return LoopDefinition{ProjectID: &project, AgentID: 7, Kind: "CHECK", Name: "Periodic evidence check", Anchor: LoopAnchor{ChannelID: 4, ThreadRootMessageID: 5}, ScheduleKind: "INTERVAL", IntervalMinutes: &interval, SelfChecks: []LoopSelfCheck{{Key: "evidence", Description: "Inspect actual evidence", Required: true}}, Budget: LoopBudget{MaxDurationMinutes: 30}, Escalation: LoopEscalation{OnRequiredCheckFailed: true, ConsecutiveFailuresToPause: 3}, OutputFormat: "CHECK_RESULT"}
}
func loopHarness(t *testing.T) harness {
	h := setup(t)
	require.NoError(t, h.db.Exec("CREATE TABLE scheduler_probe(id BIGINT, state TEXT)").Error)
	h.m.loopReminders = agents.ReminderLoops{UpsertOn: func(_ context.Context, tx *gorm.DB, _ guid.ID, in agents.LoopReminderInput) (guid.ID, error) {
		id := guid.ID(500)
		if in.ReminderID > 0 {
			id = in.ReminderID
		}
		return id, tx.Exec("INSERT INTO scheduler_probe VALUES (?,?)", id, "ACTIVE").Error
	}, ChangeOn: func(_ context.Context, tx *gorm.DB, _, _, id guid.ID, state string) error {
		return tx.Exec("INSERT INTO scheduler_probe VALUES (?,?)", id, state).Error
	}}
	return h
}
func createTestLoop(t *testing.T, h harness, activate bool) LoopContractView {
	var v LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		v, e = h.m.createLoopOn(claims(t, 2), tx, 1, testLoopDefinition(), activate)
		return e
	}))
	return v
}
func TestLoopDraftActivationReminderAndRunsAreAtomic(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, true)
	require.Equal(t, "ACTIVE", v.State)
	require.NotNil(t, v.ReminderID)
	require.Contains(t, v.UnavailableCapabilities, "SETTLED_EXECUTION_COST")
	var first LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var skip bool
		var e error
		first, skip, e = h.m.openLoopRunOn(t.Context(), tx, 1, v.ID, 1, h.m.now())
		require.False(t, skip)
		return e
	}))
	require.Equal(t, "OPEN", first.State)
	require.Nil(t, first.CostMicros)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		again, skip, e := h.m.openLoopRunOn(t.Context(), tx, 1, v.ID, 1, h.m.now())
		require.NoError(t, e)
		require.False(t, skip)
		require.Equal(t, first.ID, again.ID)
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		next, skip, e := h.m.openLoopRunOn(t.Context(), tx, 1, v.ID, 2, h.m.now())
		require.NoError(t, e)
		require.True(t, skip)
		require.Equal(t, "SKIPPED", next.State)
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, v.Version, "ARCHIVED")
		return e
	}))
	q := query.BindLoopRuns(h.db, h.m.names)
	r, e := q.WithContext(t.Context()).Where(q.ID.Eq(first.ID)).First()
	require.NoError(t, e)
	require.Equal(t, "FAILED", r.State)
	require.NotNil(t, r.CompletedAt)
}
func TestLoopRollbackPermissionsAndUnavailableScheduling(t *testing.T) {
	h := loopHarness(t)
	for _, actor := range []guid.ID{7, 8, 9} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.createLoopOn(claims(t, actor), tx, 1, testLoopDefinition(), false)
			return e
		}))
	}
	original := h.m.loopReminders.UpsertOn
	h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, _ agents.LoopReminderInput) (guid.ID, error) {
		require.NoError(t, tx.Exec("INSERT INTO scheduler_probe VALUES (1,'FAIL')").Error)
		return 0, errors.New("scheduler refused")
	}
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.createLoopOn(claims(t, 2), tx, 1, testLoopDefinition(), true)
		return e
	}))
	var count int64
	require.NoError(t, h.db.Table(h.m.names.LoopContracts.String()).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, h.db.Table("scheduler_probe").Count(&count).Error)
	require.Zero(t, count)
	h.m.loopReminders.UpsertOn = original
	in := testLoopDefinition()
	in.ScheduleKind = "PERIOD"
	in.IntervalMinutes = nil
	period := "DAY"
	in.PeriodKind = &period
	var draft LoopContractView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		draft, e = h.m.createLoopOn(claims(t, 2), tx, 1, in, false)
		return e
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		active, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, draft.ID, 1, "ACTIVE")
		require.Equal(t, "ACTIVE", active.State)
		return e
	}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, draft.ID, 1, "ARCHIVED")
		return e
	}), ErrConflict)
}
func TestLoopDefinitionRejectsInvalidContractsAndAllowsBoundaryValues(t *testing.T) {
	invalid := []struct {
		name   string
		change func(*LoopDefinition)
	}{
		{"agent", func(v *LoopDefinition) { v.AgentID = 0 }},
		{"project", func(v *LoopDefinition) { x := guid.ID(0); v.ProjectID = &x }},
		{"owner", func(v *LoopDefinition) { v.OwnerPrincipalID = -1 }},
		{"blank name", func(v *LoopDefinition) { v.Name = " " }},
		{"anchor", func(v *LoopDefinition) { v.Anchor.ChannelID = 0 }},
		{"thread", func(v *LoopDefinition) { v.Anchor.ThreadRootMessageID = 0 }},
		{"interval low", func(v *LoopDefinition) { x := 14; v.IntervalMinutes = &x }},
		{"interval high", func(v *LoopDefinition) { x := 10081; v.IntervalMinutes = &x }},
		{"interval missing", func(v *LoopDefinition) { v.IntervalMinutes = nil }},
		{"mixed schedule", func(v *LoopDefinition) { x := "DAY"; v.PeriodKind = &x }},
		{"invalid schedule", func(v *LoopDefinition) { v.ScheduleKind = "CRON" }},
		{"period missing", func(v *LoopDefinition) { v.ScheduleKind = "PERIOD"; v.IntervalMinutes = nil }},
		{"cost", func(v *LoopDefinition) { v.Budget.MaxCostMicrosPerRun = -1 }},
		{"monthly cap", func(v *LoopDefinition) { x := int64(-1); v.Budget.MonthlyCostCapMicros = &x }},
		{"duration low", func(v *LoopDefinition) { v.Budget.MaxDurationMinutes = 4 }},
		{"duration high", func(v *LoopDefinition) { v.Budget.MaxDurationMinutes = 241 }},
		{"fixed escalation", func(v *LoopDefinition) { v.Escalation.OnRequiredCheckFailed = false }},
		{"pause threshold", func(v *LoopDefinition) { v.Escalation.ConsecutiveFailuresToPause = 11 }},
		{"required keys", func(v *LoopDefinition) { v.SelfChecks[0].Key = "UPPER" }},
		{"duplicate keys", func(v *LoopDefinition) { v.SelfChecks = append(v.SelfChecks, v.SelfChecks[0]) }},
		{"description", func(v *LoopDefinition) { v.SelfChecks[0].Description = "" }},
		{"output", func(v *LoopDefinition) { v.OutputFormat = "REPORT_DRAFT" }},
		{"kind", func(v *LoopDefinition) { v.Kind = "EXECUTION" }},
		{"check binding", func(v *LoopDefinition) { v.ReportBinding = &LoopReportBinding{Level: "MEMBER", PeriodKind: "DAY"} }},
		{"report binding", func(v *LoopDefinition) { v.Kind = "REPORT"; v.OutputFormat = "REPORT_DRAFT" }},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) { v := testLoopDefinition(); tt.change(&v); require.False(t, validLoop(v)) })
	}
	for _, minutes := range []int{15, 10080} {
		v := testLoopDefinition()
		v.IntervalMinutes = &minutes
		require.True(t, validLoop(v))
	}
	v := testLoopDefinition()
	v.Kind = "REPORT"
	v.OutputFormat = "REPORT_DRAFT"
	v.ReportBinding = &LoopReportBinding{Level: "MEMBER", PeriodKind: "WEEK"}
	require.True(t, validLoop(v))
	v.ReportBinding.Level = "PROJECT"
	require.True(t, validLoop(v))
	v.ProjectID = nil
	require.False(t, validLoop(v))
	v = testLoopDefinition()
	v.ScheduleKind = "PERIOD"
	v.IntervalMinutes = nil
	kind := "DAY"
	v.PeriodKind = &kind
	require.True(t, validLoop(v))
	kind = "INVALID"
	require.False(t, validLoop(v))
}

func TestLoopValidationUsesCurrentFleetOwnerAndRealAnchor(t *testing.T) {
	h := loopHarness(t)
	validate := func(v LoopDefinition) error {
		return h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateLoopOn(claims(t, 2), tx, 1, &v) })
	}
	v := testLoopDefinition()
	v.SelfChecks = nil
	v.Escalation.ConsecutiveFailuresToPause = 0
	require.NoError(t, validate(v))
	v = testLoopDefinition()
	v.Name = ""
	require.ErrorIs(t, validate(v), ErrInvalid)
	v = testLoopDefinition()
	v.ProjectID = nil
	require.NoError(t, validate(v))
	for _, owner := range []guid.ID{7, 9} {
		v = testLoopDefinition()
		v.OwnerPrincipalID = owner
		require.ErrorIs(t, validate(v), ErrInvalid)
	}
	original := h.m.agents.ByPrincipalOn
	h.m.agents.ByPrincipalOn = nil
	require.ErrorIs(t, validate(testLoopDefinition()), ErrUnavailable)
	failure := errors.New("fleet unavailable")
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, failure
	}
	require.ErrorIs(t, validate(testLoopDefinition()), failure)
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, nil
	}
	require.ErrorIs(t, validate(testLoopDefinition()), ErrInvalid)
	h.m.agents.ByPrincipalOn = original
	h.m.loopAnchors.CheckOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) error { return failure }
	require.ErrorIs(t, validate(testLoopDefinition()), failure)
	h.m.loopAnchors.CheckOn = nil
	require.ErrorIs(t, validate(testLoopDefinition()), ErrUnavailable)
}

func TestLoopStoredDefinitionRejectsCorruptJSON(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, false)
	row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
	require.NoError(t, e)
	for _, field := range []string{"anchor", "self_checks", "budget", "escalation", "report_binding"} {
		t.Run(field, func(t *testing.T) {
			corrupt := *row
			switch field {
			case "anchor":
				corrupt.Anchor = "{"
			case "self_checks":
				corrupt.SelfChecks = "{"
			case "budget":
				corrupt.Budget = "{"
			case "escalation":
				corrupt.Escalation = "{"
			case "report_binding":
				bad := "{"
				corrupt.ReportBinding = &bad
			}
			_, e := loopView(&corrupt)
			require.Error(t, e)
		})
	}
}

func TestLoopStateLimitAndReminderFailurePreserveContract(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, true)
	failure := errors.New("reminder change failed")
	for _, state := range []string{"PAUSED", "ARCHIVED"} {
		h.m.loopReminders.ChangeOn = func(_ context.Context, tx *gorm.DB, _, _, id guid.ID, _ string) error {
			require.NoError(t, tx.Exec("INSERT INTO scheduler_probe VALUES (?,'FAILED_CHANGE')", id).Error)
			return failure
		}
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, v.Version, state)
			return e
		}), failure)
		row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
		require.NoError(t, e)
		require.Equal(t, "ACTIVE", row.State)
		require.Equal(t, v.Version, row.Version)
		var n int64
		require.NoError(t, h.db.Table("scheduler_probe").Where("state = ?", "FAILED_CHANGE").Count(&n).Error)
		require.Zero(t, n)
	}
	h.m.loopReminders.ChangeOn = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, v.Version, "PAUSED")
		return e
	}), ErrUnavailable)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 7), tx, 1, v.ID, v.Version, "PAUSED")
		return e
	}), ErrForbidden)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, 999, 1, "ARCHIVED")
		return e
	}), ErrNotFound)
	h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, _ agents.LoopReminderInput) (guid.ID, error) {
		id, e := h.m.ids.Next()
		if e != nil {
			return 0, e
		}
		return guid.ID(id), tx.Exec("INSERT INTO scheduler_probe VALUES (?,'ACTIVE')", id).Error
	}
	for range 19 {
		createTestLoop(t, h, true)
	}
	draft := createTestLoop(t, h, false)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, draft.ID, 1, "ACTIVE")
		return e
	}), ErrConflict)
	row, e := h.m.loopOn(t.Context(), h.db, 1, draft.ID)
	require.NoError(t, e)
	require.Equal(t, "DRAFT", row.State)
	require.Nil(t, row.ReminderID)
	require.Equal(t, 1, row.Version)
}

func TestLoopAuthorityDependencyFailuresRemainFailClosed(t *testing.T) {
	h := loopHarness(t)
	row := &model.LoopContract{TenantID: 1, AgentID: 7, OwnerPrincipalID: 2}
	failure := errors.New("authority unavailable")
	check := func(ctx context.Context, want error) {
		t.Helper()
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.loopAuthority(ctx, tx, 1, row, true) }), want)
	}
	check(t.Context(), ErrForbidden)
	require.ErrorIs(t, h.m.loopAuthority(claims(t, 2), nil, 1, row, true), ErrInvalid)
	lock := h.m.principals.LockActiveRow
	h.m.principals.LockActiveRow = nil
	check(claims(t, 2), ErrUnavailable)
	h.m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, failure
	}
	check(claims(t, 2), failure)
	h.m.principals.LockActiveRow = lock
	human := h.m.principals.ActiveHumanByID
	h.m.principals.ActiveHumanByID = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{}, false, failure
	}
	check(claims(t, 2), failure)
	h.m.principals.ActiveHumanByID = human
	fleetLookup := h.m.agents.ByPrincipalOn
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, failure
	}
	check(claims(t, 2), failure)
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, nil
	}
	check(claims(t, 2), ErrForbidden)
	h.m.agents.ByPrincipalOn = fleetLookup
	h.m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, failure
	}
	check(claims(t, 6), failure)
	h.m.policy = func(_ context.Context, _ *gorm.DB, _ guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, "agent_manage", permission)
		require.Equal(t, guid.ID(6), actor)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.loopAuthority(claims(t, 6), tx, 1, row, true) }))
}

type loopIDFailure struct {
	id  int64
	err error
}

func (s loopIDFailure) Next() (int64, error) { return s.id, s.err }

func TestLoopAllocationFailuresDoNotPersistContractsOrRuns(t *testing.T) {
	h := loopHarness(t)
	v := createTestLoop(t, h, false)
	invalid := testLoopDefinition()
	invalid.Name = ""
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.createLoopOn(claims(t, 2), tx, 1, invalid, false); return e }), ErrInvalid)
	failure := errors.New("id allocation failed")
	for _, source := range []loopIDFailure{{err: failure}, {id: 0}, {id: int64(v.ID)}} {
		h.m.ids = source
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.createLoopOn(claims(t, 2), tx, 1, testLoopDefinition(), false)
			return e
		})
		require.Error(t, err)
		count, e := query.BindLoopContracts(h.db, h.m.names).WithContext(t.Context()).Count()
		require.NoError(t, e)
		require.EqualValues(t, 1, count)
	}
	h.m.ids = nil
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.createLoopOn(claims(t, 2), tx, 1, testLoopDefinition(), false)
		return e
	}), ErrUnavailable)
	for _, source := range []loopIDFailure{{err: failure}, {id: 0}} {
		h.m.ids = source
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, _, e := h.m.openLoopRunOn(t.Context(), tx, 1, v.ID, 1, h.m.now())
			return e
		})
		require.Error(t, err)
		count, e := query.BindLoopRuns(h.db, h.m.names).WithContext(t.Context()).Count()
		require.NoError(t, e)
		require.Zero(t, count)
	}
	for _, id := range []guid.ID{v.ID, 999} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, _, e := h.m.openLoopRunOn(t.Context(), tx, 1, id, 0, h.m.now()); return e }))
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, _, e := h.m.openLoopRunOn(t.Context(), tx, 1, 999, 1, h.m.now()); return e }), ErrNotFound)
	h.m.audit = nil
	row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
	require.NoError(t, e)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.recordLoop(claims(t, 2), tx, row, "PATCH") }), ErrUnavailable)
	h.m.ids = loopIDFailure{id: 10001}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, e := h.m.openLoopRunOn(t.Context(), tx, 1, v.ID, 1, h.m.now())
		return e
	}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, e := h.m.openLoopRunOn(t.Context(), tx, 1, v.ID, 2, h.m.now())
		return e
	}))
	runCount, e := query.BindLoopRuns(h.db, h.m.names).WithContext(t.Context()).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, runCount)
}

func TestLoopActivationRechecksAgentLockAndCASAfterScheduler(t *testing.T) {
	for _, mode := range []string{"revoked-agent", "lock-error", "cas"} {
		t.Run(mode, func(t *testing.T) {
			h := loopHarness(t)
			v := createTestLoop(t, h, false)
			failure := errors.New("principal lock failed")
			lock := h.m.principals.LockActiveRow
			if mode != "cas" {
				h.m.principals.LockActiveRow = func(ctx context.Context, tx *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
					if id == 7 {
						if mode == "lock-error" {
							return identity.Principal{}, false, failure
						}
						return identity.Principal{}, false, nil
					}
					return lock(ctx, tx, id)
				}
			} else {
				h.m.loopReminders.UpsertOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, _ agents.LoopReminderInput) (guid.ID, error) {
					require.NoError(t, tx.Exec("INSERT INTO scheduler_probe VALUES (500,'RACE')").Error)
					q := query.BindLoopContracts(tx, h.m.names)
					_, e := q.WithContext(t.Context()).Where(q.ID.Eq(v.ID)).Update(q.Version, v.Version+1)
					return 500, e
				}
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.changeLoopStateOn(claims(t, 2), tx, 1, v.ID, v.Version, "ACTIVE")
				return e
			})
			switch mode {
			case "revoked-agent":
				require.ErrorIs(t, err, ErrInvalid)
			case "lock-error":
				require.ErrorIs(t, err, failure)
			case "cas":
				require.ErrorIs(t, err, ErrConflict)
			}
			row, e := h.m.loopOn(t.Context(), h.db, 1, v.ID)
			require.NoError(t, e)
			require.Equal(t, "DRAFT", row.State)
			require.Equal(t, v.Version, row.Version)
			require.Nil(t, row.ReminderID)
			var n int64
			require.NoError(t, h.db.Table("scheduler_probe").Count(&n).Error)
			require.Zero(t, n)
		})
	}
}
