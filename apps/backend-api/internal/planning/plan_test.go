package planning

import (
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"testing"
	"time"
)

func TestPlanExactLimitsAndStableIdentity(t *testing.T) {
	for _, tc := range []struct {
		kind, date string
		limit      int
	}{{"DAY", "2026-10-20", 30}, {"WEEK", "2026-10-19", 200}} {
		t.Run(tc.kind, func(t *testing.T) {
			h := setup(t)
			g := goalTree(t, h)
			items := make([]PlanningPlanItem, tc.limit)
			for i := range items {
				items[i] = linkedItem(g)
			}
			var p PlanningRecord
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				p, err = h.m.createOn(claims(t, 2), tx, 1, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: tc.kind, PeriodStart: tc.date, Plan: &PlanningPlanInput{Items: items}})
				return err
			}))
			var body PlanningPlanInput
			require.NoError(t, json.Unmarshal(p.Content, &body))
			seen := map[guid.ID]bool{}
			for _, item := range body.Items {
				require.Positive(t, int64(item.ID))
				require.False(t, seen[item.ID])
				seen[item.ID] = true
			}
			oversized := append(append([]PlanningPlanItem(nil), body.Items...), linkedItem(g))
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, err := h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, PlanningPlanInput{Items: oversized})
				return err
			}), ErrPlanLimit)
			forged := append([]PlanningPlanItem(nil), body.Items...)
			forged[0].ID = 999
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, err := h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, PlanningPlanInput{Items: forged})
				return err
			}), ErrInvalid)
			body.Items[0].Notes = "Retained identity"
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				saved, err := h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, body)
				var actual PlanningPlanInput
				require.NoError(t, json.Unmarshal(saved.Content, &actual))
				require.Equal(t, body.Items[0].ID, actual.Items[0].ID)
				return err
			}))
		})
	}
}

func TestConfirmedPlanAnnotationsPreserveConfirmationAndClock(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	for _, decision := range []struct {
		actor  guid.ID
		action string
	}{{2, "SUBMIT"}, {3, "CONFIRM"}} {
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			p, err = h.m.decidePlanOn(claims(t, decision.actor), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: decision.action})
			return err
		}))
	}
	var body PlanningPlanInput
	require.NoError(t, json.Unmarshal(p.Content, &body))
	var before model.Plan
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id = ?", p.ID).First(&before).Error)
	now := h.m.now().Add(time.Hour)
	h.m.now = func() time.Time { return now }
	hours, cost := 1.5, "9223372036854775807"
	body.Items[0].Notes, body.Items[0].EstimateHours, body.Items[0].EstimateCostMicros = "Actual estimate", &hours, &cost
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, body)
		return err
	}))
	require.Equal(t, "CONFIRMED", p.State)
	var after model.Plan
	require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id = ?", p.ID).First(&after).Error)
	require.Equal(t, before.SubmittedAt, after.SubmittedAt)
	require.Equal(t, before.UnconfirmedAt, after.UnconfirmedAt)
	body.Items = append(body.Items, linkedItem(g))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		p, err = h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, body)
		return err
	}))
	require.Equal(t, "SUBMITTED", p.State)
}

func TestPlanAnnotationsRejectInvalidNumbers(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		require.False(t, validPlanAnnotations(PlanningPlanItem{EstimateHours: &value}))
	}
	for _, value := range []string{"-1", "9223372036854775808", "1.5", "", "+1"} {
		require.False(t, validPlanAnnotations(PlanningPlanItem{EstimateCostMicros: &value}))
	}
	zero := 0.0
	require.True(t, validPlanAnnotations(PlanningPlanItem{EstimateHours: &zero}))
}

func TestLegacyPlanIdentityAllocationAlonePreservesConfirmation(t *testing.T) {
	previous := []PlanningPlanItem{{Title: "Existing scope", GoalID: 9, KR: "quality", Category: "OTHER"}}
	next := append([]PlanningPlanItem(nil), previous...)
	next[0].ID, next[0].Notes = 123, "Revised note"
	require.False(t, planNeedsConfirmation(previous, next))
	next[0].Title = "Changed scope"
	require.True(t, planNeedsConfirmation(previous, next))
	require.True(t, planNeedsConfirmation(next, nil))
}

func TestPlanDecisionsRespectAuthorPhaseAndAtomicAudit(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	decide := func(actor int64, action string) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.decidePlanOn(claims(t, guid.ID(actor)), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: action})
			return e
		})
	}
	require.ErrorIs(t, decide(3, "SUBMIT"), ErrForbidden)
	require.ErrorIs(t, decide(3, "CONFIRM"), ErrConflict)
	failure := errors.New("audit unavailable")
	appender := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return failure }
	require.ErrorIs(t, decide(2, "SUBMIT"), failure)
	h.m.audit = appender
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; p, e = h.m.readOn(claims(t, 2), tx, 1, p.ID, "plan"); return e }))
	require.Equal(t, "DRAFT", p.State)
	require.Equal(t, 1, p.Version)
	require.NoError(t, decide(2, "SUBMIT"))
	p.Version++
	require.ErrorIs(t, decide(2, "SUBMIT"), ErrConflict)
	require.ErrorIs(t, decide(2, "CONFIRM"), ErrForbidden)
	require.NoError(t, decide(3, "CONFIRM"))
	p.Version++
	require.ErrorIs(t, decide(3, "RETURN"), ErrConflict)
}

func TestPlanReturnNeedsReasonAndStaleDecisionFails(t *testing.T) {
	h := setup(t)
	g := goalTree(t, h)
	p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
	for _, in := range []PlanningDecisionInput{{Version: 0, Action: "SUBMIT", ExpectedContentHash: p.ContentHash}, {Version: 1, Action: "SUBMIT", ExpectedContentHash: "wrong"}, {Version: 1, Action: "UNKNOWN", ExpectedContentHash: p.ContentHash}} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.decidePlanOn(claims(t, 2), tx, 1, p.ID, in); return e }))
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decidePlanOn(claims(t, 2), tx, 1, p.ID, PlanningDecisionInput{Version: 1, Action: "SUBMIT", ExpectedContentHash: p.ContentHash})
		return e
	}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := h.m.decidePlanOn(claims(t, 3), tx, 1, p.ID, PlanningDecisionInput{Version: 2, Action: "RETURN", ExpectedContentHash: p.ContentHash, Reason: "bad"})
		return e
	}), ErrInvalid)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		r, e := h.m.decidePlanOn(claims(t, 3), tx, 1, p.ID, PlanningDecisionInput{Version: 2, Action: "RETURN", ExpectedContentHash: p.ContentHash, Reason: "Align next-period scope"})
		require.Equal(t, "RETURNED", r.State)
		return e
	}))
}

func TestPlanEditAndSubmitRejectCorruptSourcesWithoutChangingVersion(t *testing.T) {
	for _, action := range []string{"EDIT", "SUBMIT"} {
		for _, mode := range []string{"content", "period", "goal", "write", "read", "audit"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				h := setup(t)
				g := goalTree(t, h)
				p := create(t, h, "plan", PlanningWriteInput{ProjectID: 3, PrincipalID: 2, PeriodKind: "DAY", PeriodStart: "2026-10-20", Plan: &PlanningPlanInput{Items: []PlanningPlanItem{linkedItem(g)}}})
				var body PlanningPlanInput
				require.NoError(t, json.Unmarshal(p.Content, &body))
				switch mode {
				case "content":
					require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).Update("content", "[").Error)
				case "period":
					require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).Update("period_start", "bad").Error)
				case "goal":
					require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("id=?", g.ID).Update("state", "CLOSED").Error)
				case "write":
					require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("plan_mutation_write", func(tx *gorm.DB) {
						if tx.Statement.Table == h.m.names.Plans.String() {
							tx.AddError(ErrUnavailable)
						}
					}))
				case "read":
					require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("plan_mutation_read", func(tx *gorm.DB) {
						if tx.Statement.Table == h.m.names.Plans.String() {
							tx.AddError(ErrUnavailable)
						}
					}))
				case "audit":
					h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
				}
				err := h.db.Transaction(func(tx *gorm.DB) error {
					if action == "EDIT" {
						_, e := h.m.editPlanOn(claims(t, 2), tx, 1, p.ID, p.Version, p.ContentHash, body)
						return e
					}
					_, e := h.m.decidePlanOn(claims(t, 2), tx, 1, p.ID, PlanningDecisionInput{Version: p.Version, ExpectedContentHash: p.ContentHash, Action: action})
					return e
				})
				require.Error(t, err)
				require.NoError(t, h.db.Callback().Query().Remove("plan_mutation_read"))
				require.NoError(t, h.db.Callback().Update().Remove("plan_mutation_write"))
				var row model.Plan
				require.NoError(t, h.db.Table(h.m.names.Plans.String()).Where("id=?", p.ID).First(&row).Error)
				require.Equal(t, p.Version, row.Version)
				require.Equal(t, "DRAFT", row.State)
			})
		}
	}
}
