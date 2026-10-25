package agents

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

// RosterAnalytics counts actual first decisions in the current reader's
// authorized scope. Zero samples must never be presented as perfect quality.
type RosterAnalytics struct {
	FirstDecisions   int64 `json:"first_decisions"`
	FirstPassAccepts int64 `json:"first_pass_accepts"`
}
type AgentRosterAnalyticsConnector struct {
	Bind func(func(context.Context, *gorm.DB, guid.ID, []guid.ID) (map[guid.ID]RosterAnalytics, error)) error
}

func (m *Module) provideRoster(s *module.Services) error {
	return module.Provide(s, AgentRosterAnalyticsConnector{Bind: func(f func(context.Context, *gorm.DB, guid.ID, []guid.ID) (map[guid.ID]RosterAnalytics, error)) error {
		if f == nil || m.rosterAnalytics != nil {
			return ErrInvalid
		}
		m.rosterAnalytics = f
		return nil
	}})
}
