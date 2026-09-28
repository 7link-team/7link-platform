package organization

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/policyx"
)

func normalizeOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC().Truncate(time.Millisecond)
	return &normalized
}

const (
	GroupTypeStatic           = "static"
	GroupTypeDynamic          = "dynamic"
	maxGroupDescriptionLength = 1024
)

var (
	ErrGroupInvalid           = errors.New("invalid group")
	ErrGroupNotFound          = errors.New("group not found")
	ErrGroupNameConflict      = errors.New("group name already exists")
	ErrGroupVersionConflict   = errors.New("group version conflict")
	ErrGroupHasMembers        = errors.New("group has members")
	ErrGroupRoleBound         = errors.New("group is assigned to a role")
	ErrGroupRelationshipBound = errors.New("group is referenced by a relationship")
	ErrGroupMemberInactive    = errors.New("group member is not an active tenant member")
	ErrGroupMemberNotFound    = errors.New("group member not found")
	ErrGroupRuleInvalid       = errors.New("invalid dynamic group membership rule")
	ErrGroupRuleType          = errors.New("membership rules require a dynamic group")
	ErrDynamicGroupMembers    = errors.New("dynamic group membership is computed from its rule")
)

type CreateGroup struct {
	Name        string
	Type        string
	Rule        MembershipRule
	Description string
	Status      string
	SortOrder   int
}

type UpdateGroup struct {
	Name        *string
	Description *string
	Status      *string
	SortOrder   *int
	Rule        *MembershipRule
	Version     int64
}

type GroupMemberWindow = MembershipWindow

// memberRuleNarrows proves that every future match also matched the old rule.
// Unproven implications require the caller's role grant authority.
func memberRuleNarrows(beforeJSON, afterJSON string) bool {
	var before, after policyx.MemberRule
	if json.Unmarshal([]byte(beforeJSON), &before) != nil || json.Unmarshal([]byte(afterJSON), &after) != nil {
		return false
	}
	implies := func(after, before policyx.MemberCondition) bool {
		if after.Field != before.Field {
			return false
		}
		if after.Operator == "in" {
			for _, value := range after.Values {
				found := slices.Contains(before.Values, value)
				if before.Operator == "in" && !found || before.Operator == "not_in" && found {
					return false
				}
			}
			return true
		}
		if before.Operator != "not_in" {
			return false
		}
		for _, value := range before.Values {
			if !slices.Contains(after.Values, value) {
				return false
			}
		}
		return true
	}
	if after.Match == "all" && before.Match == "all" {
		for _, oldCondition := range before.Conditions {
			if !slices.ContainsFunc(after.Conditions, func(condition policyx.MemberCondition) bool { return implies(condition, oldCondition) }) {
				return false
			}
		}
		return true
	}
	for _, newCondition := range after.Conditions {
		matched := false
		if before.Match == "any" {
			matched = slices.ContainsFunc(before.Conditions, func(condition policyx.MemberCondition) bool { return implies(newCondition, condition) })
		} else {
			matched = !slices.ContainsFunc(before.Conditions, func(condition policyx.MemberCondition) bool { return !implies(newCondition, condition) })
		}
		if after.Match == "all" && matched {
			return true
		}
		if after.Match == "any" && !matched {
			return false
		}
	}
	return after.Match == "any"
}

func normalizeGroupCreate(tenantID guid.ID, input CreateGroup) (guid.ID, CreateGroup, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Type == "" {
		input.Type = GroupTypeStatic
	}
	if input.Status == "" {
		input.Status = StatusActive
	}
	if tenantID.Zero() || !validName(input.Name) || (input.Type != GroupTypeStatic && input.Type != GroupTypeDynamic) || len(input.Description) > maxGroupDescriptionLength || !validStatus(input.Status) || !validSortOrder(input.SortOrder) {
		return 0, CreateGroup{}, ErrGroupInvalid
	}
	if input.Type == GroupTypeStatic {
		if len(strings.TrimSpace(string(input.Rule))) > 0 {
			return 0, CreateGroup{}, ErrGroupRuleType
		}
		input.Rule = nil
		return tenantID, input, nil
	}
	canonical, err := policyx.CanonicalMemberRule(json.RawMessage(input.Rule))
	if err != nil {
		return 0, CreateGroup{}, ErrGroupRuleInvalid
	}
	input.Rule = MembershipRule(canonical)
	return tenantID, input, nil
}

func normalizeGroupUpdate(tenantID, id guid.ID, input UpdateGroup) (guid.ID, guid.ID, UpdateGroup, error) {
	if tenantID.Zero() || id.Zero() || input.Version < 1 || (input.Name == nil && input.Description == nil && input.Status == nil && input.SortOrder == nil && input.Rule == nil) {
		return 0, 0, UpdateGroup{}, ErrGroupInvalid
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if !validName(value) {
			return 0, 0, UpdateGroup{}, ErrGroupInvalid
		}
		input.Name = &value
	}
	if input.Description != nil {
		value := strings.TrimSpace(*input.Description)
		if len(value) > maxGroupDescriptionLength {
			return 0, 0, UpdateGroup{}, ErrGroupInvalid
		}
		input.Description = &value
	}
	if input.Status != nil && !validStatus(*input.Status) {
		return 0, 0, UpdateGroup{}, ErrGroupInvalid
	}
	if input.SortOrder != nil && !validSortOrder(*input.SortOrder) {
		return 0, 0, UpdateGroup{}, ErrGroupInvalid
	}
	if input.Rule != nil {
		canonical, err := policyx.CanonicalMemberRule(json.RawMessage(*input.Rule))
		if err != nil {
			return 0, 0, UpdateGroup{}, ErrGroupRuleInvalid
		}
		value := MembershipRule(canonical)
		input.Rule = &value
	}
	return tenantID, id, input, nil
}

func normalizeGroupMember(tenantID, groupID, principalID guid.ID, input GroupMemberWindow) (guid.ID, guid.ID, guid.ID, GroupMemberWindow, error) {
	if tenantID.Zero() || groupID.Zero() || principalID.Zero() {
		return 0, 0, 0, GroupMemberWindow{}, ErrGroupInvalid
	}
	input.StartsAt = normalizeOptionalTime(input.StartsAt)
	input.EndsAt = normalizeOptionalTime(input.EndsAt)
	if (input.StartsAt != nil && input.StartsAt.IsZero()) || (input.EndsAt != nil && input.EndsAt.IsZero()) || (input.StartsAt != nil && input.EndsAt != nil && !input.EndsAt.After(*input.StartsAt)) {
		return 0, 0, 0, GroupMemberWindow{}, ErrGroupInvalid
	}
	return tenantID, groupID, principalID, input, nil
}
