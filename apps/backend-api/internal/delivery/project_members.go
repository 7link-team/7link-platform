package delivery

import (
	"context"
	"errors"
	"slices"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var ErrInvalidMember = errors.New("delivery: invalid project member or role")

// projectMembersOn resolves H1 bindings after authorization and tenant seeding.
// It reads through the command transaction and never grants a tenant-wide role.
// Fleet supplies actual Agent role mappings; arbitrary service accounts are rejected.
func (m *Module) projectMembersOn(ctx context.Context, tx *gorm.DB, tenant, creator guid.ID, input []projectMemberInput) ([]projectMember, error) {
	if tx == nil || tx.Statement == nil || tenant <= 0 || creator <= 0 || len(input) > 50 {
		return nil, ErrInvalidArgument
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrInvalidArgument
	}
	if m == nil || m.members == nil || m.principals.ActiveHumanByID == nil || m.roles.RoleIDOn == nil {
		return nil, ErrUnavailable
	}
	requested := make([]projectMemberInput, 0, len(input)+1)
	requested = append(requested, projectMemberInput{PrincipalID: creator, RoleKey: "delivery_lead"})
	requested = append(requested, input...)
	bindings := make([]projectMember, 0, len(requested))
	seen := make(map[projectMemberInput]bool, len(requested))
	for _, member := range requested {
		if member.PrincipalID <= 0 || member.RoleKey == "" {
			return nil, ErrInvalidMember
		}
		if seen[member] {
			continue
		}
		active, err := m.members.IsMemberActiveOn(ctx, tx, tenant, member.PrincipalID)
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, ErrInvalidMember
		}
		principal, human, err := m.principals.ActiveHumanByID(ctx, tx, member.PrincipalID)
		if err != nil {
			return nil, err
		}
		if human {
			if principal.ID != member.PrincipalID {
				return nil, ErrUnavailable
			}
			if member.RoleKey != "delivery_lead" && member.RoleKey != "engineer" {
				return nil, ErrInvalidMember
			}
		} else {
			if m.agents.ByPrincipalOn == nil {
				return nil, ErrUnavailable
			}
			agent, found, err := m.agents.ByPrincipalOn(ctx, tx, tenant, member.PrincipalID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, ErrInvalidMember
			}
			if agent.TenantID != tenant || agent.PrincipalID != member.PrincipalID || agent.ID <= 0 {
				return nil, ErrUnavailable
			}
			if !slices.Contains(agent.IAMRoleKeys, member.RoleKey) {
				return nil, ErrInvalidMember
			}
		}
		role, err := m.roles.RoleIDOn(ctx, tx, tenant, member.RoleKey)
		if err != nil {
			return nil, err
		}
		if role <= 0 {
			return nil, ErrUnavailable
		}
		bindings = append(bindings, projectMember{principalID: member.PrincipalID, roleID: role})
		seen[member] = true
	}
	return bindings, nil
}
