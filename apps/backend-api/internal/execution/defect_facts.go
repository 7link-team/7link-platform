package execution

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
)

func (m *Module) defectMergeOn(ctx context.Context, tx *gorm.DB, tenant, project, work, execution guid.ID, commit string) (delivery.DefectMergeFact, error) {
	var out delivery.DefectMergeFact
	if transaction(tx, tenant, execution) != nil || !commitHash(commit) {
		return out, ErrInvalidArgument
	}
	source, err := m.rowOn(ctx, tx, tenant, execution, false)
	if err != nil {
		return out, err
	}
	if source.ProjectID != project || source.WorkItemID != work || source.State != "SUCCEEDED" {
		return out, ErrConflict
	}
	q := query.Use(tx).Merge.Table(m.names.Merges.String())
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ExecutionID.Eq(execution), q.State.Eq("MERGED")).Order(q.Attempt.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, delivery.ErrVerificationPending
	}
	if err != nil {
		return out, err
	}
	p, err := decodeMerge(r)
	if err != nil {
		return out, err
	}
	v, err := mergeView(r)
	if err != nil {
		return out, err
	}
	if v.Observation == nil || r.SessionID <= 0 || r.ConnEpoch < 1 || r.FinishedAt == nil || p.SourceCommit != commit || v.Observation.SourceCommit != commit || !v.Observation.Contained || !validMergeSignature(p, *v.Observation) || !commitHash(v.Observation.MergeCommit) {
		return out, delivery.ErrVerificationPending
	}
	return delivery.DefectMergeFact{MergeID: r.ID, SourceCommit: commit, MergeCommit: v.Observation.MergeCommit, TargetBranch: p.TargetBranch, ReceiptHash: r.ReceiptHash}, nil
}
