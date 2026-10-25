package collab

import (
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"gorm.io/gorm"
)

// MentionNotifications reuses the channel owner's authorization at projection,
// inbox read and SMTP time, including explicit MEMBERS_ONLY membership.
type MentionNotifications struct {
	RecipientsOn func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]guid.ID, error)
	VisibleOn    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
}

func (m *Module) provideNotificationMentions(s *module.Services) error {
	return module.Provide[MentionNotifications](s, MentionNotifications{RecipientsOn: m.notificationMentionsOn, VisibleOn: m.notificationMentionVisibleOn})
}
func (m *Module) notificationMentionsOn(ctx context.Context, tx *gorm.DB, tenant, message guid.ID) ([]guid.ID, error) {
	if tx == nil || tenant <= 0 || message <= 0 {
		return nil, ErrInvalidArgument
	}
	q := query.BindMentions(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.MessageID.Eq(message)).Order(q.PrincipalID).Find()
	if err != nil {
		return nil, err
	}
	ids := []guid.ID{}
	for _, r := range rows {
		visible, err := m.notificationMentionVisibleOn(ctx, tx, tenant, message, r.PrincipalID)
		if err != nil {
			return nil, err
		}
		if visible {
			ids = append(ids, r.PrincipalID)
		}
	}
	return ids, nil
}
func (m *Module) notificationMentionVisibleOn(ctx context.Context, tx *gorm.DB, tenant, message, principal guid.ID) (bool, error) {
	if tx == nil || tenant <= 0 || message <= 0 || principal <= 0 {
		return false, ErrInvalidArgument
	}
	q := query.BindMessages(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(message)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c := query.BindChannels(tx, m.names)
	channel, err := c.WithContext(ctx).Where(c.TenantID.Eq(tenant), c.ID.Eq(r.ChannelID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if channel.ProjectID == nil {
		claims := &authn.Claims{TenantID: tenant, PrincipalID: principal, SubjectType: authn.SubjectTypePrincipal}
		reader := authn.WithClaims(ctx, claims)
		if _, err = m.humanOn(reader, tx, tenant, principal); errors.Is(err, ErrForbidden) || errors.Is(err, ErrMemberInactive) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		_, err = m.directAccessOn(reader, tx, tenant, channel.ID, false)
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrMemberInactive) {
			return false, nil
		}
		return err == nil, err
	}
	claims := &authn.Claims{TenantID: tenant, PrincipalID: principal, SubjectType: authn.SubjectTypePrincipal}
	_, member, err := m.authorizeChannel(authn.WithClaims(ctx, claims), tx, tenant, *channel.ProjectID, channel.ID, "channel_read")
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if member.MemberKind != "HUMAN" || member.External && r.Visibility == "INTERNAL" {
		return false, nil
	}
	return true, nil
}
