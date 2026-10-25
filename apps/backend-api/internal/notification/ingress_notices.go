package notification

import (
	"context"
	"errors"

	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/yunxiao"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) injectIngress(s *module.Services) error {
	facts, err := module.Resolve[yunxiao.IngressFailureFacts](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{Name: "notification.integration_ingress", EventTypes: []string{"integration.ingress_changed"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: func(ctx context.Context, tx *gorm.DB, e events.Envelope) error {
		eventCtx, changes := collectInboxChanges(ctx)
		if err := m.ingressOn(eventCtx, tx, e, facts); err != nil {
			return err
		}
		return events.AfterCommit(ctx, func() { m.broadcastInboxChanges(context.WithoutCancel(ctx), changes) })
	}})
}

func (m *Module) ingressOn(ctx context.Context, tx *gorm.DB, e events.Envelope, facts yunxiao.IngressFailureFacts) error {
	if !validTransaction(tx) || e.TenantID <= 0 || e.Subject.ID <= 0 || e.Subject.Kind != "integration_ingress" || e.EventType != "integration.ingress_changed" {
		return ErrInvalid
	}
	if facts == nil || m.candidates == nil || m.tenantPermission == nil {
		return ErrUnavailable
	}
	connection, exhausted, err := facts(ctx, tx, e.TenantID, e.Subject.ID)
	if err != nil || !exhausted {
		return err
	}
	if connection <= 0 {
		return ErrInvalid
	}
	var after guid.ID
	for {
		ids, err := m.candidates(ctx, tx, after, 100)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return ErrInvalid
			}
			after = id
			// Reuse the inbox's current membership, Human and tenant permission
			// policy; the same permission is checked again on every inbox read.
			yes, err := m.visibleOn(ctx, tx, &model.Notification{TenantID: e.TenantID, RecipientPrincipalID: id, Permission: "integration_manage", Type: "integration.ingress_exhausted", Params: "{}"}, id)
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			if err = m.notifySourceOn(ctx, tx, Notification{Channels: []ChannelName{}, TenantID: e.TenantID, Recipients: []guid.ID{id}, Type: "integration.ingress_exhausted", Severity: Action, TemplateKey: "yunxiao.ingress_exhausted", Permission: "integration_manage", Params: map[string]string{"subject_id": e.Subject.ID.String(), "connection_id": connection.String()}, LinkPath: "/admin/" + e.TenantID.String() + "/integrations", DedupeKey: "ingress-exhausted:" + e.Subject.ID.String()}, e.Subject.ID, e.Subject.ID); err != nil {
				return err
			}
		}
		if len(ids) < 100 {
			return nil
		}
	}
}
