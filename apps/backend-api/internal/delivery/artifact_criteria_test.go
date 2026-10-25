package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestExecutionArtifactProviderFailsClosedForUnusableFrozenPlans(t *testing.T) {
	h, l := executionWorkFixture(t)
	s := module.NewServices()
	require.NoError(t, h.m.provideJArtifactCriteria(s))
	require.NoError(t, s.Seal())
	p, err := module.Resolve[ExecutionArtifactCriteria](s)
	require.NoError(t, err)
	require.ErrorIs(t, p(h.ctx, h.db, l, []guid.ID{1}), ErrInvalidArgument)
	for _, ids := range [][]guid.ID{nil, make([]guid.ID, 51)} {
		require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return p(h.ctx, tx, l, ids) }), ErrInvalidArgument)
	}
	w := query.BindWorkItems(h.db, h.m.service.names)
	_, err = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Update(w.VerificationPlan, `["invalid-step"]`)
	require.NoError(t, err)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return p(h.ctx, tx, l, []guid.ID{1}) }), ErrPlanNotApproved)
	_, err = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Update(w.VerificationPlan, "[]")
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("ALTER TABLE "+h.m.service.names.WorkCriteria.String()+" RENAME TO unavailable_work_criteria").Error)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { return p(h.ctx, tx, l, []guid.ID{1}) }))
}

func TestExecutionArtifactsRequireCurrentWorkAndFrozenVerificationCriterion(t *testing.T) {
	h, l := executionWorkFixture(t)
	c := query.BindWorkCriteria(h.db, h.m.service.names)
	criterion, err := c.WithContext(h.ctx).First()
	require.NoError(t, err)
	plan, err := json.Marshal([]workVerificationStep{{CriterionID: criterion.ID, Method: "COMMAND", Detail: "Inspect actual output"}})
	require.NoError(t, err)
	w := query.BindWorkItems(h.db, h.m.service.names)
	_, err = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Update(w.VerificationPlan, string(plan))
	require.NoError(t, err)
	approveFixtureReviewPlan(t, h)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.executionArtifactCriteriaOn(h.ctx, tx, l, []guid.ID{criterion.ID}) }))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.executionArtifactCriteriaOn(h.ctx, tx, l, []guid.ID{criterion.ID + 999})
	}))
	_, err = w.WithContext(h.ctx).Where(w.ID.Eq(h.work)).Update(w.VerificationPlan, "[]")
	require.NoError(t, err)
	approveFixtureReviewPlan(t, h)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.executionArtifactCriteriaOn(h.ctx, tx, l, []guid.ID{criterion.ID}) }), ErrInvalidArgument)
	other := l
	other.WorkItemID++
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.m.executionArtifactCriteriaOn(h.ctx, tx, other, []guid.ID{criterion.ID})
	}))
}
