package execution

import (
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/guid"
	"context"
	"errors"
	"gorm.io/gorm"
)

var ErrBudgetExceeded = errors.New("execution: project budget exceeded")
var ErrPriceUnknown = errors.New("execution: price unknown for capped project")

// BudgetFact comes from the owning project aggregate while its row is locked.
// Nil ceiling means uncapped; unknown model prices are never interpreted as free.
type BudgetFact struct {
	CapMicros      *int64
	SpentMicros    int64
	ReservedMicros int64
}

func CheckBudget(fact BudgetFact, input, output int64, price *fleet.TokenPrice) (int64, bool, error) {
	if fact.SpentMicros < 0 || fact.ReservedMicros < 0 {
		return 0, false, ErrInvalidArgument
	}
	cost, known, err := fleet.EstimateTokenCost(input, output, price)
	if err != nil {
		return 0, false, err
	}
	if fact.CapMicros == nil {
		return cost, known, nil
	}
	ceiling := *fact.CapMicros
	if ceiling < 0 {
		return 0, false, ErrInvalidArgument
	}
	if !known {
		return 0, false, ErrPriceUnknown
	}
	// Subtraction avoids overflow even when persisted counters approach MaxInt64.
	if fact.SpentMicros > ceiling || fact.ReservedMicros > ceiling-fact.SpentMicros || cost > ceiling-fact.SpentMicros-fact.ReservedMicros {
		return 0, true, ErrBudgetExceeded
	}
	return cost, true, nil
}

func (m *Module) budgetOn(ctx context.Context, tx *gorm.DB, tenant guid.ID, rowLink WorkLink, plan *ExecutionPlan) error {
	if m.work.BudgetOn == nil || m.work.ReserveBudgetOn == nil || m.machines.ModelPriceOn == nil {
		return ErrCapabilityUnavailable
	}
	plan.PriceSnapshot = nil
	budget, err := m.work.BudgetOn(ctx, tx, rowLink)
	if err != nil {
		return err
	}
	price, err := m.machines.ModelPriceOn(ctx, tx, tenant, plan.Runtime, plan.Model)
	if err != nil {
		return err
	}
	cost, known, err := CheckBudget(budget, plan.MaxInputTokens, plan.MaxOutputTokens, price)
	if err != nil {
		return err
	}
	if price != nil {
		plan.PriceSnapshot = &ExecutionPrice{InputMicros: price.InputMicros, OutputMicros: price.OutputMicros}
	}
	return m.work.ReserveBudgetOn(ctx, tx, rowLink, cost, known)
}

func actualExecutionCost(plan ExecutionPlan, usage *ExecutionUsage) (*int64, error) {
	if usage == nil {
		return nil, nil
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 {
		return nil, ErrInvalidArgument
	}
	if plan.PriceSnapshot == nil {
		return nil, nil
	}
	cost, known, err := fleet.EstimateTokenCost(usage.InputTokens, usage.OutputTokens, &fleet.TokenPrice{InputMicros: plan.PriceSnapshot.InputMicros, OutputMicros: plan.PriceSnapshot.OutputMicros})
	if err != nil {
		return nil, err
	}
	if !known {
		return nil, nil
	}
	return &cost, nil
}
