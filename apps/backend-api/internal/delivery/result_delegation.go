package delivery

import (
	"context"
	"strings"

	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

type AcceptanceExecutionIDs func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error)
type AcceptanceSettlements func(context.Context, *gorm.DB, guid.ID, guid.ID, []guid.ID) (int64, bool, error)
type AcceptanceCostConnector struct {
	BindExecutions  func(AcceptanceExecutionIDs) error
	BindSettlements func(AcceptanceSettlements) error
}

func (m *Module) provideAcceptanceCost(s *module.Services) error {
	if err := m.provideProjectMetrics(s); err != nil {
		return err
	}
	return module.Provide[AcceptanceCostConnector](s, AcceptanceCostConnector{
		BindExecutions: func(f AcceptanceExecutionIDs) error {
			if f == nil || m.acceptanceExecutions != nil {
				return ErrInvalidArgument
			}
			m.acceptanceExecutions = f
			return nil
		},
		BindSettlements: func(f AcceptanceSettlements) error {
			if f == nil || m.acceptanceSettlements != nil {
				return ErrInvalidArgument
			}
			m.acceptanceSettlements = f
			return nil
		},
	})
}

func (m *Module) resultAcceptanceAuthorityOn(ctx context.Context, tx *gorm.DB, work *model.WorkItem, row *model.ResultVersion, actor guid.ID) (string, error) {
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return "", err
	}
	if active && human.ID == actor {
		if isRuntimeDefect(work, row) {
			if err = m.defectVerifierOn(ctx, tx, work, actor); err != nil {
				return "", err
			}
		}
		return m.resultAuthorityOn(ctx, tx, work, row, actor, "decision_approve")
	}
	if work.Type == "BUG" || work.Type == "INCIDENT" {
		return "", ErrDefectAgentAccept
	}
	if work.RequireHumanDecision {
		return "", ErrWorkForbidden
	}
	if m.deliveryAuthority == nil || m.acceptanceExecutions == nil || m.acceptanceSettlements == nil {
		return "", ErrUnavailable
	}
	// No override turns a failed verification into a passing fact for an Agent.
	if err = m.resultAcceptanceReadyOn(ctx, tx, work, row); err != nil {
		return "", err
	}
	runs, err := m.verificationRunsOn(ctx, tx, row)
	if err != nil {
		return "", err
	}
	latest := map[string]bool{}
	for _, run := range runs {
		key := run.CriterionID.String() + ":" + run.Method + ":" + run.Stage
		if run.Method == "HUMAN" {
			key += ":" + run.ActorID.String()
		}
		if latest[key] {
			continue
		}
		latest[key] = true
		if run.State != "DONE" || run.Verdict != "PASSED" {
			return "", ErrVerificationPending
		}
	}
	if len(latest) == 0 {
		return "", ErrVerificationPending
	}
	ids, err := m.acceptanceExecutions(ctx, tx, work.TenantID, work.ProjectID, work.ID)
	if err != nil {
		return "", err
	}
	cost, known, err := m.acceptanceSettlements(ctx, tx, work.TenantID, work.ProjectID, ids)
	if err != nil {
		return "", err
	}
	if !known || cost < 0 {
		return "", ErrUnavailable
	}
	a, err := m.deliveryAuthority(ctx, tx, governance.AuthorityRequest{TenantID: work.TenantID, ProjectID: work.ProjectID, PrincipalID: actor, ActorKind: "AGENT", Code: "decision_approve", Resource: governance.ResourceRef{Kind: "work_item", ID: work.ID}, Facts: &governance.WorkItemFacts{ClaimID: row.ID, Type: work.Type, Risk: work.Risk, CostMicros: cost, QualityReworkCount: work.QualityReworkCount, SelfProduced: row.ActorID == actor, RequiredVerificationsPassed: true, RequireHumanDecision: work.RequireHumanDecision, Paused: work.PausedAt != nil}})
	if err != nil {
		return "", err
	}
	if !a.Allowed || a.DelegationID <= 0 || a.AuthoritySource != "DELEGATION:"+a.DelegationID.String() {
		return "", ErrWorkForbidden
	}
	return a.AuthoritySource, nil
}

func (m *Module) resultActorKindOn(ctx context.Context, tx *gorm.DB, actor guid.ID, source string) (string, error) {
	if actor == 0 {
		return "SYSTEM", nil
	}
	if strings.HasPrefix(source, "DELEGATION:") {
		return "AGENT", nil
	}
	human, active, err := m.principals.ActiveHumanByID(ctx, tx, actor)
	if err != nil {
		return "", err
	}
	if active && human.ID == actor {
		return "HUMAN", nil
	}
	if m.agents.ByPrincipalOn == nil {
		return "", ErrUnavailable
	}
	a, found, err := m.agents.ByPrincipalOn(ctx, tx, authn.TenantIDFromContext(ctx), actor)
	if err != nil {
		return "", err
	}
	if !found || a.State != "ACTIVE" || a.PrincipalID != actor {
		return "", ErrInvalidMember
	}
	return "AGENT", nil
}
