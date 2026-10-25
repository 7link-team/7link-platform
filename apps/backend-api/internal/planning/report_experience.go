package planning

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/knowledge"
	"7link.com/platform/internal/planning/model"
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
)

func (m *Module) proposeReportExperiencesOn(ctx context.Context, tx *gorm.DB, r *model.Report) error {
	if r.State != "CONFIRMED" {
		return ErrConflict
	}
	var body PlanningReportInput
	if err := json.Unmarshal([]byte(r.Content), &body); err != nil {
		return err
	}
	for _, item := range body.ImprovementPlan {
		if !item.ProposeMemory {
			continue
		}
		if m.reportExperiences.ProposeOn == nil {
			return ErrUnavailable
		}
		scope := knowledge.Scope{Scope: "PROJECT"}
		project := r.ProjectID
		if item.MemoryProjectID != nil {
			project = *item.MemoryProjectID
		}
		if r.ProjectID > 0 && project != r.ProjectID {
			return ErrInvalid
		}
		if r.PrincipalID > 0 {
			if m.agents.ByPrincipalOn == nil {
				return ErrUnavailable
			}
			agent, found, err := m.agents.ByPrincipalOn(ctx, tx, r.TenantID, r.PrincipalID)
			if err != nil {
				return err
			}
			if found && agent.PrincipalID == r.PrincipalID {
				scope = knowledge.Scope{Scope: "AGENT", AgentPrincipalID: &r.PrincipalID}
			}
		}
		if scope.Scope == "PROJECT" {
			if project <= guid.ID(0) {
				return ErrInvalid
			}
			if _, err := m.authorize(ctx, tx, r.TenantID, project, 0, "report_read", false); err != nil {
				return err
			}
			scope.ProjectID = &project
		}
		if _, err := m.reportExperiences.ProposeOn(ctx, tx, r.TenantID, r.ID, scope, item.Action); err != nil {
			if errors.Is(err, knowledge.ErrForbidden) {
				return ErrForbidden
			}
			if errors.Is(err, knowledge.ErrInvalid) {
				return ErrInvalid
			}
			if errors.Is(err, knowledge.ErrUnavailable) {
				return ErrUnavailable
			}
			return err
		}
	}
	return nil
}
