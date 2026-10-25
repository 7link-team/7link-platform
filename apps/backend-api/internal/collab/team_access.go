package collab

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// teamThreadAccessOn checks the actual project-less DIRECT participants. A role,
// Agent owner relationship, or current project selector grants no private access.
func (m *Module) teamThreadAccessOn(ctx context.Context, tx *gorm.DB, tenant, thread, target guid.ID) (int64, error) {
	actor := authn.PrincipalIDFromContext(ctx)
	if tx == nil || tx.Statement == nil || tenant <= 0 || thread <= 0 || target <= 0 {
		return 0, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return 0, ErrInvalidArgument
	}
	if actor <= 0 || authn.TenantIDFromContext(ctx) != tenant {
		return 0, ErrForbidden
	}
	if m.principals.LockActiveRow == nil || m.members == nil {
		return 0, ErrUnavailable
	}
	participants := []guid.ID{actor, target}
	slices.Sort(participants)
	participants = slices.Compact(participants)
	for _, participant := range participants {
		principal, active, err := m.principals.LockActiveRow(ctx, tx, participant)
		if err != nil {
			return 0, err
		}
		if !active || principal.ID != participant {
			return 0, ErrForbidden
		}
	}
	for _, participant := range participants {
		active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, participant)
		if err != nil {
			return 0, err
		}
		if !active {
			return 0, ErrForbidden
		}
	}
	q := query.BindMessages(tx, m.names)
	root, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ID.Eq(thread), q.ReplyToMessageID.IsNull(), q.WithdrawnAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	c := query.BindChannels(tx, m.names)
	_, err = c.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(c.TenantID.Eq(tenant), c.ID.Eq(root.ChannelID), c.ProjectID.IsNull(), c.Kind.Eq("DIRECT"), c.ArchivedAt.IsNull()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	members := query.BindMembers(tx, m.names)
	for _, participant := range participants {
		member, err := members.WithContext(ctx).Where(members.TenantID.Eq(tenant), members.ChannelID.Eq(root.ChannelID), members.PrincipalID.Eq(participant), members.LeftAt.IsNull()).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrNotFound
		}
		if err != nil {
			return 0, err
		}
		if member.External {
			return 0, ErrNotFound
		}
	}
	stream := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ChannelID.Eq(root.ChannelID), q.WithContext(ctx).Where(q.ID.Eq(thread)).Or(q.ReplyToMessageID.Eq(thread)))
	latest, err := stream.Order(q.ContextSeq.Desc(), q.Seq.Desc()).First()
	if err != nil {
		return 0, err
	}
	return max(latest.Seq, latest.ContextSeq), nil
}
