package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestWorkflowRequiredFieldsAndRolesDoNotInferMissingValues(t *testing.T) {
	m, db, ctx, detail := assignmentHarness(t, true)
	text, empty, minutes, zero, actor := "explicit value", "", 30, 0, guid.ID(11)
	for _, tc := range []struct {
		rule string
		set  func(*model.WorkItem, bool)
	}{
		{"introduced_reason", func(w *model.WorkItem, valid bool) {
			w.IntroducedReason = &empty
			if valid {
				w.IntroducedReason = &text
			}
		}},
		{"module", func(w *model.WorkItem, valid bool) {
			w.Module = &empty
			if valid {
				w.Module = &text
			}
		}},
		{"estimate_minutes", func(w *model.WorkItem, valid bool) {
			w.EstimateMinutes = &zero
			if valid {
				w.EstimateMinutes = &minutes
			}
		}},
		{"introduced_stage", func(w *model.WorkItem, valid bool) {
			w.IntroducedStage = &empty
			if valid {
				w.IntroducedStage = &text
			}
		}},
		{"fixed_version_id", func(w *model.WorkItem, valid bool) {
			w.FixedVersion = &empty
			if valid {
				w.FixedVersion = &text
			}
		}},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				w := &model.WorkItem{TenantID: 1, ProjectID: 99, ID: detail.ID}
				passed, err := m.workflowFactOn(ctx, tx, w, actor, "REQUIRED_FIELDS", tc.rule, "")
				require.NoError(t, err)
				require.False(t, passed)
				for _, valid := range []bool{false, true} {
					tc.set(w, valid)
					passed, err = m.workflowFactOn(ctx, tx, w, actor, "REQUIRED_FIELDS", tc.rule, "")
					require.NoError(t, err)
					require.Equal(t, valid, passed)
				}
				return nil
			}))
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		w := &model.WorkItem{TenantID: 1, ProjectID: 99, ID: detail.ID, AccountablePrincipalID: actor, CreatedBy: &actor}
		for _, rule := range []string{"ACCOUNTABLE", "REPORTER"} {
			for _, candidate := range []guid.ID{0, actor, actor + 1} {
				passed, err := m.workflowFactOn(ctx, tx, w, candidate, "WORK_ITEM_ROLE", rule, "")
				require.NoError(t, err)
				require.Equal(t, candidate == actor, passed)
			}
		}
		passed, err := m.workflowFactOn(ctx, tx, w, actor, "REQUIRED_FIELDS", "acceptance_criteria", "")
		require.NoError(t, err)
		require.True(t, passed)
		w.ID++
		passed, err = m.workflowFactOn(ctx, tx, w, actor, "REQUIRED_FIELDS", "acceptance_criteria", "")
		require.NoError(t, err)
		require.False(t, passed)
		for _, kind := range []string{"UNKNOWN", "REQUIRED_FIELDS", "WORK_ITEM_ROLE", "RELATED_OBJECTS"} {
			passed, err = m.workflowFactOn(ctx, tx, w, actor, kind, "UNKNOWN", "")
			require.ErrorIs(t, err, ErrUnavailable)
			require.False(t, passed)
		}
		return nil
	}))
}

// Workflow predicates must consume the current canonical result. A supported
// predicate with missing evidence must stay false; unknown predicates must stay
// unavailable, even when another check on the result has passed.
func TestWorkflowEvidenceTracksCanonicalCheckAndRejectsMissingClaim(t *testing.T) {
	h := newResultFixture(t)
	probe := func(rule string) (bool, error) {
		var passed bool
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var err error
			passed, err = h.m.workflowFactOn(h.ctx, tx, h.stored(), 11, "EVIDENCE", rule, "")
			return err
		})
		return passed, err
	}
	for _, rule := range []string{"EVERY_AC_HAS_PASS_EVIDENCE", "INTERNAL_QA_PASSED"} {
		passed, err := probe(rule)
		require.NoError(t, err)
		require.False(t, passed)
	}
	passed, err := probe("UNKNOWN")
	require.ErrorIs(t, err, ErrUnavailable)
	require.False(t, passed)
	result, err := h.submit()
	require.NoError(t, err)
	passed, err = probe("INTERNAL_QA_PASSED")
	require.NoError(t, err)
	require.False(t, passed)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	passed, err = probe("INTERNAL_QA_PASSED")
	require.NoError(t, err)
	require.False(t, passed, "a Human check cannot satisfy internal Agent QA")
	passed, err = probe("EVERY_AC_HAS_PASS_EVIDENCE")
	require.NoError(t, err)
	require.True(t, passed)
	// The check on the first result cannot prove anything for another claim.
	missing := h.work + 10000
	row := h.stored()
	row.CurrentClaimID = &missing
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		passed, err := h.m.workflowFactOn(h.ctx, tx, row, 11, "EVIDENCE", "INTERNAL_QA_PASSED", "")
		require.False(t, passed)
		return err
	}))
}

// A workflow must use active canonical relationships, not the existence of an
// old assignment or a stale result reference.
func TestWorkflowAssignmentAndDependencyFacts(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	check := func(rule, kind string, actor guid.ID) bool {
		t.Helper()
		var passed bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			q := query.BindWorkItems(tx, m.service.names)
			row, err := q.WithContext(ctx).Where(q.ID.Eq(work.ID)).First()
			if err != nil {
				return err
			}
			passed, err = m.workflowFactOn(ctx, tx, row, actor, kind, rule, "")
			return err
		}))
		return passed
	}
	require.False(t, check("assignee", "REQUIRED_FIELDS", 11))
	require.False(t, check("EXECUTOR", "WORK_ITEM_ROLE", 11))
	require.False(t, check("HAS_PARENT", "RELATED_OBJECTS", 11))
	require.True(t, check("ALL_DEPENDENCIES_DONE", "RELATED_OBJECTS", 11))
	claim := assignmentClaim(t, m, db, ctx, work, false)
	require.True(t, check("assignee", "REQUIRED_FIELDS", 11))
	require.True(t, check("EXECUTOR", "WORK_ITEM_ROLE", 11))
	require.False(t, check("EXECUTOR", "WORK_ITEM_ROLE", 12))
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("id = ?", claim.Assignment.ID).Updates(map[string]any{"status": "RELEASED", "active_slot": nil, "ended_at": time.Now().UTC(), "end_reason": "TEST_RELEASE"}).Error)
	require.False(t, check("assignee", "REQUIRED_FIELDS", 11))
	require.False(t, check("EXECUTOR", "WORK_ITEM_ROLE", 11))
	// Use a separate canonical predecessor; only its acceptance satisfies the fact.
	_, _, _, body := workHTTPHarness(t)
	var predecessor workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		predecessor, err = m.createWorkOn(ctx, tx, 1, 99, 11, body)
		return err
	}))
	dep := model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, DependsOnID: predecessor.ID}
	require.NoError(t, db.Table(m.service.names.Dependencies.String()).Create(&dep).Error)
	require.False(t, check("ALL_DEPENDENCIES_DONE", "RELATED_OBJECTS", 11))
	require.NoError(t, db.Table(m.service.names.WorkItems.String()).Where("id = ?", predecessor.ID).Update("state", "ACCEPTED").Error)
	require.True(t, check("ALL_DEPENDENCIES_DONE", "RELATED_OBJECTS", 11))
}

func TestClaimAndDelegationFactsAreExactAndTransactionBound(t *testing.T) {
	h := newResultFixture(t)
	result, err := h.submit()
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		facts, found, err := h.m.claimEvaluationOn(h.ctx, tx, 1, 99, h.work, result.ID)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, result.ID, facts.ClaimID)
		require.Equal(t, h.work, facts.WorkItemID)
		require.Nil(t, facts.SelfConfidence, "Human results have no invented confidence")
		for _, ids := range [][4]guid.ID{{2, 99, h.work, result.ID}, {1, 98, h.work, result.ID}, {1, 99, h.work + 1, result.ID}, {1, 99, h.work, result.ID + 1}} {
			_, found, err = h.m.claimEvaluationOn(h.ctx, tx, ids[0], ids[1], ids[2], ids[3])
			require.NoError(t, err)
			require.False(t, found)
		}
		_, err = h.m.delegationProjectOn(h.ctx, tx, 1, 999)
		require.ErrorIs(t, err, governance.ErrNotFound)
		return nil
	}))
	_, _, err = h.m.claimEvaluationOn(h.ctx, h.db, 1, 99, h.work, result.ID)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = (&Module{}).claimEvaluationOn(h.ctx, h.db, 1, 99, h.work, result.ID)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = h.m.delegationProjectOn(h.ctx, h.db, 1, 99)
	require.ErrorIs(t, err, ErrInvalidArgument)
	for _, tc := range []struct {
		stage, mode, budget, holds string
		allowed                    bool
	}{
		{"ACTIVE", "AUTONOMOUS", "NORMAL", "[]", true},
		{"ACTIVE", "AUTONOMOUS", "NORMAL", "[{}]", false},
		{"ACTIVE", "AUTONOMOUS", "NORMAL", `[{"kind":"AUTONOMY_PRECONDITION"}]`, false},
		{"ACTIVE", "HYBRID", "NORMAL", "[]", false},
		{"CLOSED", "AUTONOMOUS", "NORMAL", "[]", false},
		{"CANCELLED", "AUTONOMOUS", "NORMAL", "[]", false}, //nolint:misspell // Frozen domain state.
	} {
		require.NoError(t, h.db.Table(h.m.service.names.Projects.String()).Where("project_id = ?", 99).Updates(map[string]any{"budget_cap_micros": 1000000, "stage": tc.stage, "team_mode": tc.mode, "budget_state": tc.budget, "holds": tc.holds}).Error)
		require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
			facts, err := h.m.delegationProjectOn(h.ctx, tx, 1, 99)
			require.NoError(t, err)
			require.Equal(t, tc.allowed, facts.DelegatedAllowed)
			return nil
		}))
	}
}
