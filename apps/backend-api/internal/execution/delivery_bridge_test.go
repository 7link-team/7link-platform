package execution

import (
	"7link.com/platform/internal/budget"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"errors"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestExecutionDeliveryAdapterFailsExplicitly(t *testing.T) {
	s := module.NewServices()
	adapter := &executionDeliveryAdapter{enabled: true}
	require.NoError(t, adapter.ProvideServices(s))
	require.NoError(t, s.Seal())
	port, err := module.Resolve[WorkItems](s)
	require.NoError(t, err)
	require.ErrorIs(t, port.AuthorizeOn(t.Context(), nil, WorkLink{}, "CREATE"), ErrCapabilityUnavailable)
	require.ErrorIs(t, port.StartedOn(t.Context(), nil, WorkLink{}), ErrCapabilityUnavailable)
	require.ErrorIs(t, port.FinishedOn(t.Context(), nil, WorkLink{}, WorkResult{}), ErrCapabilityUnavailable)
}
func TestExecutionAdapterUsesCallerTransactionAndPreservesEvidence(t *testing.T) {
	tx := &gorm.DB{}
	l := WorkLink{TenantID: 1, ProjectID: 2, WorkItemID: 3, AssignmentID: 4, ExecutionID: 5, ActorID: 6, InitiatorID: 7}
	seen := map[string]int{}
	a := &executionDeliveryAdapter{enabled: true}
	a.work = delivery.ExecutionWork{
		PrincipalsOn: func(_ context.Context, db *gorm.DB, in delivery.ExecutionWorkLink) ([]guid.ID, error) {
			require.Same(t, tx, db)
			require.Equal(t, guid.ID(4), in.AssignmentID)
			return []guid.ID{8}, nil
		},
		AuthorizeOn: func(_ context.Context, db *gorm.DB, in delivery.ExecutionWorkLink, op string) error {
			require.Same(t, tx, db)
			require.Equal(t, executionLink(l), in)
			require.Equal(t, "CREATE", op)
			seen["authorize"]++
			return nil
		},
		StartedOn: func(_ context.Context, db *gorm.DB, in delivery.ExecutionWorkLink) error {
			require.Same(t, tx, db)
			require.Equal(t, executionLink(l), in)
			seen["started"]++
			return nil
		},
		FinishedOn: func(_ context.Context, db *gorm.DB, in delivery.ExecutionWorkLink, r delivery.ExecutionCompletion) error {
			require.Same(t, tx, db)
			require.Equal(t, executionLink(l), in)
			seen["finished"]++
			if r.State == "SUCCEEDED" {
				require.Equal(t, "head", r.HeadCommit)
				require.Equal(t, guid.ID(23), r.Evidence[0].Artifact.VersionID)
				require.Equal(t, "hash", r.Evidence[0].ContentHash)
			}
			return nil
		},
	}
	budgetCap := int64(100)
	a.budget = budget.Provider{
		FactOn: func(_ context.Context, db *gorm.DB, tenant, project guid.ID) (budget.Fact, error) {
			require.Same(t, tx, db)
			require.Equal(t, guid.ID(1), tenant)
			require.Equal(t, guid.ID(2), project)
			return budget.Fact{CapMicros: &budgetCap, SpentMicros: 3, ReservedMicros: 4}, nil
		},
		ReserveOn: func(_ context.Context, db *gorm.DB, tenant, project guid.ID, source string, id guid.ID, cost int64, known bool) error {
			require.Same(t, tx, db)
			require.Equal(t, "EXECUTION", source)
			require.Equal(t, l.ExecutionID, id)
			require.EqualValues(t, 9, cost)
			require.True(t, known)
			seen["reserve"]++
			return nil
		},
		SettleOn: func(_ context.Context, db *gorm.DB, tenant, project guid.ID, source string, id guid.ID, cost int64, known bool) error {
			require.Same(t, tx, db)
			require.Equal(t, "EXECUTION", source)
			require.Equal(t, l.ExecutionID, id)
			require.Zero(t, cost)
			require.True(t, known)
			seen["settle"]++
			return nil
		},
	}
	s := module.NewServices()
	require.NoError(t, a.ProvideServices(s))
	require.NoError(t, s.Seal())
	p, err := module.Resolve[WorkItems](s)
	require.NoError(t, err)
	ids, err := p.PrincipalsOn(t.Context(), tx, l)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{8}, ids)
	require.NoError(t, p.AuthorizeOn(t.Context(), tx, l, "CREATE"))
	require.NoError(t, p.StartedOn(t.Context(), tx, l))
	f, err := p.BudgetOn(t.Context(), tx, l)
	require.NoError(t, err)
	require.Equal(t, budgetCap, *f.CapMicros)
	require.EqualValues(t, 3, f.SpentMicros)
	require.EqualValues(t, 4, f.ReservedMicros)
	require.NoError(t, p.ReserveBudgetOn(t.Context(), tx, l, 9, true))
	require.NoError(t, p.FinishedOn(t.Context(), tx, l, WorkResult{State: "SUCCEEDED", HeadCommit: "head", Evidence: []EvidenceFact{{ID: 11, ContentHash: "hash", Artifact: &ArtifactFact{SpaceID: 21, FileID: 22, VersionID: 23}}}}))
	require.Zero(t, seen["settle"], "unknown runtime usage must retain reservation")
	require.NoError(t, p.FinishedOn(t.Context(), tx, l, WorkResult{State: StateCanceled, Unused: true}))
	require.Equal(t, 1, seen["settle"])
}
func TestExecutionBridgeClassifiesDomainErrors(t *testing.T) {
	require.ErrorIs(t, executionBridgeError(delivery.ErrPlanNotApproved), ErrConflict)
	for _, pair := range [][2]error{{delivery.ErrWorkForbidden, ErrForbidden}, {delivery.ErrInvalidMember, ErrForbidden}, {delivery.ErrAssignmentHolder, ErrForbidden}, {delivery.ErrProjectNotFound, ErrNotFound}, {gorm.ErrRecordNotFound, ErrNotFound}, {delivery.ErrInvalidArgument, ErrInvalidArgument}, {delivery.ErrAssignmentState, ErrConflict}, {delivery.ErrAssignmentHeld, ErrConflict}, {delivery.ErrWorkProjectReadOnly, ErrConflict}, {delivery.ErrWorkVersion, ErrConflict}, {delivery.ErrResultState, ErrConflict}, {budget.ErrExceeded, ErrBudgetExceeded}, {budget.ErrPriceUnknown, ErrPriceUnknown}, {ErrUnavailable, ErrUnavailable}} {
		require.ErrorIs(t, executionBridgeError(pair[0]), pair[1])
	}
	require.NoError(t, executionBridgeError(nil))
}

func TestExecutionBudgetSettlementDoesNotCompleteAcceptedSourceWork(t *testing.T) {
	tx := &gorm.DB{}
	link := WorkLink{TenantID: 1, ProjectID: 2, WorkItemID: 3, ExecutionID: 4}
	a := &executionDeliveryAdapter{}
	settled := 0
	a.work.FinishedOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink, delivery.ExecutionCompletion) error {
		t.Fatal("budget-only completion must not mutate accepted delivery work")
		return nil
	}
	a.budget.SettleOn = func(_ context.Context, db *gorm.DB, tenant, project guid.ID, kind string, id guid.ID, actual int64, known bool) error {
		require.Same(t, tx, db)
		require.Equal(t, link.TenantID, tenant)
		require.Equal(t, link.ProjectID, project)
		require.Equal(t, "EXECUTION", kind)
		require.Equal(t, link.ExecutionID, id)
		require.EqualValues(t, 17, actual)
		require.True(t, known)
		settled++
		return nil
	}
	s := module.NewServices()
	require.NoError(t, a.ProvideServices(s))
	require.NoError(t, s.Seal())
	port, err := module.Resolve[WorkItems](s)
	require.NoError(t, err)
	require.NoError(t, port.SettleBudgetOn(t.Context(), tx, link, WorkResult{State: "SUCCEEDED"}))
	require.Zero(t, settled, "unknown usage keeps the existing reservation")
	actual := int64(17)
	require.NoError(t, port.SettleBudgetOn(t.Context(), tx, link, WorkResult{State: "SUCCEEDED", ActualMicros: &actual}))
	require.Equal(t, 1, settled)
}

func TestExecutionDeliveryBridgeWiringRequiresEveryCurrentProvider(t *testing.T) {
	for omitted := -1; omitted < 5; omitted++ {
		t.Run(guid.ID(omitted+1).String(), func(t *testing.T) {
			s := module.NewServices()
			bound := false
			failure := errors.New("repository authorization failed")
			providers := []func() error{
				func() error { return module.Provide(s, delivery.ExecutionWork{}) }, func() error { return module.Provide(s, budget.Provider{}) }, func() error { return module.Provide(s, delivery.LoopAssignments{}) },
				func() error {
					return module.Provide(s, fleet.RepositoryConnector{Bind: func(fn fleet.RepositoryAuthorizer) error { bound = true; return fn(t.Context(), nil, 7, 9, 4) }})
				},
				func() error {
					return module.Provide[fleet.RepositoryAuthorizer](s, func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return failure })
				},
			}
			for i, provide := range providers {
				if i != omitted {
					require.NoError(t, provide())
				}
			}
			require.NoError(t, s.Seal())
			a := &executionDeliveryAdapter{enabled: true}
			e := a.InjectServices(s)
			if omitted < 0 {
				require.ErrorIs(t, e, failure)
				require.True(t, bound)
			} else {
				require.ErrorIs(t, e, module.ErrServiceMissing)
				require.False(t, bound)
			}
		})
	}
}
func TestExecutionDeliveryBridgeOptionalCapabilitiesAndFailuresPreserveCallerContract(t *testing.T) {
	a := &executionDeliveryAdapter{}
	s := module.NewServices()
	require.NoError(t, a.ProvideServices(s))
	require.NoError(t, s.Seal())
	p, e := module.Resolve[WorkItems](s)
	require.NoError(t, e)
	ctx := t.Context()
	tx := &gorm.DB{}
	l := WorkLink{TenantID: 7, ProjectID: 9, AssignmentID: 6, ExecutionID: 8}
	failure := errors.New("source failed")
	_, e = p.PrincipalsOn(ctx, tx, l)
	require.ErrorIs(t, e, ErrCapabilityUnavailable)
	_, e = p.BudgetOn(ctx, tx, l)
	require.ErrorIs(t, e, ErrCapabilityUnavailable)
	require.ErrorIs(t, p.ReserveBudgetOn(ctx, tx, l, 1, true), ErrCapabilityUnavailable)
	require.ErrorIs(t, p.ProgressStopOn(ctx, tx, l, "NO_PROGRESS"), ErrCapabilityUnavailable)
	require.ErrorIs(t, p.AuthorizeVisualReviewOn(ctx, tx, l, 50, 2, 51, "head"), ErrCapabilityUnavailable)
	actual := int64(1)
	require.ErrorIs(t, p.SettleBudgetOn(ctx, tx, l, WorkResult{ActualMicros: &actual}), ErrCapabilityUnavailable)
	require.ErrorIs(t, p.SettleBudgetOn(ctx, tx, l, WorkResult{Unused: true}), ErrCapabilityUnavailable)
	id, e := p.AssignedAgentOn(ctx, tx, l)
	require.NoError(t, e)
	require.Zero(t, id)
	a.assignments.AgentsOn = func(_ context.Context, db *gorm.DB, tenant, project guid.ID, ids []guid.ID) (map[guid.ID]guid.ID, error) {
		require.Same(t, tx, db)
		require.Equal(t, []guid.ID{6}, ids)
		return map[guid.ID]guid.ID{6: 21}, nil
	}
	id, e = p.AssignedAgentOn(ctx, tx, l)
	require.NoError(t, e)
	require.EqualValues(t, 21, id)
	a.work.ProgressStopOn = func(_ context.Context, db *gorm.DB, in delivery.ExecutionWorkLink, reason string) error {
		require.Same(t, tx, db)
		require.Equal(t, executionLink(l), in)
		require.Equal(t, "NO_PROGRESS", reason)
		return failure
	}
	require.ErrorIs(t, p.ProgressStopOn(ctx, tx, l, "NO_PROGRESS"), failure)
	a.work.AuthorizeVisualReviewOn = func(_ context.Context, db *gorm.DB, in delivery.ExecutionWorkLink, result guid.ID, version int, criterion guid.ID, head string) error {
		require.Same(t, tx, db)
		require.Equal(t, executionLink(l), in)
		require.EqualValues(t, 50, result)
		require.Equal(t, 2, version)
		require.EqualValues(t, 51, criterion)
		require.Equal(t, "head", head)
		return failure
	}
	require.ErrorIs(t, p.AuthorizeVisualReviewOn(ctx, tx, l, 50, 2, 51, "head"), failure)
	a.work.FinishedOn = func(context.Context, *gorm.DB, delivery.ExecutionWorkLink, delivery.ExecutionCompletion) error {
		return delivery.ErrWorkForbidden
	}
	require.ErrorIs(t, p.FinishedOn(ctx, tx, l, WorkResult{State: "FAILED"}), ErrForbidden)
}
