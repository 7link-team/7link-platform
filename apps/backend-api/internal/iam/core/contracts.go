package core

import (
	"context"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	gormx "7link.com/platform/pkg/extension/gorm"
	"gorm.io/gorm"
)

// AuthorizationEvaluator is the policy evaluation capability shared by IAM
// administration and business modules enforcing data constraints.
type AuthorizationEvaluator interface {
	authz.EntityDataPermissionChecker
	authz.FieldPermissionChecker
	// PermissionChecker carries CheckPlatform, which answers for authority that
	// does not come from a tenant membership and that tenant-bulk evaluation
	// therefore reports as absent.
	authz.PermissionChecker
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

// MemberDepartmentReader resolves a member's current active department from
// the writer. A zero ID means no active department assignment.
type MemberDepartmentReader interface {
	ActiveDepartment(context.Context, guid.ID, guid.ID) (guid.ID, error)
}

// MemberTenantResolver validates an explicit tenant or returns the sole active
// membership. Zero means there is no unambiguous choice, never a default tenant.
type MemberTenantResolver func(context.Context, guid.ID, guid.ID) (guid.ID, error)

// AdministratorGuard protects mutations that could remove a tenant's last
// durable administrator. The returned verifier must run before the transaction
// commits.
type AdministratorGuard interface {
	Protect(context.Context, *gorm.DB, guid.ID) (func() error, error)
}

// RoleGrantStore groups the transactional IAM mutations required by governance
// access requests and reviews. The composition root supplies every function.
type RoleGrantStore struct {
	Grant  func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID, guid.ID, time.Time, time.Time) error
	Revoke func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error)
	// RoleGranted reports whether a principal already holds a role, as a
	// permanent member or through an unexpired temporary grant. It reads through
	// the caller's transaction because an access request has to see the grants
	// the write that follows it will be compared against.
	RoleGranted              func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, time.Time) (bool, error)
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

// TenantBootstrapInput describes the first administrator a new tenant needs in
// order to be reachable at all. Every tenant route requires an active
// membership, so a tenant created without one can be administered by nobody.
type TenantBootstrapInput struct {
	TenantID    guid.ID
	OwnerID     guid.ID
	DisplayName string
	Email       string
}

// TenantBootstrapResult reports what the bootstrap wrote, so the caller can
// name the counts in its audit event rather than asserting them from memory.
type TenantBootstrapResult struct {
	AdministratorRoleID guid.ID
	PermissionCount     int
	MenuCount           int
}

// TenantBootstrapper is the narrow tenant-bootstrap capability core publishes
// for the module that owns tenants. Organization runs it inside the same
// transaction that inserts the tenant row, so a tenant and the first principal
// able to administer it commit together or not at all. Organization must never
// write these tables itself.
type TenantBootstrapper struct {
	Bootstrap func(context.Context, *gorm.DB, TenantBootstrapInput, time.Time) (TenantBootstrapResult, error)
	// IsUnclaimed reports whether a tenant still has no member and no role. It
	// is what keeps the repair path create-only: a tenant somebody already
	// administers must never be handed a second owner by a recovery command.
	IsUnclaimed func(context.Context, *gorm.DB, guid.ID) (bool, error)
	// BumpRevision advances the tenant policy revision. A bootstrap grants a
	// role and a membership, so anything caching this tenant answers has to be
	// told they changed. Tenant creation bumps once through its own repository
	// because it has other reasons to; a caller outside organization has none,
	// and this is the only way core lets it say so.
	BumpRevision func(context.Context, *gorm.DB, guid.ID, time.Time) error
}

// DirectoryTableNames are the directory tables core's authorization statements
// JOIN, named field by field.
//
// Core declares this struct rather than receiving the owner's, because the
// import runs the other way: organization is activated after core and imports
// it, so core cannot name organization's type without a cycle. Declaring the
// shape here is the stronger half of the arrangement anyway -- it says exactly
// which tables core needs, and the owner fills that and nothing else.
type DirectoryTableNames struct {
	Groups               gormx.TableName
	GroupMembers         gormx.TableName
	GroupRoleBindings    gormx.TableName
	Positions            gormx.TableName
	PositionMembers      gormx.TableName
	PositionRoleBindings gormx.TableName
	Departments          gormx.TableName
	DepartmentClosure    gormx.TableName
	MemberDepartments    gormx.TableName
	RoleDataScopes       gormx.TableName
	RoleScopeDepartments gormx.TableName
	Tenants              gormx.TableName
}

// DirectoryTableNamesConnector lets the module that owns the directory tables
// fill in the names core declared. The owner is activated after core, so the
// names arrive through this connector rather than as a prefix core would
// otherwise have to apply itself.
type DirectoryTableNamesConnector struct {
	Bind func(DirectoryTableNames) error
}

// PrincipalTableNames is the principal table core JOINs, for the same reason
// and by the same route.
type PrincipalTableNames struct {
	Principals gormx.TableName
}

// PrincipalTableNamesConnector lets the module that owns the principal table
// fill in the name core declared.
type PrincipalTableNamesConnector struct {
	Bind func(PrincipalTableNames) error
}
