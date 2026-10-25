package execution

import (
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

// Connect capabilities in the owning module; neither owner depends on the
// consumer module. Closures resolve after all providers have registered.
type executionDeliveryAdapter struct {
	enabled     bool
	work        delivery.ExecutionWork
	budget      budget.Provider
	assignments delivery.LoopAssignments
}

func (*executionDeliveryAdapter) ModuleName() string { return DeliveryBridgeName }
func (m *executionDeliveryAdapter) Enabled() bool    { return m.enabled }
func (*executionDeliveryAdapter) ModuleDependencies() []string {
	return []string{delivery.Name, budget.Name, fleet.Name, governance.Name, core.Name}
}
func (m *executionDeliveryAdapter) InjectServices(s *module.Services) error {
	if err := bindFleetAgentRoles(s); err != nil {
		return err
	}
	var err error
	if m.work, err = module.Resolve[delivery.ExecutionWork](s); err != nil {
		return err
	}
	if m.budget, err = module.Resolve[budget.Provider](s); err != nil {
		return err
	}
	if m.assignments, err = module.Resolve[delivery.LoopAssignments](s); err != nil {
		return err
	}
	connector, err := module.Resolve[fleet.RepositoryConnector](s)
	if err != nil {
		return err
	}
	repository, err := module.Resolve[fleet.RepositoryAuthorizer](s)
	if err != nil {
		return err
	}
	return connector.Bind(repository)
}
func executionLink(l WorkLink) delivery.ExecutionWorkLink {
	return delivery.ExecutionWorkLink{ReviewPlanID: l.ReviewPlanID, TenantID: l.TenantID, ProjectID: l.ProjectID, WorkItemID: l.WorkItemID, AssignmentID: l.AssignmentID, ExecutionID: l.ExecutionID, ActorID: l.ActorID, InitiatorID: l.InitiatorID, ParentExecutionID: l.ParentExecutionID, SourceDecisionID: l.SourceDecisionID, SourceHeadCommit: l.SourceHeadCommit, SourceFeedback: l.SourceFeedback}
}
func executionBridgeError(err error) error {
	switch {
	case errors.Is(err, delivery.ErrWorkForbidden), errors.Is(err, delivery.ErrInvalidMember), errors.Is(err, delivery.ErrAssignmentHolder):
		return ErrForbidden
	case errors.Is(err, delivery.ErrProjectNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return ErrNotFound
	case errors.Is(err, delivery.ErrInvalidArgument):
		return ErrInvalidArgument
	case errors.Is(err, delivery.ErrAssignmentState), errors.Is(err, delivery.ErrAssignmentHeld), errors.Is(err, delivery.ErrWorkProjectReadOnly), errors.Is(err, delivery.ErrWorkVersion), errors.Is(err, delivery.ErrResultState):
		return ErrConflict
	case errors.Is(err, budget.ErrExceeded):
		return ErrBudgetExceeded
	case errors.Is(err, budget.ErrPriceUnknown):
		return ErrPriceUnknown
	default:
		return err
	}
}
func (m *executionDeliveryAdapter) ProvideServices(s *module.Services) error {
	return module.Provide[WorkItems](s, WorkItems{
		ProgressStopOn: func(ctx context.Context, tx *gorm.DB, l WorkLink, reason string) error {
			if m.work.ProgressStopOn == nil {
				return ErrCapabilityUnavailable
			}
			return executionBridgeError(m.work.ProgressStopOn(ctx, tx, executionLink(l), reason))
		},
		SettleBudgetOn: m.settleExecutionBudgetOn,
		AuthorizeVisualReviewOn: func(ctx context.Context, tx *gorm.DB, l WorkLink, result guid.ID, version int, criterion guid.ID, head string) error {
			if m.work.AuthorizeVisualReviewOn == nil {
				return ErrCapabilityUnavailable
			}
			return executionBridgeError(m.work.AuthorizeVisualReviewOn(ctx, tx, executionLink(l), result, version, criterion, head))
		},
		AssignedAgentOn: func(ctx context.Context, tx *gorm.DB, l WorkLink) (guid.ID, error) {
			if m.assignments.AgentsOn == nil {
				return 0, nil
			}
			agents, err := m.assignments.AgentsOn(ctx, tx, l.TenantID, l.ProjectID, []guid.ID{l.AssignmentID})
			return agents[l.AssignmentID], executionBridgeError(err)
		},
		PrincipalsOn: func(ctx context.Context, tx *gorm.DB, l WorkLink) ([]guid.ID, error) {
			if m.work.PrincipalsOn == nil {
				return nil, ErrCapabilityUnavailable
			}
			ids, err := m.work.PrincipalsOn(ctx, tx, executionLink(l))
			return ids, executionBridgeError(err)
		},
		AuthorizeOn: func(ctx context.Context, tx *gorm.DB, l WorkLink, op string) error {
			if m.work.AuthorizeOn == nil {
				return ErrCapabilityUnavailable
			}
			return executionBridgeError(m.work.AuthorizeOn(ctx, tx, executionLink(l), op))
		},
		StartedOn: func(ctx context.Context, tx *gorm.DB, l WorkLink) error {
			if m.work.StartedOn == nil {
				return ErrCapabilityUnavailable
			}
			return executionBridgeError(m.work.StartedOn(ctx, tx, executionLink(l)))
		},
		FinishedOn: func(ctx context.Context, tx *gorm.DB, l WorkLink, r WorkResult) error {
			if m.work.FinishedOn == nil {
				return ErrCapabilityUnavailable
			}
			evidence := make([]delivery.ExecutionEvidence, len(r.Evidence))
			for i, e := range r.Evidence {
				evidence[i] = delivery.ExecutionEvidence{ID: e.ID, Kind: e.Kind, Title: e.Title, ContentHash: e.ContentHash}
				if a := e.Artifact; a != nil {
					evidence[i].Artifact = &delivery.ExecutionArtifact{BindingID: a.BindingID, SpaceID: a.SpaceID, FileID: a.FileID, VersionID: a.VersionID, FileVersion: a.FileVersion, CriterionID: a.CriterionID, Coverage: a.Coverage, Note: a.Note, SHA256: a.SHA256}
				}
			}
			if err := m.work.FinishedOn(ctx, tx, executionLink(l), delivery.ExecutionCompletion{State: r.State, Reason: r.Reason, HeadCommit: r.HeadCommit, Evidence: evidence}); err != nil {
				return executionBridgeError(err)
			}
			return m.settleExecutionBudgetOn(ctx, tx, l, r)
		},
		BudgetOn: func(ctx context.Context, tx *gorm.DB, l WorkLink) (BudgetFact, error) {
			if m.budget.FactOn == nil {
				return BudgetFact{}, ErrCapabilityUnavailable
			}
			f, err := m.budget.FactOn(ctx, tx, l.TenantID, l.ProjectID)
			return BudgetFact{CapMicros: f.CapMicros, SpentMicros: f.SpentMicros, ReservedMicros: f.ReservedMicros}, executionBridgeError(err)
		},
		ReserveBudgetOn: func(ctx context.Context, tx *gorm.DB, l WorkLink, cost int64, known bool) error {
			if m.budget.ReserveOn == nil {
				return ErrCapabilityUnavailable
			}
			return executionBridgeError(m.budget.ReserveOn(ctx, tx, l.TenantID, l.ProjectID, "EXECUTION", l.ExecutionID, cost, known))
		},
	})
}

func (m *executionDeliveryAdapter) settleExecutionBudgetOn(ctx context.Context, tx *gorm.DB, l WorkLink, r WorkResult) error {
	// An unleased cancellation is the only terminal outcome with proven zero
	// usage. Unknown runtime usage keeps its reservation until reconciled.
	if r.ActualMicros != nil {
		if m.budget.SettleOn == nil {
			return ErrCapabilityUnavailable
		}
		return executionBridgeError(m.budget.SettleOn(ctx, tx, l.TenantID, l.ProjectID, "EXECUTION", l.ExecutionID, *r.ActualMicros, true))
	}
	if r.Unused {
		if m.budget.SettleOn == nil {
			return ErrCapabilityUnavailable
		}
		return executionBridgeError(m.budget.SettleOn(ctx, tx, l.TenantID, l.ProjectID, "EXECUTION", l.ExecutionID, 0, true))
	}
	return nil
}

// NewDeliveryBridge registers the existing capability adapter.
func NewDeliveryBridge(enabled bool) module.Module {
	return &executionDeliveryAdapter{enabled: enabled}
}

var _ module.Enabler = (*executionDeliveryAdapter)(nil)

var _ module.Depender = (*executionDeliveryAdapter)(nil)

var _ module.ServiceInjector = (*executionDeliveryAdapter)(nil)

var _ module.ServiceProvider = (*executionDeliveryAdapter)(nil)
