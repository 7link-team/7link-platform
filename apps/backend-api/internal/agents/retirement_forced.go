package agents

import (
	"context"
	"fmt"

	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"gorm.io/gorm"
)

func (m *Module) saveForcedRetirementSummaryOn(ctx context.Context, tx *gorm.DB, r *model.Retirement, summary string) error {
	p := r.Precheck.Progress
	if p == nil {
		p = &model.RetirementProgress{}
	}
	p.HandoverErrorCode = "SUMMARY_UNAVAILABLE"
	r.Precheck.Progress = p
	if summary != "" && m.retirementHandover.SystemOn != nil {
		content := fmt.Sprintf("Forced retirement. Open work items: %d; running executions: %d. Runtime outcomes are not confirmed by retirement.\n\n%s", len(r.Precheck.WorkItems), len(r.Precheck.Executions), summary)
		err := tx.Transaction(func(artifactTx *gorm.DB) error {
			artifact, err := m.retirementHandover.SystemOn(ctx, artifactTx, r.TenantID, r.AgentPrincipalID, r.ID, content)
			if err != nil {
				return err
			}
			if artifact.ID <= 0 || artifact.Source != "SYSTEM" || artifact.ExecutionID != nil || artifact.Content == "" || artifact.SHA256 == "" {
				return ErrUnavailable
			}
			p.HandoverArtifactID, p.HandoverSource, p.HandoverErrorCode = &artifact.ID, "SYSTEM", ""
			return nil
		})
		if err != nil {
			p.HandoverArtifactID, p.HandoverSource, p.HandoverErrorCode = nil, "", "ARTIFACT_UNAVAILABLE"
		}
	}
	q := query.BindRetirement(tx, m.names)
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(r.TenantID), q.ID.Eq(r.ID), q.Version.Eq(r.Version)).Select(q.Precheck).Updates(r)
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
