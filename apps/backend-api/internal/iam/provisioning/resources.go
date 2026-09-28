package provisioning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/passwordx"
)

func (s *Service) CreateUser(ctx context.Context, auth AuthContext, input UserInput) (UserResource, error) {
	submitted := input
	input, active, err := normalizeUserInput(input)
	if err != nil {
		return UserResource{}, err
	}
	var resourceID guid.ID
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
			return err
		}
		fields, err := s.profileConstraint(ctx, auth.TenantID, auth.PrincipalID)
		if err != nil {
			return err
		}
		if err := requireUserCreateFields(submitted, fields); err != nil {
			return err
		}
		if input.ExternalID != "" {
			mapping, err := repo.getResourceByExternalKey(ctx, auth.DirectoryID, ResourceUser, externalKey(input.ExternalID, 0))
			switch {
			case err == nil && mapping.DeletedAt == nil:
				return ErrResourceConflict
			case err == nil:
				if err := fields.RequireWrite(false, "login_name", "display_name", "email"); err != nil {
					return err
				}
				resourceID = mapping.ResourceID
				if _, err := s.identity.ReplaceProvisionedTo(ctx, repo.writer(), auth.TenantID, resourceID, provisionedUser(input, active, "")); err != nil {
					return err
				}
				now := s.now().UTC().Truncate(time.Millisecond)
				mapping.ExternalID, mapping.ExternalKey, mapping.Version = input.ExternalID, externalKey(input.ExternalID, resourceID), mapping.Version+1
				mapping.UpdatedAt, mapping.DeletedAt = now, nil
				if err := repo.updateResource(ctx, &mapping, mapping.Version-1); err != nil {
					return err
				}
				return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_user_restored", ResourceUser, resourceID)
			case !errors.Is(err, ErrResourceMissing):
				return err
			}
		}
		secret, err := randomTokenSecret()
		if err != nil {
			return err
		}
		hash, err := passwordx.Hash(secret)
		if err != nil {
			return err
		}
		principal, err := s.identity.CreateProvisionedTo(ctx, repo.writer(), auth.TenantID, provisionedUser(input, active, hash))
		if err != nil {
			return err
		}
		resourceID = principal.ID
		now := s.now().UTC().Truncate(time.Millisecond)
		mapping := resourceRow{DirectoryID: auth.DirectoryID, ResourceType: ResourceUser, ResourceID: resourceID, ExternalID: input.ExternalID, ExternalKey: externalKey(input.ExternalID, resourceID), Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := repo.insertResource(ctx, &mapping); err != nil {
			return err
		}
		return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_user_created", ResourceUser, resourceID)
	})
	if err != nil {
		return UserResource{}, fmt.Errorf("create SCIM user: %w", err)
	}
	return s.GetUser(ctx, auth, resourceID)
}

func (s *Service) GetUser(ctx context.Context, auth AuthContext, id guid.ID) (UserResource, error) {
	if err := s.repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
		return UserResource{}, err
	}
	fields, err := s.profileConstraint(ctx, auth.TenantID, auth.PrincipalID)
	if err != nil {
		return UserResource{}, err
	}
	record, err := s.repo.user(ctx, auth.DirectoryID, id)
	if err != nil {
		return UserResource{}, err
	}
	return projectUser(userResource(record), fields), nil
}

func (s *Service) ListUsers(ctx context.Context, auth AuthContext, request ListRequest) (ListResponse[UserResource], error) {
	if err := s.repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
		return ListResponse[UserResource]{}, err
	}
	fields, err := s.profileConstraint(ctx, auth.TenantID, auth.PrincipalID)
	if err == nil {
		err = fields.RequireRead("login_name") // The SQL query orders by login name.
	}
	if err == nil {
		err = requireUserFilterFields(request.Filter, fields)
	}
	if err != nil {
		return ListResponse[UserResource]{}, err
	}
	rows, total, err := s.repo.userPage(ctx, auth.DirectoryID, request, normalizeDialect(s.repo.writer()))
	if err != nil {
		return ListResponse[UserResource]{}, err
	}
	resources := make([]UserResource, 0, len(rows))
	for _, row := range rows {
		resources = append(resources, projectUser(userResource(row), fields))
	}
	return ListResponse[UserResource]{Schemas: []string{ListSchema}, TotalResults: total, StartIndex: request.StartIndex, ItemsPerPage: len(resources), Resources: resources}, nil
}

func (s *Service) ReplaceUser(ctx context.Context, auth AuthContext, id guid.ID, input UserInput, expectedVersion int64) (UserResource, error) {
	input, active, err := normalizeUserInput(input)
	if err != nil || id.Zero() {
		return UserResource{}, ErrInvalidSCIM
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
			return err
		}
		fields, err := s.profileConstraint(ctx, auth.TenantID, auth.PrincipalID)
		if err != nil {
			return err
		}
		if err := fields.RequireWrite(false, "login_name", "display_name", "email"); err != nil {
			return err
		}
		return s.replaceUserOn(ctx, repo, auth, id, input, active, expectedVersion)
	})
	if err != nil {
		return UserResource{}, fmt.Errorf("replace SCIM user: %w", err)
	}
	return s.GetUser(ctx, auth, id)
}

// replaceUserOn shares the versioned write between PUT and PATCH. PATCH must
// retain its raw snapshot's version even when the client omits If-Match.
func (s *Service) replaceUserOn(ctx context.Context, repo *Repository, auth AuthContext, id guid.ID, input UserInput, active bool, expectedVersion int64) error {
	mapping, err := repo.getResource(ctx, auth.DirectoryID, ResourceUser, id, false)
	if err != nil {
		return err
	}
	if expectedVersion > 0 && mapping.Version != expectedVersion {
		return ErrResourceVersion
	}
	if _, err := s.identity.ReplaceProvisionedTo(ctx, repo.writer(), auth.TenantID, id, provisionedUser(input, active, "")); err != nil {
		return err
	}
	now := s.now().UTC().Truncate(time.Millisecond)
	previous := mapping.Version
	mapping.ExternalID, mapping.ExternalKey, mapping.Version = input.ExternalID, externalKey(input.ExternalID, id), previous+1
	mapping.UpdatedAt = now
	if err := repo.updateResource(ctx, &mapping, previous); err != nil {
		return err
	}
	return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_user_replaced", ResourceUser, id)
}

func (s *Service) DeleteUser(ctx context.Context, auth AuthContext, id guid.ID, expectedVersion int64) error {
	if id.Zero() {
		return ErrInvalidSCIM
	}
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
			return err
		}
		record, err := repo.user(ctx, auth.DirectoryID, id)
		if err != nil {
			return err
		}
		if expectedVersion > 0 && record.Version != expectedVersion {
			return ErrResourceVersion
		}
		input := identity.ProvisionedPrincipalInput{LoginName: record.LoginName, DisplayName: record.DisplayName, Email: record.Email, Active: false}
		if _, err := s.identity.ReplaceProvisionedTo(ctx, repo.writer(), auth.TenantID, id, input); err != nil {
			return err
		}
		now, previous := s.now().UTC().Truncate(time.Millisecond), record.Version
		record.Version, record.UpdatedAt, record.DeletedAt = previous+1, now, &now
		if err := repo.updateResource(ctx, &record.Resource, previous); err != nil {
			return err
		}
		return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_user_deleted", ResourceUser, id)
	})
	if err != nil {
		return fmt.Errorf("delete SCIM user: %w", err)
	}
	return nil
}

func (s *Service) CreateGroup(ctx context.Context, auth AuthContext, input GroupInput) (GroupResource, error) {
	input, members, err := normalizeGroupInput(input)
	if err != nil {
		return GroupResource{}, err
	}
	var resourceID guid.ID
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
			return err
		}
		if err := requireSCIMUsers(ctx, repo, auth.DirectoryID, members); err != nil {
			return err
		}
		if input.ExternalID != "" {
			mapping, err := repo.getResourceByExternalKey(ctx, auth.DirectoryID, ResourceGroup, externalKey(input.ExternalID, 0))
			switch {
			case err == nil && mapping.DeletedAt == nil:
				return ErrResourceConflict
			case err == nil:
				resourceID = mapping.ResourceID
				if _, err := s.groups.ReplaceProvisionedTo(ctx, repo.writer(), auth.TenantID, resourceID, provisionedGroup(input, members, true)); err != nil {
					return err
				}
				now, previous := s.now().UTC().Truncate(time.Millisecond), mapping.Version
				mapping.ExternalID, mapping.ExternalKey, mapping.Version = input.ExternalID, externalKey(input.ExternalID, resourceID), previous+1
				mapping.UpdatedAt, mapping.DeletedAt = now, nil
				if err := repo.updateResource(ctx, &mapping, previous); err != nil {
					return err
				}
				return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_group_restored", ResourceGroup, resourceID)
			case !errors.Is(err, ErrResourceMissing):
				return err
			}
		}
		group, err := s.groups.CreateProvisionedTo(ctx, repo.writer(), auth.TenantID, provisionedGroup(input, members, true))
		if err != nil {
			return err
		}
		resourceID = group.ID
		now := s.now().UTC().Truncate(time.Millisecond)
		mapping := resourceRow{DirectoryID: auth.DirectoryID, ResourceType: ResourceGroup, ResourceID: resourceID, ExternalID: input.ExternalID, ExternalKey: externalKey(input.ExternalID, resourceID), Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := repo.insertResource(ctx, &mapping); err != nil {
			return err
		}
		return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_group_created", ResourceGroup, resourceID)
	})
	if err != nil {
		return GroupResource{}, fmt.Errorf("create SCIM group: %w", err)
	}
	return s.GetGroup(ctx, auth, resourceID)
}

func (s *Service) GetGroup(ctx context.Context, auth AuthContext, id guid.ID) (GroupResource, error) {
	if err := s.repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
		return GroupResource{}, err
	}
	record, err := s.repo.group(ctx, auth.DirectoryID, id)
	if err != nil {
		return GroupResource{}, err
	}
	members, err := s.repo.groupMembers(ctx, auth.DirectoryID, record.ResourceID)
	if err != nil {
		return GroupResource{}, err
	}
	return groupResource(record, members), nil
}

func (s *Service) ListGroups(ctx context.Context, auth AuthContext, request ListRequest) (ListResponse[GroupResource], error) {
	if err := s.repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
		return ListResponse[GroupResource]{}, err
	}
	rows, total, err := s.repo.groupPage(ctx, auth.DirectoryID, request, normalizeDialect(s.repo.writer()))
	if err != nil {
		return ListResponse[GroupResource]{}, err
	}
	resources := make([]GroupResource, 0, len(rows))
	for _, row := range rows {
		members, err := s.repo.groupMembers(ctx, auth.DirectoryID, row.ResourceID)
		if err != nil {
			return ListResponse[GroupResource]{}, err
		}
		resources = append(resources, groupResource(row, members))
	}
	return ListResponse[GroupResource]{Schemas: []string{ListSchema}, TotalResults: total, StartIndex: request.StartIndex, ItemsPerPage: len(resources), Resources: resources}, nil
}

func (s *Service) ReplaceGroup(ctx context.Context, auth AuthContext, id guid.ID, input GroupInput, expectedVersion int64) (GroupResource, error) {
	input, members, err := normalizeGroupInput(input)
	if err != nil || id.Zero() {
		return GroupResource{}, ErrInvalidSCIM
	}
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
			return err
		}
		mapping, err := repo.getResource(ctx, auth.DirectoryID, ResourceGroup, id, false)
		if err != nil {
			return err
		}
		if expectedVersion > 0 && mapping.Version != expectedVersion {
			return ErrResourceVersion
		}
		if err := requireSCIMUsers(ctx, repo, auth.DirectoryID, members); err != nil {
			return err
		}
		if _, err := s.groups.ReplaceProvisionedTo(ctx, repo.writer(), auth.TenantID, id, provisionedGroup(input, members, true)); err != nil {
			return err
		}
		now, previous := s.now().UTC().Truncate(time.Millisecond), mapping.Version
		mapping.ExternalID, mapping.ExternalKey, mapping.Version = input.ExternalID, externalKey(input.ExternalID, id), previous+1
		mapping.UpdatedAt = now
		if err := repo.updateResource(ctx, &mapping, previous); err != nil {
			return err
		}
		return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_group_replaced", ResourceGroup, id)
	})
	if err != nil {
		return GroupResource{}, fmt.Errorf("replace SCIM group: %w", err)
	}
	return s.GetGroup(ctx, auth, id)
}

func (s *Service) DeleteGroup(ctx context.Context, auth AuthContext, id guid.ID, expectedVersion int64) error {
	if id.Zero() {
		return ErrInvalidSCIM
	}
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.authorizeSCIM(ctx, auth, s.now(), s.actors); err != nil {
			return err
		}
		mapping, err := repo.getResource(ctx, auth.DirectoryID, ResourceGroup, id, false)
		if err != nil {
			return err
		}
		if expectedVersion > 0 && mapping.Version != expectedVersion {
			return ErrResourceVersion
		}
		if _, err := s.groups.DisableProvisionedTo(ctx, repo.writer(), auth.TenantID, id); err != nil {
			return err
		}
		now, previous := s.now().UTC().Truncate(time.Millisecond), mapping.Version
		mapping.Version, mapping.UpdatedAt, mapping.DeletedAt = previous+1, now, &now
		if err := repo.updateResource(ctx, &mapping, previous); err != nil {
			return err
		}
		return s.appendResourceAudit(ctx, repo.writer(), auth, "scim_group_deleted", ResourceGroup, id)
	})
	if err != nil {
		return fmt.Errorf("delete SCIM group: %w", err)
	}
	return nil
}

func provisionedUser(input UserInput, active bool, passwordHash string) identity.ProvisionedPrincipalInput {
	email := ""
	if len(input.Emails) > 0 {
		email = input.Emails[0].Value
	}
	return identity.ProvisionedPrincipalInput{LoginName: input.UserName, DisplayName: input.DisplayName, Email: email, PasswordHash: passwordHash, Active: active}
}

func provisionedGroup(input GroupInput, members []guid.ID, active bool) organization.ProvisionedGroupInput {
	return organization.ProvisionedGroupInput{DisplayName: input.DisplayName, Active: active, MemberIDs: members}
}

func requireSCIMUsers(ctx context.Context, repo *Repository, directoryID guid.ID, ids []guid.ID) error {
	for _, id := range ids {
		row, err := repo.user(ctx, directoryID, id)
		if err != nil {
			if errors.Is(err, ErrResourceMissing) {
				return organization.ErrGroupMemberInactive
			}
			return err
		}
		if row.PrincipalStatus != "active" || row.MembershipStatus != "active" {
			return organization.ErrGroupMemberInactive
		}
	}
	return nil
}

func userResource(record userRecord) UserResource {
	emails := make([]UserEmail, 0, 1)
	if record.Email != "" {
		emails = append(emails, UserEmail{Value: record.Email, Type: "work", Primary: true})
	}
	return UserResource{
		Schemas: []string{UserSchema}, ID: record.ResourceID.String(), ExternalID: record.ExternalID,
		UserName: record.LoginName, Name: &UserName{Formatted: record.DisplayName}, DisplayName: record.DisplayName,
		Active: record.PrincipalStatus == "active" && record.MembershipStatus == "active", Emails: emails,
		Meta: ResourceMeta{ResourceType: ResourceUser, Created: unixTime(record.CreatedAt), LastModified: unixTime(record.UpdatedAt), Location: "/scim/v2/Users/" + record.ResourceID.String(), Version: weakETag(record.Version)},
	}
}

func groupResource(record groupRecord, ids []guid.ID) GroupResource {
	members := make([]SCIMGroupMember, 0, len(ids))
	for _, id := range ids {
		members = append(members, SCIMGroupMember{Value: id.String(), Ref: "/scim/v2/Users/" + id.String()})
	}
	return GroupResource{
		Schemas: []string{GroupSchema}, ID: record.ResourceID.String(), ExternalID: record.ExternalID, DisplayName: record.Name, Members: members,
		Meta: ResourceMeta{ResourceType: ResourceGroup, Created: unixTime(record.CreatedAt), LastModified: unixTime(record.UpdatedAt), Location: "/scim/v2/Groups/" + record.ResourceID.String(), Version: weakETag(record.Version)},
	}
}

func unixTime(value time.Time) time.Time { return value.UTC() }
