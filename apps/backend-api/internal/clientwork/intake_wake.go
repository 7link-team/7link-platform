package clientwork

import (
	"7link.com/platform/internal/agents"
	activityi18n "7link.com/platform/internal/clientwork/i18n"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
)

func (m *Module) injectIntakeWake(s *module.Services) error {
	var err error
	if m.triageIdentity, err = module.Resolve[fleet.SpawnGrantDirectory](s); err != nil {
		return err
	}
	if m.triageInbox, err = module.Resolve[agents.InboxAppender](s); err != nil {
		return err
	}
	if m.triageTarget, err = module.Resolve[intake.TriageWakeTarget](s); err != nil {
		return err
	}
	scope, err := module.Resolve[delivery.ProjectScope](s)
	if err != nil {
		return err
	}
	connector, err := module.Resolve[intake.TriageAgentConnector](s)
	if err != nil {
		return err
	}
	if scope.AuthorizeOn == nil || connector.BindAuthority == nil {
		return ErrUnavailable
	}
	if err = connector.BindAuthority(intake.TriageRequestAuthority(scope.AuthorizeOn)); err != nil {
		return err
	}
	catalog, err := module.Resolve[events.SubscriberCatalog](s)
	if err != nil {
		return err
	}
	return catalog.AddSubscribers(Name, events.Subscriber{DisplayNameKey: activityi18n.Namespace + ".activity_subscriber_clientwork_intake_triage_wake_name", DescriptionKey: activityi18n.Namespace + ".activity_subscriber_clientwork_intake_triage_wake_description", Name: "clientwork.intake_triage_wake", EventTypes: []string{"intake.received", "intake.triage_requested"}, Mode: events.Transactional, StartFrom: events.Latest, Handle: m.intakeWakeOn})
}

func (m *Module) intakeWakeOn(ctx context.Context, tx *gorm.DB, event events.Envelope) error {
	if m.triageTarget.ReadOn == nil {
		return ErrUnavailable
	}
	if event.Actor.ID <= 0 {
		return ErrInvalid
	}
	// Participant locking needs the actual persisted source actor, even when
	// invoked by a background subscriber. This conveys no project write grant.
	ctx = authn.WithClaims(ctx, &authn.Claims{TenantID: event.TenantID, EntityID: event.ProjectID, PrincipalID: event.Actor.ID, SubjectType: authn.SubjectTypePrincipal})
	agent, found, err := m.triageTarget.ReadOn(ctx, tx, event)
	if err != nil || !found {
		return err
	}
	if agent <= 0 || m.triageInbox.AppendOn == nil || m.triageIdentity.AgentOn == nil {
		return ErrUnavailable
	}
	identity, found, err := m.triageIdentity.AgentOn(ctx, tx, event.TenantID, agent)
	if err != nil {
		return err
	}
	if !found || identity.ID != agent || identity.TenantID != event.TenantID || identity.PrincipalID <= 0 || identity.State != "ACTIVE" {
		return ErrUnavailable
	}
	_, err = m.triageInbox.AppendOn(ctx, tx, event.TenantID, identity.PrincipalID, agents.InboxReference{SourceEventID: "intake:" + event.EventID.String(), Kind: "STATE", SubjectID: event.Subject.ID})
	return err
}
