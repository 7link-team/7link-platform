package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"context"
	"gorm.io/gorm"
	"time"
)

// DefectMergeReader reads immutable, machine-authenticated containment facts.
// Callers authorize and lock the exact work/result before invoking it.
type DefectMergeReader func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, string) (DefectMergeFact, error)
type DefectMergeFact struct {
	MergeID                                              guid.ID
	SourceCommit, MergeCommit, TargetBranch, ReceiptHash string
}
type DefectMergeConnector struct{ Bind func(DefectMergeReader) error }

func (m *Module) defectRoleOn(ctx context.Context, tx *gorm.DB, tenant, project, actor guid.ID, key string) error {
	if m.settingsMembers.ListOn == nil || m.roles.RoleIDOn == nil {
		return ErrUnavailable
	}
	role, err := m.roles.RoleIDOn(ctx, tx, tenant, key)
	if err != nil {
		return err
	}
	bindings, err := m.settingsMembers.ListOn(ctx, tx, tenant, project)
	if err != nil {
		return err
	}
	allowed := false
	for _, b := range bindings {
		if b.PrincipalID != actor || b.RoleID != role || (!b.ExpiresAt.IsZero() && !b.ExpiresAt.After(time.Now())) {
			continue
		}
		if string(b.Effect) == "deny" {
			return ErrWorkForbidden
		}
		if string(b.Effect) == "allow" {
			allowed = true
		}
	}
	if !allowed {
		return ErrWorkForbidden
	}
	return nil
}
func (m *Module) defectVerifierOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, actor guid.ID) error {
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !active || human.ID != actor {
		return ErrDefectAgentAccept
	}
	if actor == work.AccountablePrincipalID {
		return nil
	}
	return m.defectRoleOn(ctx, tx, work.TenantID, work.ProjectID, actor, "tester")
}

func isRuntimeDefect(work *model.WorkItem, result *model.ResultVersion) bool {
	return (work.Type == "BUG" || work.Type == "INCIDENT") && result.SourceExecutionID != nil && result.HeadCommit != ""
}

func (m *Module) trustedDefectMergeOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, result *model.ResultVersion) (DefectMergeFact, error) {
	if !isRuntimeDefect(work, result) || m.defectMerge == nil {
		return DefectMergeFact{}, ErrUnavailable
	}
	return m.defectMerge(ctx, tx, work.TenantID, work.ProjectID, work.ID, *result.SourceExecutionID, result.HeadCommit)
}

func (m *Module) defectDeploymentReadyOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, result *model.ResultVersion) error {
	if !isRuntimeDefect(work, result) {
		return nil
	}
	if work.DefectStage == nil || (*work.DefectStage != "DEPLOYED" && *work.DefectStage != "VERIFIED" && *work.DefectStage != "CLOSED") || work.FixedVersion == nil || *work.FixedVersion == "" {
		return ErrVerificationPending
	}
	fact, err := m.trustedDefectMergeOn(ctx, tx, work, result)
	if err != nil {
		return err
	}
	d := query.BindDeployments(tx, m.service.names)
	n, err := d.WithContext(ctx).Where(d.TenantID.Eq(work.TenantID), d.ProjectID.Eq(work.ProjectID), d.WorkItemID.Eq(work.ID), d.ResultID.Eq(result.ID), d.Commit.Eq(result.HeadCommit), d.MergeReceiptHash.Eq(fact.ReceiptHash)).Count()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrVerificationPending
	}
	return nil
}
