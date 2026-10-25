package planning

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/planning/query"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlanningNotificationCurrentSourceAuthorIndependentHumanAndScope(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		f, found, err := h.m.planningNotificationFactOn(claims(t, 2), tx, 1, p.ID, "plan")
		require.NoError(t, err)
		require.True(t, found)
		require.Contains(t, f.LinkPath, "/entities/3/plans?record_id=")
		_, found, err = h.m.planningNotificationFactOn(claims(t, 2), tx, 2, p.ID, "plan")
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).Update("state", "SUBMITTED").Error)
	check := func(actor guid.ID, state string) bool {
		var yes bool
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			yes, err = h.m.planningNotificationEligibleOn(t.Context(), tx, 1, p.ID, actor, "plan", state)
			return err
		}))
		return yes
	}
	require.False(t, check(2, "SUBMITTED"), "author cannot confirm own source")
	require.True(t, check(3, "SUBMITTED"))
	require.False(t, check(7, "SUBMITTED"), "Agent is not a Human confirmation audience")
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.False(t, check(3, "SUBMITTED"))
	require.False(t, check(3, "CONFIRMED"), "current state must match original notification")
}

func TestReportCheckedReceiptNotificationInboxAndAtomicRollback(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		result, err := h.m.recordReportCheckOn(claims(t, 2), tx, 1, r.ID)
		require.Equal(t, "BLOCKED", result.Result)
		return err
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		yes, err := h.m.reportCheckedNotificationOn(claims(t, 2), tx, 1, r.ID, 2, r.Version)
		require.NoError(t, err)
		require.True(t, yes)
		yes, err = h.m.reportCheckedNotificationOn(claims(t, 3), tx, 1, r.ID, 3, r.Version)
		require.NoError(t, err)
		require.False(t, yes)
		return nil
	}))
	items, err := h.m.reportOverdueInbox(claims(t, 2), governance.InboxRequest{TenantID: 1, PrincipalID: 2, Kind: "REPORT_BLOCKED", Limit: 10})
	require.NoError(t, err)
	require.Len(t, items, 1)
	checks := query.BindReportChecks(h.db, h.m.names)
	before, err := checks.WithContext(t.Context()).Count()
	require.NoError(t, err)
	failure := errors.New("audit failure")
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := h.m.recordReportCheckOn(claims(t, 2), tx, 1, r.ID); return err }), failure)
	after, err := checks.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("version", r.Version+1).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		yes, err := h.m.reportCheckedNotificationOn(claims(t, 2), tx, 1, r.ID, 2, r.Version)
		require.NoError(t, err)
		require.False(t, yes)
		return nil
	}))
}

func TestReportCheckFencesArchivedProjectWritesButPreservesHistory(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	ctx := claims(t, 3) // A reader other than the author never needs report_submit.
	archived := true
	blocked := errors.New("project state fence: read only")
	h.m.entityPolicy = func(policyCtx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(3), project)
		require.Equal(t, guid.ID(3), actor)
		if archived && core.EntityMutationFromContext(policyCtx) {
			return authz.DataConstraint{}, blocked
		}
		return authz.DataConstraint{AllowAll: permission == "report_read"}, nil
	}
	count := func(table string) int64 {
		var n int64
		require.NoError(t, h.db.Table(table).Count(&n).Error)
		return n
	}
	checksBefore := count(h.m.names.ReportChecks.String())
	auditBefore := count("audit_probe")
	eventsBefore := count("p_domain_events")
	reports := query.BindReports(h.db, h.m.names)
	before, err := reports.WithContext(ctx).Where(reports.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.recordReportCheckOn(ctx, tx, 1, r.ID)
		return err
	}), blocked)
	require.Equal(t, checksBefore, count(h.m.names.ReportChecks.String()))
	require.Equal(t, auditBefore, count("audit_probe"))
	require.Equal(t, eventsBefore, count("p_domain_events"))
	after, err := reports.WithContext(ctx).Where(reports.ID.Eq(r.ID)).First()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, _, err := h.m.reportOn(ctx, tx, 1, r.ID, "report_read", false)
		return err
	}), "current authorized report history stays readable")
	archived = false
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := h.m.recordReportCheckOn(ctx, tx, 1, r.ID)
		return err
	}), "active-project readers retain report checking authority")
	require.Equal(t, checksBefore+1, count(h.m.names.ReportChecks.String()))
}

func TestGoalRiskNoticeUsesActualCurrentParentOwnerAndReadScope(t *testing.T) {
	h := setup(t)
	goal := goalTree(t, h)
	var body PlanningGoalInput
	require.NoError(t, json.Unmarshal(goal.Content, &body))
	require.NotNil(t, body.ParentGoalID)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", goal.ID).Update("state", "AT_RISK").Error)
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", *body.ParentGoalID).Update("principal_id", 3).Error)
	check := func(actor guid.ID) bool {
		var yes bool
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			yes, err = h.m.planningNotificationEligibleOn(t.Context(), tx, 1, goal.ID, actor, "goal", "AT_RISK")
			return err
		}))
		return yes
	}
	require.True(t, check(2))
	require.True(t, check(3))
	require.False(t, check(7))
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", *body.ParentGoalID).Update("principal_id", 2).Error)
	require.False(t, check(3))
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	require.False(t, check(2))
}

func TestPlanningNotificationFactsRejectInvalidAndUnavailableSources(t *testing.T) {
	h := setup(t)
	for _, kind := range []string{"goal", "report", "plan"} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			out, found, e := h.m.planningNotificationFactOn(t.Context(), tx, 1, 999, kind)
			require.False(t, found)
			require.Zero(t, out.ProjectID)
			return e
		})
		require.NoError(t, err)
		table := h.m.names.Goals.String()
		if kind == "report" {
			table = h.m.names.Reports.String()
		}
		if kind == "plan" {
			table = h.m.names.Plans.String()
		}
		require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("notification_fact_read_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(ErrUnavailable)
			}
		}))
		err = h.db.Transaction(func(tx *gorm.DB) error {
			_, found, e := h.m.planningNotificationFactOn(t.Context(), tx, 1, 999, kind)
			require.False(t, found)
			return e
		})
		require.ErrorIs(t, err, ErrUnavailable)
		require.NoError(t, h.db.Callback().Query().Remove("notification_fact_read_failure"))
	}
	_, _, e := h.m.planningNotificationFactOn(t.Context(), nil, 1, 1, "plan")
	require.ErrorIs(t, e, ErrInvalid)
	_, _, e = h.m.planningNotificationFactOn(t.Context(), h.db, 1, 1, "plan")
	require.ErrorIs(t, e, ErrInvalid)
}
func TestReportNeedsRecheckNotificationUsesCurrentAuthorAndConfirmerOnly(t *testing.T) {
	h := setup(t)
	r := reportDraft(t, h)
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Updates(map[string]any{"state": "SUBMITTED", "needs_recheck": true}).Error)
	for _, actor := range []guid.ID{2, 3, 4} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			yes, e := h.m.planningNotificationEligibleOn(t.Context(), tx, 1, r.ID, actor, "report", "NEEDS_RECHECK")
			require.Equal(t, actor == 2 || actor == 3, yes)
			return e
		}))
	}
	require.NoError(t, h.db.Table(h.m.names.Reports.String()).Where("id=?", r.ID).Update("needs_recheck", false).Error)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		yes, e := h.m.planningNotificationEligibleOn(t.Context(), tx, 1, r.ID, 2, "report", "NEEDS_RECHECK")
		require.False(t, yes)
		return e
	}))
}

func TestLoopNotificationRequiresCurrentEscalationAndPausedOwner(t *testing.T) {
	h := loopHarness(t)
	c := createTestLoop(t, h, true)
	cost := int64(1)
	settled := true
	bindLoopFacts(h, &cost, &settled)
	var run LoopRunView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		run, _, e = h.m.openLoopRunOn(t.Context(), tx, 1, c.ID, 1, h.m.now())
		return e
	}))
	check := func(id, actor guid.ID, typ string) (bool, error) {
		var yes bool
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			yes, e = h.m.loopNotificationEligibleOn(t.Context(), tx, 1, id, actor, typ)
			return e
		})
		return yes, e
	}
	yes, e := check(999, 2, "planning.loop_paused")
	require.NoError(t, e)
	require.False(t, yes)
	yes, e = check(run.ID, 2, "planning.loop_paused")
	require.NoError(t, e)
	require.False(t, yes)
	require.NoError(t, h.db.Table(h.m.names.LoopRuns.String()).Where("id=?", run.ID).Update("state", "TIMED_OUT").Error)
	yes, e = check(run.ID, 2, "planning.loop_paused")
	require.NoError(t, e)
	require.False(t, yes)
	require.NoError(t, h.db.Table(h.m.names.LoopContracts.String()).Where("id=?", c.ID).Update("state", "PAUSED").Error)
	reader := h.m.agents.ByPrincipalOn
	h.m.agents.ByPrincipalOn = nil
	yes, e = check(run.ID, 2, "planning.loop_paused")
	require.ErrorIs(t, e, ErrUnavailable)
	require.False(t, yes)
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{}, false, ErrUnavailable
	}
	yes, e = check(run.ID, 2, "planning.loop_paused")
	require.ErrorIs(t, e, ErrUnavailable)
	require.False(t, yes)
	h.m.agents.ByPrincipalOn = reader
	yes, e = check(run.ID, 2, "planning.loop_paused")
	require.NoError(t, e)
	require.True(t, yes)
	for _, table := range []string{h.m.names.LoopRuns.String(), h.m.names.LoopContracts.String()} {
		require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("notice_loop_read", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(ErrUnavailable)
			}
		}))
		yes, e = check(run.ID, 2, "planning.loop_paused")
		require.ErrorIs(t, e, ErrUnavailable)
		require.False(t, yes)
		require.NoError(t, h.db.Callback().Query().Remove("notice_loop_read"))
	}
}

func TestReportCheckPersistenceFailureNeverStoresPartialEvidence(t *testing.T) {
	for _, mode := range []string{"events", "audit", "id_error", "id_zero", "insert", "update"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			r := reportDraft(t, h)
			switch mode {
			case "events":
				h.m.events = nil
			case "audit":
				h.m.audit = nil
			case "id_error":
				h.m.ids = failingPlanningIDs{err: ErrUnavailable}
			case "id_zero":
				h.m.ids = failingPlanningIDs{}
			case "insert":
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("report_check_create", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.ReportChecks.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "update":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("report_check_update", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Reports.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.recordReportCheckOn(claims(t, 2), tx, 1, r.ID); return e })
			require.ErrorIs(t, err, ErrUnavailable)
			require.NoError(t, h.db.Callback().Create().Remove("report_check_create"))
			require.NoError(t, h.db.Callback().Update().Remove("report_check_update"))
			checks := query.BindReportChecks(h.db, h.m.names)
			count, e := checks.WithContext(t.Context()).Count()
			require.NoError(t, e)
			require.Zero(t, count)
			row, e := query.BindReports(h.db, h.m.names).WithContext(t.Context()).First()
			require.NoError(t, e)
			require.Equal(t, r.Version, row.Version)
			require.Empty(t, row.LastCheckHash)
		})
	}
}
