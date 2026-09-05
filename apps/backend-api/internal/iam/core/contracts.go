package core

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

// AuthorizationEvaluator is the policy evaluation capability used by the IAM
// administration service itself.
type AuthorizationEvaluator interface {
	CheckBulk(ctx context.Context, tenantID guid.ID, permissions []string, principalID guid.ID) (map[string]bool, error)
	Constraint(ctx context.Context, tenantID guid.ID, permission string, principalID guid.ID) (authz.DataConstraint, error)
	ExplainEntity(ctx context.Context, tenantID, entityID guid.ID, permission string, principalID guid.ID) (authz.Explanation, error)
	ExplainResource(ctx context.Context, tenantID, entityID guid.ID, resourceType string, resourceID guid.ID, permission string, principalID guid.ID) (authz.Explanation, error)
}

// ActiveMemberChecker verifies membership either independently or through a
// caller-owned transaction.
type ActiveMemberChecker interface {
	IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error)
	IsMemberActiveOn(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
}

// AdministratorGuard protects mutations that could remove a tenant's last
// durable administrator. The returned verifier must run before the transaction
// commits.
type AdministratorGuard interface {
	Protect(context.Context, *gorm.DB, guid.ID) (func() error, error)
}

// RoleGrantStore groups the transactional IAM mutations required by governance
// access requests and reviews. The composition root supplies every function.
type RoleGrantStore struct {
	Grant                    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, time.Time, time.Time) error
	Revoke                   func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
	RemovePermanent          func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error)
	RemoveGroupMembership    func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	RemovePositionMembership func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error)
	RemoveEntityRoleBinding  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (bool, error)
}

// PlatformAdministratorStore is the narrow platform-administrator capability
// the composition boundary binds to its own bootstrap transaction. Reading and
// granting share that transaction so a concurrent bootstrap cannot slip a
// second administrator in between the check and the write.
type PlatformAdministratorStore struct {
	List      func(context.Context, *gorm.DB) ([]PlatformAdministrator, error)
	GrantFull func(context.Context, *gorm.DB, guid.ID) (bool, error)
}
