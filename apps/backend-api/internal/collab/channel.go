package collab

import (
	"7link.com/platform/internal/collab/model"
	"7link.com/platform/internal/collab/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"gorm.io/gorm"
	"sort"
	"strings"
)

var (
	ErrUnavailable     = errors.New("collab: provisioning capability unavailable")
	ErrInvalidArgument = errors.New("collab: invalid channel input")
	ErrMemberInactive  = errors.New("collab: member unavailable")
	ErrNotFound        = errors.New("collab: default channel not found or archived")
)

// DefaultChannelInput is supplied by an authorized project creation operation.
// ProvisioningAudit carries the business authorization decision; it is never
// inferred from the member being added.
type ProvisioningAudit struct {
	Reason          string
	EvidenceRefs    []string
	AuthoritySource string
}

type DefaultChannelInput struct {
	Audit               ProvisioningAudit
	TenantID, ProjectID guid.ID
	Name                string
	MemberPrincipalIDs  []guid.ID
}

// ChannelProvisioner participates in the caller's transaction. The caller must
// authorize the project operation and roll back on any error. Tenant qualifiers
// are explicit even for globally unique project IDs.
type ChannelProvisioner struct {
	CreateDefaultOn          func(context.Context, *gorm.DB, DefaultChannelInput) (guid.ID, error)
	RenameDefaultOn          func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error
	ArchiveProjectChannelsOn func(context.Context, *gorm.DB, guid.ID, guid.ID, ProvisioningAudit) error
}

func checkTransaction(tx *gorm.DB, tenant, project guid.ID) error {
	if tx == nil || tx.Statement == nil || tenant <= 0 || project <= 0 {
		return ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidArgument
	}
	return nil
}
func channelName(name string) (string, error) {
	runes := []rune(strings.TrimSpace(name))
	if len(runes) == 0 {
		return "", ErrInvalidArgument
	}
	if len(runes) > 40 {
		runes = runes[:40]
	}
	return string(runes), nil
}
func actorID(ctx context.Context) *guid.ID {
	id := authn.PrincipalIDFromContext(ctx)
	if id <= 0 {
		return nil
	}
	return &id
}
func (m *Module) CreateDefaultOn(ctx context.Context, tx *gorm.DB, in DefaultChannelInput) (guid.ID, error) {
	if err := checkTransaction(tx, in.TenantID, in.ProjectID); err != nil {
		return 0, err
	}
	if err := m.Initialize(ctx); err != nil {
		return 0, err
	}
	if strings.TrimSpace(in.Audit.AuthoritySource) == "" {
		return 0, ErrInvalidArgument
	}
	name, err := channelName(in.Name)
	if err != nil {
		return 0, err
	}
	// Stable principal lock order avoids inversion across concurrent provisioning.
	ids := append([]guid.ID(nil), in.MemberPrincipalIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	unique := make([]guid.ID, 0, len(ids))
	kinds := make(map[guid.ID]string, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return 0, ErrInvalidArgument
		}
		if len(unique) > 0 && unique[len(unique)-1] == id {
			continue
		}
		principal, found, err := m.principals.LockActiveRow(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		if !found {
			return 0, ErrMemberInactive
		}
		active, err := m.members.IsMemberActiveOn(ctx, tx, in.TenantID, id)
		if err != nil {
			return 0, err
		}
		if !active {
			return 0, ErrMemberInactive
		}
		human, found, err := m.principals.HumanByLogin(ctx, tx, principal.LoginName)
		if err != nil {
			return 0, err
		}
		kind := "HUMAN"
		if found {
			if human.ID != id {
				return 0, ErrUnavailable
			}
		} else {
			agent, exists, err := m.agents.ByPrincipalOn(ctx, tx, in.TenantID, id)
			if err != nil {
				return 0, err
			}
			if !exists {
				return 0, ErrMemberInactive
			}
			if agent.ID <= 0 || agent.TenantID != in.TenantID || agent.PrincipalID != id {
				return 0, ErrUnavailable
			}
			kind = "AGENT"
		}
		kinds[id] = kind
		unique = append(unique, id)
	}
	id, err := m.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, ErrUnavailable
	}
	now := m.now()
	actor := actorID(ctx)
	row := model.Channel{ID: guid.ID(id), TenantID: in.TenantID, ProjectID: &in.ProjectID, Kind: "PROJECT_DEFAULT", Name: name, Version: 1, CreatedAt: now, UpdatedAt: now, CreatedBy: actor, UpdatedBy: actor}
	if err := query.BindChannels(tx, m.names).WithContext(ctx).Create(&row); err != nil {
		return 0, err
	}
	if err := m.appendAudit(ctx, tx, in.TenantID, in.ProjectID, row.ID, "collab.channel_created", 0, in.Audit); err != nil {
		return 0, err
	}
	for _, id := range unique {
		member := model.ChannelMember{ChannelID: row.ID, PrincipalID: id, TenantID: in.TenantID, MemberKind: kinds[id], Role: "MEMBER", Source: "DERIVED", JoinedAt: now, CreatedAt: now, UpdatedAt: now, CreatedBy: actor, UpdatedBy: actor}
		if err := query.BindMembers(tx, m.names).WithContext(ctx).Create(&member); err != nil {
			return 0, err
		}
		if err := m.appendAudit(ctx, tx, in.TenantID, in.ProjectID, row.ID, "collab.member_added", id, in.Audit); err != nil {
			return 0, err
		}
	}
	return row.ID, nil
}
func (m *Module) RenameDefaultOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, name string) error {
	if err := checkTransaction(tx, tenant, project); err != nil {
		return err
	}
	name, err := channelName(name)
	if err != nil {
		return err
	}
	q := query.BindChannels(tx, m.names)
	result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.Kind.Eq("PROJECT_DEFAULT"), q.ArchivedAt.IsNull()).Updates(map[string]any{"name": name, "updated_at": m.now(), "updated_by": actorID(ctx), "version": gorm.Expr("version + 1")})
	if err != nil {
		return err
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (m *Module) ArchiveProjectChannelsOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, detail ProvisioningAudit) error {
	if err := checkTransaction(tx, tenant, project); err != nil {
		return err
	}
	if strings.TrimSpace(detail.AuthoritySource) == "" {
		return ErrInvalidArgument
	}
	if m.audit == nil {
		return ErrUnavailable
	}
	q := query.BindChannels(tx, m.names)
	rows, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ArchivedAt.IsNull()).Find()
	if err != nil {
		return err
	}
	now := m.now()
	for _, row := range rows {
		result, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(row.ID), q.ArchivedAt.IsNull()).Updates(map[string]any{"archived_at": now, "updated_at": now, "updated_by": actorID(ctx), "version": gorm.Expr("version + 1")})
		if err != nil {
			return err
		}
		if result.RowsAffected > 0 {
			if err := m.appendAudit(ctx, tx, tenant, project, row.ID, "collab.channel_archived", 0, detail); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Module) appendAudit(ctx context.Context, tx *gorm.DB, tenant, project, channel guid.ID, eventType string, member guid.ID, detail ProvisioningAudit) error {
	event := audit.NewDomainEvent(ctx, tenant, eventType, "channel", channel)
	event.Detail["project_id"] = project.String()
	event.Detail["reason"] = detail.Reason
	event.Detail["evidence_refs"] = append([]string{}, detail.EvidenceRefs...)
	event.Detail["authority_source"] = detail.AuthoritySource
	if member > 0 {
		event.Detail["member_principal_id"] = member.String()
		event.Detail["source"] = "DERIVED"
	}
	return m.audit(ctx, tx, event)
}
