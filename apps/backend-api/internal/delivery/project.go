package delivery

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var (
	ErrUnavailable     = errors.New("delivery: provisioning capability unavailable")
	ErrInvalidArgument = errors.New("delivery: invalid project input")
	ErrParentNotClient = errors.New("delivery: parent must be an active client")
)

// projectDraft is internal: the command layer must authorize HUMAN project
// creation, validate the complete request, and resolve seeded role identities
// before entering this write path. Persistence rows never cross module APIs.
type projectDraft struct {
	entity       core.Entity
	client       *projectClientInput
	project      model.Project
	contacts     []model.ProjectContact
	repositories []model.ProjectRepository
	members      []projectMember
	actor        events.Actor
	audit        collab.ProvisioningAudit
}
type projectMember struct{ principalID, roleID guid.ID }
type projectCreated struct {
	entity    core.Entity
	project   model.Project
	channelID guid.ID
	bindings  []core.EntityRoleBinding
}
type projectService struct {
	names    query.TableNames
	entities core.EntityProvisioner
	roles    core.EntityRoleBindingStore
	channels collab.ChannelProvisioner
	audit    audit.Appender
	events   events.Publisher
}

// createOn writes the complete project aggregate through one caller-owned
// transaction. The caller must roll back every returned error, including errors
// after an event publisher has already inserted its outbox row.
func (s *projectService) createOn(ctx context.Context, tx *gorm.DB, draft projectDraft) (projectCreated, error) {
	if tx == nil || tx.Statement == nil {
		return projectCreated{}, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return projectCreated{}, ErrInvalidArgument
	}
	if s == nil || s.names.Projects.IsZero() || s.names.Contacts.IsZero() || s.names.Repositories.IsZero() || s.entities.CreateOn == nil || s.roles.PutOn == nil || s.channels.CreateDefaultOn == nil || s.audit == nil || s.events == nil {
		return projectCreated{}, ErrUnavailable
	}
	tenant := draft.entity.TenantID
	if tenant <= 0 || draft.project.TenantID != tenant || draft.entity.Type != "project" || draft.actor.Kind != "HUMAN" || draft.actor.ID <= 0 || draft.actor.AuthoritySource == "" || draft.audit.AuthoritySource != draft.actor.AuthoritySource {
		return projectCreated{}, ErrInvalidArgument
	}
	// Resolve and validate all child ownership before asking other modules to
	// write. Project IDs are supplied by the newly created entity, never trusted
	// from a client or stale draft.
	for _, contact := range draft.contacts {
		if contact.TenantID != tenant {
			return projectCreated{}, ErrInvalidArgument
		}
	}
	for _, repository := range draft.repositories {
		if repository.TenantID != tenant {
			return projectCreated{}, ErrInvalidArgument
		}
	}
	memberIDs := make([]guid.ID, 0, len(draft.members))
	seen := make(map[guid.ID]bool, len(draft.members))
	for _, member := range draft.members {
		if member.principalID <= 0 || member.roleID <= 0 {
			return projectCreated{}, ErrInvalidArgument
		}
		if !seen[member.principalID] {
			memberIDs = append(memberIDs, member.principalID)
			seen[member.principalID] = true
		}
	}
	if !seen[draft.actor.ID] {
		return projectCreated{}, ErrInvalidArgument
	}
	entityInput := draft.entity
	parentID, err := s.clientOn(ctx, tx, tenant, entityInput.ParentID, draft.client)
	if err != nil {
		return projectCreated{}, err
	}
	entityInput.ParentID = parentID
	entity, err := s.entities.CreateOn(ctx, tx, entityInput)
	if err != nil {
		return projectCreated{}, err
	}
	if entity.ID <= 0 || entity.TenantID != tenant || entity.Type != "project" {
		return projectCreated{}, ErrUnavailable
	}
	project := draft.project
	project.ProjectID = entity.ID
	if entity.ParentID > 0 {
		parent := entity.ParentID
		project.ClientEntityID = &parent
	} else {
		project.ClientEntityID = nil
	}
	if err := query.BindProjects(tx, s.names).WithContext(ctx).Create(&project); err != nil {
		return projectCreated{}, err
	}
	for _, contact := range draft.contacts {
		contact.ProjectID = entity.ID
		if err := query.BindContacts(tx, s.names).WithContext(ctx).Create(&contact); err != nil {
			return projectCreated{}, err
		}
	}
	for _, repository := range draft.repositories {
		repository.ProjectID = entity.ID
		if err := query.BindRepositories(tx, s.names).WithContext(ctx).Create(&repository); err != nil {
			return projectCreated{}, err
		}
	}
	bindings := make([]core.EntityRoleBinding, 0, len(draft.members))
	for _, member := range draft.members {
		binding, _, err := s.roles.PutOn(ctx, tx, tenant, entity.ID, member.roleID, member.principalID, core.BindingAllow, time.Time{})
		if err != nil {
			return projectCreated{}, err
		}
		bindings = append(bindings, binding)
	}
	channelID, err := s.channels.CreateDefaultOn(ctx, tx, collab.DefaultChannelInput{Audit: draft.audit, TenantID: tenant, ProjectID: entity.ID, Name: entity.Name, MemberPrincipalIDs: memberIDs})
	if err != nil {
		return projectCreated{}, err
	}
	if channelID <= 0 {
		return projectCreated{}, ErrUnavailable
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery.project_created", "project", entity.ID)
	event.Detail["authority_source"] = draft.actor.AuthoritySource
	event.Detail["reason"] = draft.audit.Reason
	event.Detail["evidence_refs"] = draft.audit.EvidenceRefs
	event.Detail["code"] = project.Code
	event.Detail["channel_id"] = channelID.String()
	if err := s.audit(ctx, tx, event); err != nil {
		return projectCreated{}, err
	}
	_, err = s.events.Publish(ctx, tx, events.Event{TenantID: tenant, ProjectID: entity.ID, Type: "project.created", Actor: draft.actor, Subject: events.Subject{Kind: "project", ID: entity.ID}, SubjectVersion: int64(project.Version), Data: map[string]any{
		"code": project.Code, "name": entity.Name, "client_entity_id": project.ClientEntityID, "origin_intake_id": project.OriginIntakeID, "team_mode": project.TeamMode, "channel_id": channelID, "member_ids": memberIDs,
	}})
	if err != nil {
		return projectCreated{}, err
	}
	return projectCreated{entity: entity, project: project, channelID: channelID, bindings: bindings}, nil
}

// clientOn keeps optional client creation inside the project transaction. A
// selected parent is always re-read through IAM, including trusted internal
// drafts that supply ParentID directly. No cross-module storage is accessed.
func (s *projectService) clientOn(ctx context.Context, tx *gorm.DB, tenant, parent guid.ID, input *projectClientInput) (guid.ID, error) {
	if input != nil {
		if parent != 0 || (input.EntityID == nil) == (input.NewName == nil) {
			return 0, ErrInvalidArgument
		}
		if input.NewName != nil {
			if *input.NewName == "" {
				return 0, ErrInvalidArgument
			}
			client, err := s.entities.CreateOn(ctx, tx, core.Entity{TenantID: tenant, Type: "client", Name: *input.NewName, Status: core.EntityActive})
			if err != nil {
				return 0, err
			}
			if client.ID <= 0 || client.TenantID != tenant || client.Type != "client" || client.Status != core.EntityActive {
				return 0, ErrUnavailable
			}
			return client.ID, nil
		}
		parent = *input.EntityID
		if parent <= 0 {
			return 0, ErrParentNotClient
		}
	}
	if parent == 0 {
		return 0, nil
	}
	if s.entities.GetOn == nil {
		return 0, ErrUnavailable
	}
	client, err := s.entities.GetOn(ctx, tx, tenant, parent)
	if errors.Is(err, core.ErrEntityNotFound) {
		return 0, ErrParentNotClient
	}
	if err != nil {
		return 0, err
	}
	if client.ID != parent || client.TenantID != tenant || client.Type != "client" || client.Status != core.EntityActive {
		return 0, ErrParentNotClient
	}
	return parent, nil
}
