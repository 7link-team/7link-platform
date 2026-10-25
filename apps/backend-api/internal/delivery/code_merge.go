package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CodeMergeGuard owns current acceptance and authorization. Execution never
// infers these facts from its historical successful process receipt.
type CodeMergeGuard func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID) (CodeMergeFacts, error)
type CodeMergeFacts struct{ HeadCommit, DefaultBranch, AuthoritySource, ActorKind string }

func (m *Module) provideCodeMerge(s *module.Services) error {
	if err := module.Provide[DefectMergeConnector](s, DefectMergeConnector{Bind: func(reader DefectMergeReader) error {
		if reader == nil || m.defectMerge != nil {
			return ErrInvalidArgument
		}
		m.defectMerge = reader
		return nil
	}}); err != nil {
		return err
	}
	return module.Provide[CodeMergeGuard](s, m.codeMergeOn)
}
func (m *Module) codeMergeOn(ctx context.Context, tx *gorm.DB, tenant, project, workID, execution, repository, producer guid.ID) (CodeMergeFacts, error) {
	var out CodeMergeFacts
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tenant <= 0 || project <= 0 || workID <= 0 || execution <= 0 || repository <= 0 || producer <= 0 || actor <= 0 || authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project {
		return out, ErrInvalidArgument
	}
	w := query.BindWorkItems(tx, m.service.names)
	snapshot, err := w.WithContext(ctx).Where(w.TenantID.Eq(tenant), w.ProjectID.Eq(project), w.ID.Eq(workID), w.ArchivedAt.IsNull()).First()
	if err != nil {
		return out, err
	}
	_, work, _, err := m.lockedWorkActorOn(ctx, tx, tenant, project, workID, actor, snapshot.AccountablePrincipalID, "code_merge", true)
	if err != nil {
		return out, err
	}
	kind, err := m.resultActorKindOn(ctx, tx, actor, "")
	if err != nil {
		return out, err
	}
	preAcceptance := (work.Type == "BUG" || work.Type == "INCIDENT") && work.State == "VERIFYING"
	if (!preAcceptance && work.State != "ACCEPTED") || work.CurrentClaimID == nil || *work.CurrentClaimID != execution || (!preAcceptance && (work.AcceptedDecisionID == nil || work.AcceptedAt == nil)) || work.PausedAt != nil {
		return out, ErrResultState
	}
	row, err := m.resultOn(ctx, tx, tenant, project, workID, execution)
	if err != nil {
		return out, err
	}
	if row.SourceExecutionID == nil || *row.SourceExecutionID != execution || row.HeadCommit == "" || work.BaselineStale || row.BaselineVersionNo != work.BaselineVersionNo {
		return out, ErrResultCoverage
	}
	if err = m.requireCurrentUpstreamOn(ctx, tx, work); err != nil {
		return out, err
	}
	if err = m.requireNoAndonOn(ctx, tx, work); err != nil {
		return out, err
	}
	if !preAcceptance {
		// Require real passing receipts for the frozen required verification plan;
		// Human force-valid records cannot turn a failed process into a pass.
		if err = m.verificationGateOn(ctx, tx, work, row, false); err != nil {
			return out, err
		}
		runs, err := m.verificationRunsOn(ctx, tx, row)
		if err != nil {
			return out, err
		}
		seen := map[string]bool{}
		for _, run := range runs {
			key := run.CriterionID.String() + ":" + run.Method + ":" + run.Stage
			if run.Method == "HUMAN" {
				key += ":" + run.ActorID.String()
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if run.State != "DONE" || run.Verdict != "PASSED" || (run.Method != "HUMAN" && (run.TaskID == nil || len(run.ReceiptHash) != 64)) || (run.Method == "HUMAN" && run.CheckID == nil) {
				return out, ErrVerificationPending
			}
		}
		if len(seen) == 0 {
			return out, ErrVerificationPending
		}
	} else if err = m.verificationStageReadyOn(ctx, tx, work, row, "AGENT_TEST"); err != nil {
		return out, err
	}
	r := query.BindRepositories(tx, m.service.names)
	repo, err := r.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(r.TenantID.Eq(tenant), r.ProjectID.Eq(project), r.ID.Eq(repository), r.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, ErrResultState
	}
	if err != nil {
		return out, err
	}
	if m.deliveryAuthority == nil {
		return out, ErrUnavailable
	}
	var cost int64
	if kind == "AGENT" {
		if m.acceptanceExecutions == nil || m.acceptanceSettlements == nil {
			return out, ErrUnavailable
		}
		ids, e := m.acceptanceExecutions(ctx, tx, tenant, project, workID)
		if e != nil {
			return out, e
		}
		value, known, e := m.acceptanceSettlements(ctx, tx, tenant, project, ids)
		if e != nil {
			return out, e
		}
		if !known || value < 0 {
			return out, ErrUnavailable
		}
		cost = value
	}
	decision, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: tenant, ProjectID: project, PrincipalID: actor, ActorKind: kind, Code: "code_merge", Resource: governance.ResourceRef{Kind: "work_item", ID: workID}, Facts: &governance.WorkItemFacts{ClaimID: row.ID, Type: work.Type, Risk: work.Risk, CostMicros: cost, QualityReworkCount: work.QualityReworkCount, SelfProduced: producer == actor || row.ActorID == actor, RequiredVerificationsPassed: true, RequireHumanDecision: work.RequireHumanDecision, Paused: false}})
	if err != nil {
		return out, err
	}
	if !decision.Allowed || decision.AuthoritySource == "" {
		return out, ErrWorkForbidden
	}
	if kind == "AGENT" && (decision.DelegationID <= 0 || decision.AuthoritySource != "DELEGATION:"+decision.DelegationID.String()) {
		return out, ErrWorkForbidden
	}
	return CodeMergeFacts{HeadCommit: row.HeadCommit, DefaultBranch: repo.DefaultBranch, AuthoritySource: decision.AuthoritySource, ActorKind: kind}, nil
}
