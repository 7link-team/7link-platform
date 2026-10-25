package planning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGoalTargetMembershipAndParentReadAuthorityAreCurrent(t *testing.T) {
	h := setup(t)
	for _, subject := range []guid.ID{8, 9} {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.createOn(claims(t, 2), tx, 1, "goal", PlanningWriteInput{PrincipalID: subject, PeriodKind: "QUARTER", PeriodStart: "2026-10-01", Goal: &PlanningGoalInput{Level: "TEAM", Title: "Objective", KeyResults: []PlanningKeyResult{{Key: "k", Title: "Result", Source: "MANUAL", Target: 1}}}})
			return e
		})
		require.ErrorIs(t, err, ErrForbidden)
	}
	g := goalTree(t, h)
	old := h.m.entityPolicy
	h.m.entityPolicy = func(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		if permission == "goal_read" {
			return authz.DataConstraint{}, nil
		}
		return old(ctx, tx, tenant, project, permission, actor)
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, _, e := h.m.goalOn(claims(t, 2), tx, 1, 3, g.ID); return e }), ErrForbidden)
}

func TestGoalLifecycleManualEvidenceAndRealAutomaticMetrics(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	var body PlanningGoalInput
	require.NoError(t, json.Unmarshal(g.Content, &body))
	change := func(actor guid.ID, in PlanningGoalChange) (PlanningRecord, error) {
		in.Version = g.Version
		in.ExpectedContentHash = g.ContentHash
		var r PlanningRecord
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			r, e = h.m.changeGoalOn(claims(t, actor), tx, 1, g.ID, in)
			return e
		})
		if err == nil {
			g = r
		}
		return r, err
	}
	_, err := change(2, PlanningGoalChange{Action: "EDIT", Content: &body})
	require.ErrorIs(t, err, ErrConflict)
	_, err = change(2, PlanningGoalChange{Action: "CLOSE", Result: "ACHIEVED"})
	require.ErrorIs(t, err, ErrConflict)
	_, err = change(2, PlanningGoalChange{Action: "MANUAL", Key: "unknown", Current: 10})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = change(2, PlanningGoalChange{Action: "MANUAL", Key: "delivery", Current: 10, Evidence: "Accepted result record"})
	require.NoError(t, err)
	require.InDelta(t, 1, g.Progress, 1e-9)
	_, err = change(2, PlanningGoalChange{Action: "CLOSE", Result: "ACHIEVED"})
	require.NoError(t, err)
	_, err = change(2, PlanningGoalChange{Action: "MANUAL", Key: "delivery", Current: 0})
	require.ErrorIs(t, err, ErrConflict)
	// New member goal uses a canonical real accepted-work metric, not a client count.
	var member PlanningGoalInput
	require.NoError(t, json.Unmarshal(g.Content, &member))
	member.KeyResults[0].Source = "AUTO"
	member.KeyResults[0].Metric = "ACCEPTED_WORK_ITEMS"
	member.KeyResults[0].Current = 0
	g = create(t, h, "goal", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &member})
	_, err = change(7, PlanningGoalChange{Action: "ACTIVATE"})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = change(2, PlanningGoalChange{Action: "ACTIVATE"})
	require.NoError(t, err)
	late := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	h.facts.Work = append(h.facts.Work, h.facts.Work[0], delivery.PlanningWorkFact{ID: 999, State: "ACCEPTED", AccountablePrincipalID: 2, AcceptedAt: &late})
	_, err = change(2, PlanningGoalChange{Action: "REFRESH"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(g.Content, &member))
	require.EqualValues(t, 1, member.KeyResults[0].Current)
	before := g
	failure := errors.New("facts unavailable")
	h.m.facts.ReadOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time, time.Time) (delivery.PlanningSnapshot, error) {
		return delivery.PlanningSnapshot{}, failure
	}
	_, err = change(2, PlanningGoalChange{Action: "REFRESH"})
	require.ErrorIs(t, err, failure)
	var persisted PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		persisted, e = h.m.readOn(claims(t, 2), tx, 1, g.ID, "goal")
		return e
	}))
	require.Equal(t, before.ContentHash, persisted.ContentHash)
	require.Equal(t, before.Version, persisted.Version)
}

func TestPlanEditRequiresAuthorAndInvalidatesConfirmation(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	for _, action := range []string{"SUBMIT", "CONFIRM"} {
		actor := guid.ID(2)
		if action == "CONFIRM" {
			actor = 3
		}
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			p, e = h.m.decidePlanOn(claims(t, actor), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: action})
			return e
		}))
	}
	items := PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g), linkedItem(g)}}
	for _, actor := range []guid.ID{3, 7} {
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.editPlanOn(claims(t, actor), tx, 1, p.ID, p.Version, p.ContentHash, items)
			return e
		}), ErrForbidden)
	}
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, "stale", items)
		return e
	}), ErrConflict)
	var source string
	var reason any
	original := h.m.audit
	h.m.audit = func(ctx context.Context, tx *gorm.DB, ev audit.DomainEvent) error {
		source, _ = ev.Detail["authority_source"].(string)
		reason = ev.Detail["reason"]
		return original(ctx, tx, ev)
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		p, e = h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, items)
		return e
	}))
	require.Equal(t, "SUBMITTED", p.State)
	require.Equal(t, "IAM:plan_submit", source)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decidePlanOn(claims(t, 3), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: "RETURN", Reason: "Clarify next period"})
		return e
	}))
	require.Equal(t, "IAM:plan_confirm", source)
	require.Equal(t, "Clarify next period", reason)
	var count int64
	require.NoError(t, h.db.Table("p_domain_events").Where("event_type = ?", "planning.changed").Count(&count).Error)
	require.Positive(t, count)
}

func TestGoalProgressClipsDirectionsAndPreservesLargeIntegerDifferences(t *testing.T) {
	for _, tc := range []struct {
		baseline, target, current int64
		want                      float64
	}{
		{0, 10, 5, .5}, {10, 0, 5, .5}, {0, 10, 50, 1}, {10, 0, 20, 0},
		{9007199254740992, 9007199254740993, 9007199254740993, 1},
	} {
		require.InDelta(t, tc.want, goalProgress(PlanningGoalInput{KeyResults: []PlanningKeyResult{{Baseline: tc.baseline, Target: tc.target, Current: tc.current}}}), 1e-9)
	}
	require.Zero(t, goalProgress(PlanningGoalInput{}))
}

func TestGoalDraftEditCancellationAndAgentManualEvidence(t *testing.T) {
	h := setup(t)
	parent := goalTree(t, h)
	var g PlanningGoalInput
	require.NoError(t, json.Unmarshal(parent.Content, &g))
	g.Title = "Agent objective"
	var r PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		r, e = h.m.createOn(claims(t, 7), tx, 1, "goal", PlanningWriteInput{ProjectID: 3, PrincipalID: 7, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &g})
		return e
	}))
	require.Equal(t, "DRAFT", r.State)
	change := func(actor guid.ID, in PlanningGoalChange) error {
		in.Version = r.Version
		in.ExpectedContentHash = r.ContentHash
		return h.db.Transaction(func(tx *gorm.DB) error {
			next, e := h.m.changeGoalOn(claims(t, actor), tx, 1, r.ID, in)
			if e == nil {
				r = next
			}
			return e
		})
	}
	g.Title = "Edited objective"
	require.NoError(t, change(7, PlanningGoalChange{Action: "EDIT", Content: &g}))
	require.ErrorIs(t, change(7, PlanningGoalChange{Action: "MANUAL", Key: "delivery", Current: 3}), ErrInvalid)
	require.NoError(t, change(7, PlanningGoalChange{Action: "MANUAL", Key: "delivery", Current: 3, Evidence: "Result 100"}))
	require.ErrorIs(t, change(7, PlanningGoalChange{Action: "ACTIVATE"}), ErrForbidden)
	require.NoError(t, change(2, PlanningGoalChange{Action: "ACTIVATE"}))
	require.ErrorIs(t, change(7, PlanningGoalChange{Action: "CLOSE", Result: goalCancelled, Reason: "End work"}), ErrForbidden)
	require.ErrorIs(t, change(2, PlanningGoalChange{Action: "CLOSE", Result: goalCancelled}), ErrInvalid)
	require.NoError(t, change(2, PlanningGoalChange{Action: "CLOSE", Result: goalCancelled, Reason: "Scope retired"}))
	require.Equal(t, goalCancelled, r.State)
	require.ErrorIs(t, change(2, PlanningGoalChange{Action: "ACTIVATE"}), ErrConflict)
}

func TestChildGoalPeriodMustStayInsideParent(t *testing.T) {
	h := setup(t)
	member := goalTree(t, h)
	var g PlanningGoalInput
	require.NoError(t, json.Unmarshal(member.Content, &g))
	for _, periodStart := range []string{"2026-09-01", "2026-11-01"} {
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.createOn(claims(t, 2), tx, 1, "goal", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: periodStart, Goal: &g})
			return e
		}), ErrInvalid)
	}
}

func TestGoalValidationRejectsIndependentHierarchyAndMetricViolations(t *testing.T) {
	h := setup(t)
	member := goalTree(t, h)
	var original PlanningGoalInput
	require.NoError(t, json.Unmarshal(member.Content, &original))
	for _, mode := range []string{"nil", "title", "no_kr", "too_many", "level", "period", "project", "owner", "parent", "parent_kr", "unknown_parent_kr", "duplicate_kr", "target_baseline", "negative", "source", "auto_metric", "auto_forged"} {
		t.Run(mode, func(t *testing.T) {
			var g PlanningGoalInput
			require.NoError(t, json.Unmarshal(member.Content, &g))
			in := PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &g}
			switch mode {
			case "nil":
				in.Goal = nil
			case "title":
				g.Title = ""
			case "no_kr":
				g.KeyResults = nil
			case "too_many":
				for len(g.KeyResults) < 6 {
					g.KeyResults = append(g.KeyResults, g.KeyResults[0])
				}
			case "level":
				g.Level = "UNKNOWN"
			case "period":
				in.PeriodKind = "YEAR"
			case "project":
				in.ProjectID = 0
			case "owner":
				in.PrincipalID = 0
			case "parent":
				g.ParentGoalID = nil
			case "parent_kr":
				g.ParentKR = ""
			case "unknown_parent_kr":
				g.ParentKR = "missing"
			case "duplicate_kr":
				g.KeyResults = append(g.KeyResults, g.KeyResults[0])
			case "target_baseline":
				g.KeyResults[0].Target = g.KeyResults[0].Baseline
			case "negative":
				g.KeyResults[0].Current = -1
			case "source":
				g.KeyResults[0].Source = "FAKE"
			case "auto_metric":
				g.KeyResults[0].Source = "AUTO"
				g.KeyResults[0].Metric = "FAKE"
			case "auto_forged":
				g.KeyResults[0].Source = "AUTO"
				g.KeyResults[0].Metric = "ACCEPTED_WORK_ITEMS"
				g.KeyResults[0].Current = 100
			}
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateGoal(claims(t, 2), tx, 1, in) }), ErrInvalid)
		})
	}
	// Human evidence is advisory; an Agent cannot write an unbacked changed value.
	original.KeyResults[0].Current = 3
	original.KeyResults[0].Evidence = ""
	in := PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &original}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateGoal(claims(t, 2), tx, 1, in) }))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateGoal(claims(t, 7), tx, 1, in) }), ErrInvalid)
}

func TestGoalAuditFailureRollsBackEditedContentAndVersion(t *testing.T) {
	h := setup(t)
	member := goalTree(t, h)
	var g PlanningGoalInput
	require.NoError(t, json.Unmarshal(member.Content, &g))
	r := create(t, h, "goal", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "MONTH", PeriodStart: "2026-10-01", Goal: &g})
	g.Title = "Audited replacement"
	failure := errors.New("audit write failed")
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeGoalOn(claims(t, 2), tx, 1, r.ID, PlanningGoalChange{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "EDIT", Content: &g})
		return e
	}), failure)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		current, e := h.m.readOn(claims(t, 2), tx, 1, r.ID, "goal")
		require.Equal(t, r.Version, current.Version)
		require.Equal(t, r.ContentHash, current.ContentHash)
		return e
	}))
}

func TestGoalRejectedChangesPreserveVersionAndProgress(t *testing.T) {
	h := setup(t)
	r := goalTree(t, h)
	for _, in := range []PlanningGoalChange{
		{Action: "EDIT", Content: &PlanningGoalInput{}},
		{Action: "ACTIVATE"},
		{Action: "CLOSE", Result: "UNKNOWN"},
		{Action: "CLOSE", Result: goalCancelled},
		{Action: "CLOSE", Result: "ACHIEVED"},
		{Action: "MANUAL", Key: "missing", Current: 1},
		{Action: "MANUAL", Key: "delivery", Current: -1},
		{Action: "UNKNOWN"},
	} {
		in.Version, in.ExpectedContentHash = r.Version, r.ContentHash
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.changeGoalOn(claims(t, 2), tx, 1, r.ID, in); return e }))
		var current PlanningRecord
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			current, err = h.m.readOn(claims(t, 2), tx, 1, r.ID, "goal")
			return err
		})
		require.NoError(t, e)
		require.Equal(t, r.Version, current.Version)
		require.Equal(t, r.ContentHash, current.ContentHash)
		require.InDelta(t, r.Progress, current.Progress, 0.0001)
	}
	// Closure after the period is a Human decision even when the target was missed.
	h.m.now = func() time.Time { return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) }
	var closed PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		closed, e = h.m.changeGoalOn(claims(t, 2), tx, 1, r.ID, PlanningGoalChange{Version: r.Version, ExpectedContentHash: r.ContentHash, Action: "CLOSE", Result: "MISSED"})
		return e
	}))
	require.Equal(t, "MISSED", closed.State)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.changeGoalOn(claims(t, 2), tx, 1, r.ID, PlanningGoalChange{Version: closed.Version, ExpectedContentHash: closed.ContentHash, Action: "MANUAL", Key: "delivery", Current: 10})
		return e
	}), ErrConflict)
}

func TestGoalRefreshRiskUsesConfiguredLocalWorkdaysAndRecovers(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	h.m.now = func() time.Time { return time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC) }
	refresh := func() PlanningRecord {
		var out PlanningRecord
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = h.m.changeGoalOn(claims(t, 2), tx, 1, g.ID, PlanningGoalChange{Action: "REFRESH", Version: g.Version, ExpectedContentHash: g.ContentHash})
			return err
		}))
		g = out
		return out
	}
	require.Equal(t, "AT_RISK", refresh().State)
	h.m.now = func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }
	require.Equal(t, "ACTIVE", refresh().State)
}

func TestGoalRefreshCommandPreservesVersionOnMissingOrCorruptSources(t *testing.T) {
	for _, mode := range []string{"missing_facts", "period", "metric", "write", "read", "settings", "content"} {
		t.Run(mode, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			var body PlanningGoalInput
			require.NoError(t, json.Unmarshal(g.Content, &body))
			body.KeyResults[0].Source = "AUTO"
			body.KeyResults[0].Metric = "ACCEPTED_WORK_ITEMS"
			if mode == "metric" {
				body.KeyResults[0].Metric = "UNOBSERVED"
			}
			data, e := json.Marshal(body)
			require.NoError(t, e)
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", string(data)).Error)
			switch mode {
			case "missing_facts":
				h.m.facts.ReadOn = nil
			case "period":
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("period_start", "bad").Error)
			case "content":
				require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("content", "[").Error)
			case "read", "settings":
				table := h.m.names.Goals.String()
				if mode == "settings" {
					table = h.m.names.Settings.String()
				}
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("goal_command_read", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(ErrUnavailable)
					}
				}))
			case "write":
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("goal_command_write", func(tx *gorm.DB) {
					if tx.Statement.Table == h.m.names.Goals.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.changeGoalOn(claims(t, 2), tx, 1, g.ID, PlanningGoalChange{Version: g.Version, ExpectedContentHash: g.ContentHash, Action: "REFRESH"})
				return e
			})
			require.Error(t, err)
			require.NoError(t, h.db.Callback().Query().Remove("goal_command_read"))
			require.NoError(t, h.db.Callback().Update().Remove("goal_command_write"))
			var version int
			require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Select("version").Scan(&version).Error)
			require.Equal(t, g.Version, version)
		})
	}
}
