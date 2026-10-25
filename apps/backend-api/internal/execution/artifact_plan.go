package execution

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type PlannedArtifact struct {
	CriterionID guid.ID `json:"criterion_id"`
	Path        string  `json:"path" minLength:"1" maxLength:"512" doc:"Repository-relative slash path of a regular file in the final commit."`
	Note        string  `json:"note" minLength:"1" maxLength:"1000"`
}

func plannedArtifactNote(a PlannedArtifact) string {
	return a.Note + " Source: final execution commit; criterion association is not a verification verdict."
}
func plannedArtifactTitle(i int) string { return fmt.Sprintf("Planned artifact %d", i+1) }

func validateArtifactReceipt(plan ExecutionPlan, in ReceiptInput) error {
	if len(plan.Artifacts) == 0 || in.Upload == nil || in.Upload.CriterionID == nil {
		return nil
	}
	for i, a := range plan.Artifacts {
		if *in.Upload.CriterionID == a.CriterionID && in.Upload.Name == path.Base(a.Path) && in.Upload.Note == plannedArtifactNote(a) && in.Upload.Coverage == "COVERED" && in.EvidenceTitle == plannedArtifactTitle(i) {
			return nil
		}
	}
	return ErrInvalidArgument
}

func validateArtifactCompletion(plan ExecutionPlan, facts []EvidenceFact) error {
	for i, a := range plan.Artifacts {
		found := false
		for _, f := range facts {
			if f.Kind == "FILE" && f.Title == plannedArtifactTitle(i) && f.Artifact != nil && f.Artifact.CriterionID != nil && *f.Artifact.CriterionID == a.CriterionID && f.Artifact.Coverage == "COVERED" && f.Artifact.Note == plannedArtifactNote(a) {
				found = true
				break
			}
		}
		if !found {
			return ErrConflict
		}
	}
	return nil
}

func (m *Module) injectArtifactCriteria(s *module.Services) error {
	p, err := module.Resolve[delivery.ExecutionArtifactCriteria](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	} // Declared artifacts fail closed below.
	if err != nil {
		return err
	}
	m.work.ArtifactCriteriaOn = func(ctx context.Context, tx *gorm.DB, l WorkLink, ids []guid.ID) error {
		return p(ctx, tx, delivery.ExecutionWorkLink{TenantID: l.TenantID, ProjectID: l.ProjectID, WorkItemID: l.WorkItemID, AssignmentID: l.AssignmentID, ExecutionID: l.ExecutionID, ActorID: l.ActorID, InitiatorID: l.InitiatorID, ParentExecutionID: l.ParentExecutionID, SourceDecisionID: l.SourceDecisionID}, ids)
	}
	return nil
}

func (m *Module) validatePlannedArtifactsOn(ctx context.Context, tx *gorm.DB, l WorkLink, artifacts []PlannedArtifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	if len(artifacts) > 50 {
		return ErrInvalidArgument
	}
	ids := make([]guid.ID, 0, len(artifacts))
	seen := map[string]bool{}
	for _, a := range artifacts {
		key := a.CriterionID.String() + ":" + a.Path
		if a.CriterionID <= 0 || !fs.ValidPath(a.Path) || a.Path == "." || len(a.Path) > 512 || len(path.Base(a.Path)) > 200 || strings.ContainsAny(a.Path, "\\:\x00") || strings.EqualFold(strings.Split(a.Path, "/")[0], ".git") || !bounded(a.Note, 1000) || seen[key] {
			return ErrInvalidArgument
		}
		seen[key] = true
		ids = append(ids, a.CriterionID)
	}
	if m.work.ArtifactCriteriaOn == nil {
		return ErrCapabilityUnavailable
	}
	return m.work.ArtifactCriteriaOn(ctx, tx, l, ids)
}
