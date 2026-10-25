package delivery

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ExecutionArtifactCriteria validates a Human's explicit evidence associations
// against the currently authorized work and its frozen verification plan.
// It does not produce coverage, observations, or verification verdicts.
type ExecutionArtifactCriteria func(context.Context, *gorm.DB, ExecutionWorkLink, []guid.ID) error

func (m *Module) provideJArtifactCriteria(s *module.Services) error {
	return module.Provide[ExecutionArtifactCriteria](s, m.executionArtifactCriteriaOn)
}

func (m *Module) executionArtifactCriteriaOn(ctx context.Context, tx *gorm.DB, l ExecutionWorkLink, ids []guid.ID) error {
	work, _, err := m.executionWorkOn(ctx, tx, l, "CREATE")
	if err != nil {
		return err
	}
	if len(ids) == 0 || len(ids) > 50 {
		return ErrInvalidArgument
	}
	var plan []workVerificationStep
	if json.Unmarshal([]byte(work.VerificationPlan), &plan) != nil {
		return ErrUnavailable
	}
	planned := map[guid.ID]bool{}
	for _, step := range plan {
		planned[step.CriterionID] = true
	}
	c := query.BindWorkCriteria(tx, m.service.names)
	criteria, err := c.WithContext(ctx).Where(c.TenantID.Eq(l.TenantID), c.ProjectID.Eq(l.ProjectID), c.WorkItemID.Eq(l.WorkItemID)).Find()
	if err != nil {
		return err
	}
	owned := map[guid.ID]bool{}
	for _, criterion := range criteria {
		owned[criterion.ID] = true
	}
	for _, id := range ids {
		if id <= 0 || !owned[id] || !planned[id] {
			return ErrInvalidArgument
		}
	}
	return nil
}
