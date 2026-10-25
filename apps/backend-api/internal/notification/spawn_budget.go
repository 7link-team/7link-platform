package notification

import (
	"7link.com/platform/internal/governance"
	"context"
	"errors"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// Rule 11 adapter: source-owned settled spend is projected through the existing
// notification transaction, visibility checks, inbox, delivery and preferences.
func (m *Module) injectSpawnBudget(s *module.Services) error {
	if f, err := module.Resolve[governance.SpawnNotificationFacts](s); err == nil {
		m.spawnFacts = f
	} else if !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	c, e := module.Resolve[events.SubscriberCatalog](s)
	if e != nil {
		return e
	}
	return c.AddSubscribers(Name, events.Subscriber{Name: "notification.spawn_budget", EventTypes: []string{"spawn_grant.budget_threshold", "spawn_grant.exhausted", "spawn_grant.granted", "governance.spawn_request_changed"}, Mode: events.Transactional, StartFrom: events.Earliest, Handle: func(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		if err := m.consumeSpawnEventOn(eventCtx, tx, e); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}})
}

func (m *Module) consumeSpawnBudget(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
	return m.consumeSpawnEventOn(ctx, tx, e)
}
