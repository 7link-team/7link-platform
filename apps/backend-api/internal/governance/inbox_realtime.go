package governance

import (
	activityi18n "7link.com/platform/internal/governance/i18n"
	"context"
	"errors"
	"log/slog"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// Inbox changes are hints only: no source identity, counts or decision facts
// cross this topic. Every consumer re-reads its own current authorized sources.
// This also invalidates resolved items for recipients other than the actor.
func (m *Module) bindInboxRealtime(s *module.Services) error {
	registry, err := module.Resolve[realtime.TopicAuthorizers](s)
	if errors.Is(err, module.ErrServiceMissing) {
		slog.Warn("governance inbox realtime unavailable without topic authorizers")
		return nil
	}
	if err != nil {
		return err
	}
	if err = registry.Register("governance_inbox", func(ctx context.Context, tenant, principal guid.ID, topic realtime.Topic) (bool, error) {
		// The stream registrar rechecks active tenant membership, exactly as the
		// inbox HTTP route does. A topic can never broaden that route's scope.
		return topic.Collection && topic.Kind == "governance_inbox" && topic.TenantID == tenant && tenant > 0 && principal > 0 && authn.TenantIDFromContext(ctx) == tenant && authn.PrincipalIDFromContext(ctx) == principal, nil
	}); err != nil {
		return err
	}
	broadcast, err := module.Resolve[realtime.Broadcaster](s)
	if err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_business_governance_inbox_invalidation_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_business_governance_inbox_invalidation_description",
		Name: "business_governance.inbox_invalidation", Mode: events.Transactional, StartFrom: events.Latest,
		EventTypes: []string{"work_item.*", "result.*", "execution.*", "intake.*", "clarification.*", "delivery.*", "package.*", "project.*", "plan.*", "report.*", "memory.*", "agent.*", "spawn_request.*", "dispatch_proposal.*", "ensemble.*", "review_sheet.*", "worklog.*", "preview_share.*", "governance.*", "quality.*", "clientwork.*", "planning.*", "knowledge.*", "fleet.*", "agents.*", "yunxiao.*", "delegation.*", "escalation.*", "review_plan.*", "andon.*", "verification.*", "artifact_comment.*", "preview.*"},
		Handle:     func(context.Context, *gorm.DB, events.Envelope) error { return nil },
		AfterCommit: func(ctx context.Context, event events.Envelope) {
			if event.TenantID > 0 {
				_ = broadcast(ctx, "tenant/"+event.TenantID.String()+"/governance_inbox", realtime.Message{Type: "governance.inbox_changed", Payload: map[string]any{}})
			}
		},
	})
}
