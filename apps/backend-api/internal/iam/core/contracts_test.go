package core

import (
	"context"
	"testing"
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

func (*authorizationEvaluatorFixture) FieldConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.FieldConstraint, error) {
	return authz.FieldConstraint{}, nil
}

func (*authorizationEvaluatorFixture) CheckBulk(context.Context, guid.ID, []string, guid.ID) (map[string]bool, error) {
	return nil, nil
}

func (*authorizationEvaluatorFixture) Constraint(context.Context, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, nil
}

func (*authorizationEvaluatorFixture) EntityConstraint(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
	return authz.DataConstraint{}, nil
}

func (*authorizationEvaluatorFixture) ExplainEntity(context.Context, guid.ID, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, nil
}

func (*authorizationEvaluatorFixture) ExplainResource(context.Context, guid.ID, guid.ID, string, guid.ID, string, guid.ID) (authz.Explanation, error) {
	return authz.Explanation{}, nil
}

type activeMemberCheckerFixture struct{}

func (*activeMemberCheckerFixture) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return false, nil
}

func (*activeMemberCheckerFixture) IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
	return false, nil
}

type administratorGuardFixture struct{}

func (*administratorGuardFixture) Protect(context.Context, *gorm.DB, guid.ID) (func() error, error) {
	return func() error { return nil }, nil
}

func TestContractConsumersUseEveryMethod(t *testing.T) {
	t.Parallel()

	consumeAuthorizationEvaluator(&authorizationEvaluatorFixture{})
	consumeActiveMemberChecker(&activeMemberCheckerFixture{})
	consumeAdministratorGuard(&administratorGuardFixture{})
}

func consumeAuthorizationEvaluator(evaluator AuthorizationEvaluator) {
	ctx := context.Background()
	_, _ = evaluator.CheckBulk(ctx, 0, nil, 0)
	_, _ = evaluator.Constraint(ctx, 0, "", 0)
	_, _ = evaluator.EntityConstraint(ctx, 0, 0, "", 0)
	_, _ = evaluator.ExplainEntity(ctx, 0, 0, "", 0)
	_, _ = evaluator.ExplainResource(ctx, 0, 0, "", 0, "", 0)
}

func consumeActiveMemberChecker(checker ActiveMemberChecker) {
	ctx := context.Background()
	_, _ = checker.IsMemberActive(ctx, 0, 0)
	_, _ = checker.IsMemberActiveOn(ctx, nil, 0, 0)
}

func consumeAdministratorGuard(guard AdministratorGuard) {
	_, _ = guard.Protect(context.Background(), nil, 0)
}
