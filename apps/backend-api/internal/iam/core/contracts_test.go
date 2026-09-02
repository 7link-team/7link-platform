package core

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

var (
	_ AuthorizationEvaluator = (*authorizationEvaluatorFixture)(nil)
	_ ActiveMemberChecker    = (*activeMemberCheckerFixture)(nil)
	_ AdministratorGuard     = (*administratorGuardFixture)(nil)
	_                        = RoleGrantStore{
		Grant: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, time.Time, time.Time) error {
			return nil
		},
		Revoke: func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, nil },
		RemovePermanent: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error) {
			return false, nil
		},
		RemoveGroupMembership: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
			return false, nil
		},
		RemovePositionMembership: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) {
			return false, nil
		},
		RemoveEntityRoleBinding: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error) {
			return false, nil
		},
	}
)

type authorizationEvaluatorFixture struct{}

func (*authorizationEvaluatorFixture) CheckBulk(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) {
	return nil, nil
}

func (*authorizationEvaluatorFixture) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, nil
}

func (*authorizationEvaluatorFixture) ExplainEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, nil
}

func (*authorizationEvaluatorFixture) ExplainResource(context.Context, guid.ID, guid.ID, string, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, nil
}

type activeMemberCheckerFixture struct{}

func (*activeMemberCheckerFixture) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return false, nil
}

type administratorGuardFixture struct{}

func (*administratorGuardFixture) Protect(context.Context, *gorm.DB, guid.ID) (func() error, error) {
	return func() error { return nil }, nil
}
