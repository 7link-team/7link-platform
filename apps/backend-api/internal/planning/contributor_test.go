package planning

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/planning/model"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func contributorParent(t *testing.T, h harness) (PlanningRecord, PlanningRecord) {
	t.Helper()
	member := goalTree(t, h)
	var body PlanningGoalInput
	require.NoError(t, json.Unmarshal(member.Content, &body))
	var parent PlanningRecord
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		parent, e = h.m.readOn(claims(t, 2), tx, 1, *body.ParentGoalID, "goal")
		return e
	}))
	return parent, member
}

func TestGoalContributorsAssignRemoveFeedPlanValidationAndAuditRollback(t *testing.T) {
	h := setup(t)
	parent, _ := contributorParent(t, h)
	item := PlanningPlanItem{Title: "Contribute upstream", GoalID: parent.ID, KR: "delivery", Category: "LEARNING"}
	validate := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error { return h.m.validateItems(claims(t, 3), tx, 1, 3, 3, []PlanningPlanItem{item}) })
	}
	require.ErrorIs(t, validate(), ErrForbidden)
	change := func(action string, principal guid.ID, key string) (PlanningRecord, error) {
		var out PlanningRecord
		e := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.changeGoalOn(claims(t, 2), tx, 1, parent.ID, PlanningGoalChange{Version: parent.Version, ExpectedContentHash: parent.ContentHash, Action: action, PrincipalID: principal, Key: key})
			return e
		})
		if e == nil {
			parent = out
		}
		return out, e
	}
	_, e := change("ASSIGN_CONTRIBUTOR", 3, "delivery")
	require.NoError(t, e)
	require.NoError(t, validate())
	_, e = change("ASSIGN_CONTRIBUTOR", 3, "delivery")
	require.ErrorIs(t, e, ErrConflict)
	_, e = change("REMOVE_CONTRIBUTOR", 2, "delivery")
	require.ErrorIs(t, e, ErrConflict)
	_, e = change("ASSIGN_CONTRIBUTOR", 8, "delivery")
	require.ErrorIs(t, e, ErrForbidden)
	_, e = change("ASSIGN_CONTRIBUTOR", 9, "delivery")
	require.ErrorIs(t, e, ErrForbidden)
	_, e = change("ASSIGN_CONTRIBUTOR", 3, "unknown")
	require.ErrorIs(t, e, ErrInvalid)
	appender := h.m.audit
	h.m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit offline") }
	_, e = change("REMOVE_CONTRIBUTOR", 3, "delivery")
	require.ErrorContains(t, e, "audit offline")
	require.NoError(t, validate())
	h.m.audit = appender
	_, e = change("REMOVE_CONTRIBUTOR", 3, "delivery")
	require.NoError(t, e)
	require.ErrorIs(t, validate(), ErrForbidden)
	var n int64
	require.NoError(t, h.db.Table(h.m.names.Contributors.String()).Count(&n).Error)
	require.Zero(t, n)
}

func TestGoalAlignmentDerivesCoverageAndContributorsFromCurrentAuthorizedChildren(t *testing.T) {
	h := setup(t)
	parent, member := contributorParent(t, h)
	var alignment GoalAlignment
	read := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			alignment, e = h.m.goalAlignmentOn(claims(t, 2), tx, 1, parent.ID)
			return e
		})
	}
	require.NoError(t, read())
	require.NotNil(t, alignment.Coverage)
	require.InDelta(t, 1.0, *alignment.Coverage, 1e-12)
	require.Len(t, alignment.Children, 1)
	require.Contains(t, alignment.Contributors, GoalContributor{KR: "delivery", PrincipalID: 2, Source: "OWNER"})
	require.Contains(t, alignment.Contributors, GoalContributor{KR: "delivery", PrincipalID: 2, Source: "CHILD_GOAL", ChildGoalID: member.ID})
	// Canceled children immediately stop covering a KR and granting contributions.
	require.NoError(t, h.db.Table(h.m.names.Goals.String()).Where("tenant_id=? AND id=?", 1, member.ID).Update("state", goalCancelled).Error)
	require.NoError(t, read())
	require.Empty(t, alignment.Children)
	require.InDelta(t, 0.0, *alignment.Coverage, 1e-12)
	require.Equal(t, []string{"delivery"}, alignment.Uncovered)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		v, e := h.m.goalAlignmentOn(claims(t, 2), tx, 1, member.ID)
		require.Nil(t, v.Coverage)
		require.Empty(t, v.Contributors)
		return e
	}))
	// Exact project policy denial must not expose the parent or its descendants.
	h.m.entityPolicy = core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	})
	require.ErrorIs(t, read(), ErrForbidden)
}

func TestContributorMutationsRequireCurrentHumanCASAndPlatformReplay(t *testing.T) {
	h := setup(t)
	parent, _ := contributorParent(t, h)
	in := PlanningGoalChange{Version: parent.Version, ExpectedContentHash: parent.ContentHash, Action: "ASSIGN_CONTRIBUTOR", PrincipalID: 3, Key: "delivery"}
	for _, actor := range []guid.ID{6, 7, 8, 9} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.changeGoalOn(claims(t, actor), tx, 1, parent.ID, in); return e }))
	}
	stale := in
	stale.Version = 99
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := h.m.changeGoalOn(claims(t, 2), tx, 1, parent.ID, stale); return e }), ErrConflict)
	call := func() (*PlanningCommandOutput, error) {
		return h.m.command(claims(t, 2), 1, "contribution-key", "goal.change", "/goals/"+parent.ID.String()+"/changes", in, func(tx *gorm.DB) (PlanningRecord, error) { return h.m.changeGoalOn(claims(t, 2), tx, 1, parent.ID, in) })
	}
	a, e := call()
	require.NoError(t, e)
	b, e := call()
	require.NoError(t, e)
	require.Equal(t, "true", b.Replayed)
	require.Equal(t, a.Body.Data.ID, b.Body.Data.ID)
	var rows []model.Contributor
	require.NoError(t, h.db.Table(h.m.names.Contributors.String()).Find(&rows).Error)
	require.Len(t, rows, 1)
	policy := h.m.entityPolicy
	h.m.entityPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = call()
	require.Error(t, e)
	h.m.entityPolicy = policy
}
