package collab

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

var (
	ErrChannelImmutable = errors.New("collab: immutable channel")
	ErrChannelVersion   = errors.New("collab: stale channel version")
	ErrChannelArchived  = errors.New("collab: archived channel")
	ErrLastOwner        = errors.New("collab: last channel owner")
)

type ChannelUpdateInput struct {
	Version  int     `json:"version" minimum:"1"`
	Name     *string `json:"name,omitempty" minLength:"1" maxLength:"40"`
	Topic    *string `json:"topic,omitempty" maxLength:"200"`
	Archived *bool   `json:"archived,omitempty"`
}

// Custom-channel ownership is an existing explicit membership, never inferred
// from a tenant role. IAM is rechecked for non-owner management in this tx.
func (m *Module) channelManagerOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, member *model.ChannelMember) error {
	if member.External || member.MemberKind != "HUMAN" || member.LeftAt != nil {
		return ErrForbidden
	}
	if _, err := m.humanOn(ctx, tx, tenant, member.PrincipalID); err != nil {
		return err
	}
	if member.Role == "OWNER" {
		return nil
	}
	c, err := m.policy(ctx, tx, tenant, project, "channel_manage", authn.PrincipalIDFromContext(ctx))
	if err != nil {
		return err
	}
	if !c.AllowsEntityRows(project) || (!c.AllowAll && !slices.Contains(c.ResourceIDs, project.String()) && !slices.Contains(c.OwnerIDs, member.PrincipalID.String())) {
		return ErrForbidden
	}
	return nil
}

func (m *Module) updateChannelOn(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, in ChannelUpdateInput) (ChannelView, error) {
	ctx = mutationContext(ctx)
	var empty ChannelView
	if in.Version < 1 || (in.Name == nil && in.Topic == nil && in.Archived == nil) {
		return empty, ErrInvalidArgument
	}
	if in.Name != nil && (!utf8.ValidString(*in.Name) || strings.TrimSpace(*in.Name) == "" || utf8.RuneCountInString(*in.Name) > 40) {
		return empty, ErrInvalidArgument
	}
	if in.Topic != nil && (!utf8.ValidString(*in.Topic) || utf8.RuneCountInString(*in.Topic) > 200) {
		return empty, ErrInvalidArgument
	}
	row, member, err := m.authorizeChannelState(ctx, tx, tenant, project, channel, "channel_read", true, false)
	if err != nil {
		return empty, err
	}
	if err = m.channelManagerOn(ctx, tx, tenant, project, member); err != nil {
		return empty, err
	}
	if row.Kind != "CUSTOM" {
		return empty, ErrChannelImmutable
	}
	if row.Version != in.Version {
		return empty, ErrChannelVersion
	}
	if row.ArchivedAt != nil && (in.Archived == nil || *in.Archived || in.Name != nil || in.Topic != nil) {
		return empty, ErrChannelArchived
	}
	updates := map[string]any{"version": row.Version + 1, "updated_at": m.now(), "updated_by": member.PrincipalID}
	if in.Name != nil {
		row.Name = strings.TrimSpace(*in.Name)
		updates["name"] = row.Name
	}
	if in.Topic != nil {
		row.Topic = in.Topic
		updates["topic"] = *in.Topic
	}
	if in.Archived != nil {
		row.ArchivedAt = nil
		if *in.Archived {
			now := m.now()
			row.ArchivedAt = &now
		}
		updates["archived_at"] = row.ArchivedAt
	}
	q := query.BindChannels(tx, m.names)
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(channel), q.Version.Eq(in.Version)).Updates(updates)
	if err != nil {
		return empty, err
	}
	if res.RowsAffected != 1 {
		return empty, ErrChannelVersion
	}
	permission := "channel_manage"
	if member.Role == "OWNER" {
		permission = "CHANNEL_OWNER"
	}
	if err = m.channelEvent(ctx, tx, tenant, project, channel, "collab.channel_updated", permission); err != nil {
		return empty, err
	}
	row.Version++
	return channelView(row), nil
}

func (m *Module) removeChannelMemberOn(ctx context.Context, tx *gorm.DB, tenant, project, channel, target guid.ID) error {
	ctx = mutationContext(ctx)
	if target <= 0 {
		return ErrInvalidArgument
	}
	actor := authn.PrincipalIDFromContext(ctx)
	row, member, err := m.authorizeChannelState(ctx, tx, tenant, project, channel, "channel_read", true, target == actor)
	if err != nil {
		return err
	}
	if row.Kind != "CUSTOM" {
		return ErrChannelImmutable
	}
	if row.ArchivedAt != nil {
		return ErrChannelArchived
	}
	if target != actor {
		if err = m.channelManagerOn(ctx, tx, tenant, project, member); err != nil {
			return err
		}
	} else if member.MemberKind != "HUMAN" || member.External {
		return ErrForbidden
	}
	q := query.BindMembers(tx, m.names)
	peer, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.PrincipalID.Eq(target)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if peer.LeftAt != nil {
		return nil
	}
	if peer.Source != "EXPLICIT" {
		return ErrChannelImmutable
	}
	if peer.Role == "OWNER" {
		owners, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.Role.Eq("OWNER"), q.LeftAt.IsNull()).Count()
		if err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	now := m.now()
	res, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(channel), q.PrincipalID.Eq(target), q.LeftAt.IsNull()).Updates(map[string]any{"left_at": now, "updated_at": now, "updated_by": actor})
	if err != nil {
		return err
	}
	if res.RowsAffected != 1 {
		return ErrChannelVersion
	}
	permission := "channel_manage"
	if target == actor {
		permission = "CHANNEL_SELF"
	} else if member.Role == "OWNER" {
		permission = "CHANNEL_OWNER"
	}
	return m.channelEvent(ctx, tx, tenant, project, channel, "collab.member_updated", permission, target)
}
