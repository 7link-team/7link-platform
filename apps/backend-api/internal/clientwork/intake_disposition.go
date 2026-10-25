package clientwork

import (
	"context"
	"errors"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

func (m *Module) injectIntakeConversation(s *module.Services) error {
	fence, fenceErr := module.Resolve[delivery.IntakeWorkFence](s)
	if fenceErr != nil && !errors.Is(fenceErr, module.ErrServiceMissing) {
		return fenceErr
	}
	m.intakeWorkFence = fence
	owner, ownerErr := module.Resolve[delivery.IntakeWorkOwner](s)
	if ownerErr != nil && !errors.Is(ownerErr, module.ErrServiceMissing) {
		return ownerErr
	}
	m.intakeWorkOwner = owner
	provider, err := module.Resolve[collab.IntakeConversation](s)
	if err != nil && !errors.Is(err, module.ErrServiceMissing) {
		return err
	}
	m.intakeConversation = provider
	connector, err := module.Resolve[intake.DispositionRoundConnector](s)
	if errors.Is(err, module.ErrServiceMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if connector.Bind == nil {
		return ErrUnavailable
	}
	if err := connector.Bind(m.closeClarificationForDispositionOn); err != nil {
		return err
	}
	if connector.BindNotification == nil {
		return ErrUnavailable
	}
	return connector.BindNotification(m.notifyIntakeDispositionOn)
}

func (m *Module) notifyIntakeDispositionOn(ctx context.Context, tx *gorm.DB, tenant, project, source guid.ID, body string, seen *int64) (intake.DispositionDelivery, error) {
	row, err := m.intakeThreadOn(ctx, tx, project, source)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return intake.DispositionDelivery{State: "UNAVAILABLE"}, nil
	}
	if err != nil {
		return intake.DispositionDelivery{}, err
	}
	hash, err := m.validateIntakeThreadOn(ctx, tx, project, source, threadLink(row))
	if err != nil {
		return intake.DispositionDelivery{}, err
	}
	if m.intakeConversation.ReplyOn == nil && m.dispositionNotice == nil {
		return intake.DispositionDelivery{}, ErrUnavailable
	}
	var message guid.ID
	if m.dispositionNotice != nil {
		message, err = m.dispositionNotice(ctx, tx, tenant, project, row.ChannelID, row.RootMessageID, row.RecipientID, hash, body, seen)
	} else {
		message, err = m.intakeConversation.ReplyOn(ctx, tx, tenant, project, row.ChannelID, row.RootMessageID, row.RecipientID, hash, body)
	}
	if err != nil {
		return intake.DispositionDelivery{}, err
	}
	return intake.DispositionDelivery{State: "POSTED", MessageID: &message}, nil
}

func (m *Module) closeClarificationForDispositionOn(ctx context.Context, tx *gorm.DB, tenant, project, source, round guid.ID, permission, reason string) error {
	if permission != "intake_convert" && permission != "intake_decline" {
		return ErrForbidden
	}
	if !validText(reason, 2000) {
		return ErrInvalid
	}
	q := query.BindClarificationRound(tx, m.names)
	row, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.IntakeID.Eq(source), q.ID.Eq(round), q.State.Eq("OPEN")).First()
	if err != nil {
		return err
	}
	at := now()
	changed, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(round), q.Version.Eq(row.Version), q.State.Eq("OPEN")).Updates(map[string]any{"state": "CANCELLED", "closed_at": at, "close_reason": "CANCELLED_BY_TEAM", "close_note": reason, "next_reminder_at": nil, "version": row.Version + 1, "updated_at": at}) //nolint:misspell // Frozen domain states.
	if err != nil {
		return err
	}
	if changed.RowsAffected != 1 {
		return ErrConflict
	}
	if kind, authority := intake.DispositionActor(ctx); kind == "AGENT" {
		if m.audit == nil || m.events == nil {
			return ErrUnavailable
		}
		e := audit.NewDomainEvent(ctx, tenant, "clientwork.clarification_cancelled", Name, round) //nolint:misspell // Frozen domain event.
		e.Detail["actor_kind"], e.Detail["authority_source"], e.Detail["project_id"], e.Detail["version"] = kind, authority, project.String(), row.Version+1
		if err := m.audit(ctx, tx, e); err != nil {
			return err
		}
		_, err := m.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: project, Type: "clientwork.clarification_cancelled", Actor: events.Actor{Kind: kind, ID: authn.PrincipalIDFromContext(ctx), AuthoritySource: authority}, Subject: events.Subject{Kind: "clientwork", ID: round}, SubjectVersion: int64(row.Version + 1), Data: map[string]any{"version": row.Version + 1}}) //nolint:misspell // Frozen domain event.
		return err
	}
	return m.event(ctx, tx, project, round, "clarification_cancelled", permission, row.Version+1) //nolint:misspell // Frozen event name.
}
