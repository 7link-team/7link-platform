package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/policyx"
	"gorm.io/gorm"
)

type GroupService struct {
	repo           *Repository
	members        core.ActiveMemberChecker
	administrators core.AdministratorGuard
	now            func() time.Time
}

func NewGroupService(runtime *appenv.Runtime, appender audit.Appender, members core.ActiveMemberChecker, administrators core.AdministratorGuard, ids guid.Source) *GroupService {
	if runtime == nil || appender == nil || members == nil || administrators == nil || ids == nil {
		panic("group service requires runtime, audit appender, active member checker, administrator guard, and id generator")
	}
	return &GroupService{repo: NewRepository(runtime, ids, appender), members: members, administrators: administrators, now: time.Now}
}

func (s *GroupService) memberActiveOn(ctx context.Context, repo *Repository, ownerID, principalID guid.ID) (bool, error) {
	kind, err := repo.rootKind(ctx, ownerID)
	if err != nil {
		return false, err
	}
	if kind == ScopeTenant {
		return s.members.IsMemberActiveOn(ctx, repo.writer(), ownerID, principalID)
	}
	if kind != ScopePlatform {
		return false, nil
	}
	checker, ok := s.members.(interface {
		IsOperatingPlatformMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
	})
	if !ok {
		return false, ErrUnavailable
	}
	return checker.IsOperatingPlatformMemberActiveOn(ctx, repo.writer(), ownerID, principalID)
}

func (s *GroupService) requireGrantableGroupRoles(ctx context.Context, repo *Repository, ownerID, groupID guid.ID) error {
	if authn.PrincipalIDFromContext(ctx).Zero() {
		return nil
	}
	var roles []guid.ID
	if err := repo.writer().WithContext(ctx).Table(repo.names.GroupRoleBindings.String()).Where("tenant_id = ? AND group_id = ?", ownerID, groupID).Pluck("role_id", &roles).Error; err != nil {
		return err
	}
	if len(roles) == 0 {
		return nil
	}
	checker, ok := s.administrators.(interface {
		RequireGrantableRoles(context.Context, *gorm.DB, guid.ID, []guid.ID) error
	})
	if !ok {
		return ErrUnavailable
	}
	return checker.RequireGrantableRoles(ctx, repo.writer(), ownerID, roles)
}

func membershipWindowNarrows(current *groupMemberRow, input GroupMemberWindow) bool {
	return (current.StartsAt == nil || input.StartsAt != nil && !input.StartsAt.Before(*current.StartsAt)) &&
		(current.EndsAt == nil || input.EndsAt != nil && !input.EndsAt.After(*current.EndsAt))
}

func (s *GroupService) List(ctx context.Context, tenantID guid.ID) ([]Group, error) {
	if tenantID.Zero() {
		return nil, ErrGroupInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	rows, err := repo.listGroups(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	result := make([]Group, len(rows))
	for index := range rows {
		result[index] = groupFromRow(&rows[index])
	}
	return result, nil
}

func (s *GroupService) Get(ctx context.Context, tenantID, id guid.ID) (Group, error) {
	if tenantID.Zero() || !validID(id) {
		return Group{}, ErrGroupInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return Group{}, err
	}
	row, err := repo.getGroup(ctx, tenantID, id)
	if err != nil {
		return Group{}, err
	}
	return groupFromRow(row), nil
}

func (s *GroupService) Create(ctx context.Context, tenantID guid.ID, input CreateGroup) (Group, error) {
	tenantID, input, err := normalizeGroupCreate(tenantID, input)
	if err != nil {
		return Group{}, err
	}
	id, err := s.repo.nextID()
	if err != nil {
		return Group{}, fmt.Errorf("generate group id: %w", err)
	}
	now := organizationTime(s.now())
	row := groupRow{TenantID: tenantID, ID: id, Name: input.Name, NameKey: nameKey(input.Name), GroupType: input.Type, RuleJSON: string(input.Rule), Description: input.Description, Status: input.Status, SortOrder: input.SortOrder, Version: 1, CreatedAt: now, UpdatedAt: now}
	event := audit.NewDomainEvent(ctx, tenantID, "group_created", "group", id)
	event.Detail["status"] = input.Status
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.insertGroup(ctx, &row); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Group{}, fmt.Errorf("create group: %w", err)
	}
	return groupFromRow(&row), nil
}

func (s *GroupService) Update(ctx context.Context, tenantID, id guid.ID, input UpdateGroup) (Group, error) {
	tenantID, id, input, err := normalizeGroupUpdate(tenantID, id, input)
	if err != nil {
		return Group{}, err
	}
	now := organizationTime(s.now())
	var updated *groupRow
	event := audit.NewDomainEvent(ctx, tenantID, "group_updated", "group", id)
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		current, err := repo.getGroup(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != input.Version {
			return ErrGroupVersionConflict
		}
		candidate := *current
		updated = &candidate
		if input.Name != nil {
			updated.Name, updated.NameKey = *input.Name, nameKey(*input.Name)
		}
		if input.Description != nil {
			updated.Description = *input.Description
		}
		if input.Status != nil {
			updated.Status = *input.Status
		}
		if input.SortOrder != nil {
			updated.SortOrder = *input.SortOrder
		}
		if input.Rule != nil {
			if current.GroupType != GroupTypeDynamic {
				return ErrGroupRuleType
			}
			updated.RuleJSON = string(*input.Rule)
		}
		changed := updated.Name != current.Name || updated.Description != current.Description || updated.Status != current.Status || updated.SortOrder != current.SortOrder || updated.RuleJSON != current.RuleJSON
		if !changed {
			return nil
		}
		addsAuthority := current.Status != StatusActive && updated.Status == StatusActive
		if updated.Status == StatusActive && updated.RuleJSON != current.RuleJSON {
			addsAuthority = addsAuthority || !memberRuleNarrows(current.RuleJSON, updated.RuleJSON)
		}
		if addsAuthority {
			if err := s.requireGrantableGroupRoles(ctx, repo, tenantID, id); err != nil {
				return err
			}
		}
		var verify func() error
		if current.Status == StatusActive && updated.Status == StatusDisabled || current.RuleJSON != updated.RuleJSON {
			verify, err = s.administrators.Protect(ctx, repo.writer(), tenantID)
			if err != nil {
				return err
			}
		}
		updated.Version, updated.UpdatedAt = current.Version+1, now
		if err := repo.updateGroup(ctx, updated, current.Version); err != nil {
			return err
		}
		if verify != nil {
			if err := verify(); err != nil {
				return err
			}
		}
		event.Detail["name_changed"], event.Detail["description_changed"] = updated.Name != current.Name, updated.Description != current.Description
		event.Detail["status_changed"], event.Detail["sort_order_changed"] = updated.Status != current.Status, updated.SortOrder != current.SortOrder
		event.Detail["membership_rule_changed"] = updated.RuleJSON != current.RuleJSON
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return Group{}, fmt.Errorf("update group: %w", err)
	}
	return groupFromRow(updated), nil
}

func (s *GroupService) Delete(ctx context.Context, tenantID, id guid.ID, version int64) error {
	if tenantID.Zero() || !validID(id) || version < 1 {
		return ErrGroupInvalid
	}
	now := organizationTime(s.now())
	event := audit.NewDomainEvent(ctx, tenantID, "group_deleted", "group", id)
	err := s.repo.runInTx(ctx, func(repo *Repository) error {
		current, err := repo.getGroup(ctx, tenantID, id)
		if err != nil {
			return err
		}
		if current.Version != version {
			return ErrGroupVersionConflict
		}
		if err := repo.deleteGroup(ctx, tenantID, id, version); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return nil
}

func (s *GroupService) ListMembers(ctx context.Context, tenantID, groupID guid.ID) ([]GroupMember, error) {
	if tenantID.Zero() || !validID(groupID) {
		return nil, ErrGroupInvalid
	}
	repo, err := s.repo.pinWriter()
	if err != nil {
		return nil, err
	}
	group, err := repo.getGroup(ctx, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	if group.GroupType == GroupTypeDynamic {
		rows, err := repo.listDynamicGroupCandidates(ctx, tenantID, groupID)
		if err != nil {
			return nil, err
		}
		result := make([]GroupMember, 0, len(rows))
		for _, row := range rows {
			matched, err := policyx.EvaluateMemberRule([]byte(group.RuleJSON), policyx.MemberFacts{Subject: row.PrincipalID.String(), Email: row.Email, DepartmentID: optionalID(row.DepartmentID).String(), Status: row.Status})
			if err != nil {
				return nil, fmt.Errorf("evaluate persisted dynamic group rule: %w", err)
			}
			if matched {
				result = append(result, groupMemberFromRow(&row))
			}
		}
		return result, nil
	}
	rows, err := repo.listGroupMembers(ctx, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	result := make([]GroupMember, len(rows))
	for index := range rows {
		result[index] = groupMemberFromRow(&rows[index])
	}
	return result, nil
}

func (s *GroupService) PutMember(ctx context.Context, tenantID, groupID, principalID guid.ID, input GroupMemberWindow) (GroupMember, error) {
	tenantID, groupID, principalID, input, err := normalizeGroupMember(tenantID, groupID, principalID, input)
	if err != nil {
		return GroupMember{}, err
	}
	now := organizationTime(s.now())
	var result *groupMemberRow
	event := audit.NewDomainEvent(ctx, tenantID, "group_member_assigned", "group", groupID)
	event.Detail["principal_id"] = principalID
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		if err := repo.revisions.Lock(ctx, repo.writer(), tenantID); err != nil {
			return err
		}
		group, err := repo.getGroup(ctx, tenantID, groupID)
		if err != nil {
			return err
		}
		if group.GroupType == GroupTypeDynamic {
			return ErrDynamicGroupMembers
		}
		verify, err := s.administrators.Protect(ctx, repo.writer(), tenantID)
		if err != nil {
			return err
		}
		active, err := s.memberActiveOn(ctx, repo, tenantID, principalID)
		if err != nil {
			return fmt.Errorf("check group member: %w", err)
		}
		if !active {
			return ErrGroupMemberInactive
		}
		current, getErr := repo.getGroupMember(ctx, tenantID, groupID, principalID)
		if getErr == nil && optionalTimesEqual(current.StartsAt, input.StartsAt) && optionalTimesEqual(current.EndsAt, input.EndsAt) {
			result = current
			return verify()
		}
		if getErr != nil && !errors.Is(getErr, ErrGroupMemberNotFound) {
			return getErr
		}
		if getErr != nil || !membershipWindowNarrows(current, input) {
			if err := s.requireGrantableGroupRoles(ctx, repo, tenantID, groupID); err != nil {
				return err
			}
		}
		createdAt := now
		if getErr == nil {
			createdAt = current.CreatedAt
		}
		row := model.GroupMember{TenantID: tenantID, GroupID: groupID, PrincipalID: principalID, StartsAt: input.StartsAt, EndsAt: input.EndsAt, CreatedAt: createdAt, UpdatedAt: now}
		if err := repo.putGroupMember(ctx, &row); err != nil {
			return err
		}
		result, err = repo.getGroupMember(ctx, tenantID, groupID, principalID)
		if err != nil {
			return err
		}
		if err := verify(); err != nil {
			return err
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return GroupMember{}, fmt.Errorf("assign group member: %w", err)
	}
	return groupMemberFromRow(result), nil
}

func (s *GroupService) DeleteMember(ctx context.Context, tenantID, groupID, principalID guid.ID) (bool, error) {
	tenantID, groupID, principalID, _, err := normalizeGroupMember(tenantID, groupID, principalID, GroupMemberWindow{})
	if err != nil {
		return false, err
	}
	now := organizationTime(s.now())
	changed := false
	event := audit.NewDomainEvent(ctx, tenantID, "group_member_removed", "group", groupID)
	event.Detail["principal_id"] = principalID
	err = s.repo.runInTx(ctx, func(repo *Repository) error {
		group, err := repo.getGroup(ctx, tenantID, groupID)
		if err != nil {
			return err
		}
		if group.GroupType == GroupTypeDynamic {
			return ErrDynamicGroupMembers
		}
		verify, err := s.administrators.Protect(ctx, repo.writer(), tenantID)
		if err != nil {
			return err
		}
		changed, err = repo.deleteGroupMember(ctx, tenantID, groupID, principalID)
		if err != nil {
			return err
		}
		if err := verify(); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if err := repo.bumpPolicyRevision(ctx, tenantID, now); err != nil {
			return err
		}
		return repo.appendAudit(ctx, event)
	})
	if err != nil {
		return false, fmt.Errorf("remove group member: %w", err)
	}
	return changed, nil
}

func groupFromRow(row *groupRow) Group {
	return Group{ID: row.ID, TenantID: row.TenantID, Name: row.Name, Type: row.GroupType, Rule: MembershipRule(row.RuleJSON), Description: row.Description, Status: row.Status, SortOrder: row.SortOrder, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
func groupMemberFromRow(row *groupMemberRow) GroupMember {
	return GroupMember{GroupID: row.GroupID, PrincipalID: row.PrincipalID, DisplayName: row.DisplayName, Email: row.Email, StartsAt: row.StartsAt, EndsAt: row.EndsAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func optionalTimesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
