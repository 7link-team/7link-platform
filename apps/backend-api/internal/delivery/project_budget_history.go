package delivery

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// ProjectBudgetPoint is an actual settlement observation. Money is a decimal
// integer string so browsers retain all int64 micros, including historical costs.
type ProjectBudgetPoint struct {
	ID          guid.ID   `json:"id"`
	At          time.Time `json:"at"`
	SpentMicros string    `json:"spent_micros"`
}

type ProjectBudgetTrend struct {
	Available bool                 `json:"available"`
	Truncated bool                 `json:"truncated"`
	Points    []ProjectBudgetPoint `json:"points"`
}

// ProjectBudgetHistory is read inside the already-authorized project's snapshot.
// The budget owner reads its ledger; Delivery never imports its persistence.
type ProjectBudgetHistory func(context.Context, *gorm.DB, guid.ID, guid.ID, int64) (ProjectBudgetTrend, error)
type ProjectBudgetHistoryConnector struct {
	Bind func(ProjectBudgetHistory) error
}

func (m *Module) provideProjectBudgetHistory(s *module.Services) error {
	return module.Provide[ProjectBudgetHistoryConnector](s, ProjectBudgetHistoryConnector{Bind: func(read ProjectBudgetHistory) error {
		if m == nil || m.service == nil || read == nil {
			return ErrUnavailable
		}
		m.service.budgetHistory = read
		return nil
	}})
}
