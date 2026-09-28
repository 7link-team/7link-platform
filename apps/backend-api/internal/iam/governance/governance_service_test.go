package governance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	coremodel "7link.com/platform/internal/iam/core/model"
	governancemodel "7link.com/platform/internal/iam/governance/model"
	organizationmodel "7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var governanceTestNow = time.Date(2026, 9, 3, 20, 0, 0, 123_000_000, time.UTC)

type governanceTestIDs struct{ next atomic.Int64 }

func newGovernanceTestIDs(start int64) *governanceTestIDs {
	ids := &governanceTestIDs{}
	ids.next.Store(start)
	return ids
}

func (ids *governanceTestIDs) Next() (int64, error) { return ids.next.Add(1), nil }

type failingGovernanceIDs struct{ id int64 }

func (ids failingGovernanceIDs) Next() (int64, error) {
	if ids.id <= 0 {
		return ids.id, errors.New("ID source unavailable")
	}
	return ids.id, nil
}

type governanceIDSourceFunc func() (int64, error)

func (next governanceIDSourceFunc) Next() (int64, error) { return next() }

type governanceAuditRow struct {
	ID        uint `gorm:"primaryKey"`
	TenantID  guid.ID
	EventType string
	TargetID  guid.ID
}

// governanceGrantGuard adds the grant capability core's administrator guard
// publishes, recording the roles an approval asked it to judge.
type governanceGrantGuard struct {
	governanceAdministratorGuard
	requireErr error
	required   [][]guid.ID
}

func (g *governanceGrantGuard) RequireGrantableRoles(_ context.Context, _ *gorm.DB, _ guid.ID, roles []guid.ID) error {
	g.required = append(g.required, roles)
	return g.requireErr
}

type governanceHarness struct {
	service       *Service
	guard         *governanceGrantGuard
	runtime       *appenv.Runtime
	db            *gorm.DB
	prefix        string
	mu            sync.Mutex
	failEvent     string
	lastGrantDB   *gorm.DB
	lastAuditDB   *gorm.DB
	lastAdminFail bool
}

func newGovernanceHarness(t *testing.T, prefix string) *governanceHarness {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=on",
		filepath.ToSlash(filepath.Join(t.TempDir(), "governance.sqlite")))
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{
		"primary": {Type: "sqlite", DSN: dsn, Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	runtime := appenv.NewRuntime(appenv.NewIdentity("governance-test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix(prefix))
	h := &governanceHarness{runtime: runtime, db: router.Write(), prefix: prefix, guard: &governanceGrantGuard{}}
	h.migrate(t)
	h.service = NewService(runtime, h.appendAudit, h.grantStore(), h.guard, newGovernanceTestIDs(1000))
	h.service.now = func() time.Time { return governanceTestNow }
	bindForeignTables(t, h.service.repo, prefix)
	return h
}

func (h *governanceHarness) table(name string) string { return h.prefix + name }

func (h *governanceHarness) migrate(t *testing.T) {
	t.Helper()
	models := []struct {
		name string
		row  any
	}{
		{governancemodel.AccessRequest{}.TableName(), &governancemodel.AccessRequest{}},
		{governancemodel.ApprovalStep{}.TableName(), &governancemodel.ApprovalStep{}},
		{governancemodel.AccessReview{}.TableName(), &governancemodel.AccessReview{}},
		{governancemodel.AccessReviewItem{}.TableName(), &governancemodel.AccessReviewItem{}},
		{organizationmodel.Tenant{}.TableName(), &organizationmodel.Tenant{}},
		{coremodel.TenantMember{}.TableName(), &coremodel.TenantMember{}},
		{coremodel.Role{}.TableName(), &coremodel.Role{}},
		{coremodel.RoleMember{}.TableName(), &coremodel.RoleMember{}},
		{coremodel.TemporaryRoleGrant{}.TableName(), &coremodel.TemporaryRoleGrant{}},
		{coremodel.RoleBinding{}.TableName(), &coremodel.RoleBinding{}},
		{coremodel.Entity{}.TableName(), &coremodel.Entity{}},
		{coremodel.PolicyRevision{}.TableName(), &coremodel.PolicyRevision{}},
		{coremodel.Relationship{}.TableName(), &coremodel.Relationship{}},
		{coremodel.ResourceRelationship{}.TableName(), &coremodel.ResourceRelationship{}},
		{organizationmodel.Group{}.TableName(), &organizationmodel.Group{}},
		{organizationmodel.GroupMember{}.TableName(), &organizationmodel.GroupMember{}},
		{organizationmodel.GroupRoleBinding{}.TableName(), &organizationmodel.GroupRoleBinding{}},
		{organizationmodel.Position{}.TableName(), &organizationmodel.Position{}},
		{organizationmodel.PositionMember{}.TableName(), &organizationmodel.PositionMember{}},
		{organizationmodel.PositionRoleBinding{}.TableName(), &organizationmodel.PositionRoleBinding{}},
		{organizationmodel.MemberDepartment{}.TableName(), &organizationmodel.MemberDepartment{}},
		{"governance_audit", &governanceAuditRow{}},
	}
	for _, item := range models {
		require.NoError(t, h.db.Table(h.table(item.name)).AutoMigrate(item.row), item.name)
	}
}

func (h *governanceHarness) appendAudit(ctx context.Context, db *gorm.DB, event audit.DomainEvent) error {
	h.mu.Lock()
	h.lastAuditDB = db
	fail := h.failEvent == event.EventType
	h.mu.Unlock()
	if fail {
		return errors.New("audit unavailable")
	}
	return db.WithContext(ctx).Table(h.table("governance_audit")).Create(&governanceAuditRow{
		TenantID: event.TenantID, EventType: event.EventType, TargetID: event.TargetID,
	}).Error
}

func (h *governanceHarness) grantStore() core.RoleGrantStore {
	track := func(db *gorm.DB) {
		h.mu.Lock()
		h.lastGrantDB = db
		h.mu.Unlock()
	}
	return core.RoleGrantStore{
		Grant: func(ctx context.Context, db *gorm.DB, tenantID, grantID, roleID, principalID, actorID guid.ID, startsAt, endsAt time.Time) error {
			track(db)
			return db.WithContext(ctx).Table(h.table("iam_temporary_role_grants")).Create(&coremodel.TemporaryRoleGrant{
				TenantID: tenantID, ID: grantID, RoleID: roleID, PrincipalID: principalID,
				SourceType: "access_request", SourceID: grantID, StartsAt: startsAt, EndsAt: endsAt,
				CreatedBy: actorID, CreatedAt: startsAt,
			}).Error
		},
		Revoke: func(ctx context.Context, db *gorm.DB, tenantID, grantID guid.ID) (bool, error) {
			track(db)
			result := db.WithContext(ctx).Table(h.table("iam_temporary_role_grants")).
				Where("tenant_id = ? AND id = ?", tenantID, grantID).Delete(&coremodel.TemporaryRoleGrant{})
			return result.RowsAffected > 0, result.Error
		},
		// The message matches the core implementation's, because this harness
		// stands in for it: a fault in the check has to stay recognizable as the
		// check failing rather than as "already granted".
		RoleGranted: func(ctx context.Context, db *gorm.DB, tenantID, roleID, principalID guid.ID, now time.Time) (bool, error) {
			track(db)
			var memberCount, grantCount int64
			if err := db.WithContext(ctx).Table(h.table("iam_role_members")).
				Where("tenant_id = ? AND role_id = ? AND principal_id = ?", tenantID, roleID, principalID).
				Count(&memberCount).Error; err != nil {
				return false, fmt.Errorf("check existing role grant: %w", err)
			}
			if err := db.WithContext(ctx).Table(h.table("iam_temporary_role_grants")).
				Where(
					"tenant_id = ? AND role_id = ? AND principal_id = ? AND starts_at <= ? AND ends_at > ?",
					tenantID, roleID, principalID, now, now,
				).Count(&grantCount).Error; err != nil {
				return false, fmt.Errorf("check existing role grant: %w", err)
			}
			return memberCount+grantCount > 0, nil
		},
		RemovePermanent: func(ctx context.Context, db *gorm.DB, tenantID, roleID, principalID guid.ID, createdAt time.Time) (bool, error) {
			track(db)
			h.mu.Lock()
			fail := h.lastAdminFail
			h.mu.Unlock()
			if fail {
				return false, core.ErrLastTenantAdministrator
			}
			result := db.WithContext(ctx).Table(h.table("iam_role_members")).
				Where("tenant_id = ? AND role_id = ? AND principal_id = ? AND created_at = ?", tenantID, roleID, principalID, createdAt).
				Delete(&coremodel.RoleMember{})
			return result.RowsAffected > 0, result.Error
		},
		RemoveGroupMembership: func(ctx context.Context, db *gorm.DB, tenantID, groupID, principalID guid.ID) (bool, error) {
			track(db)
			result := db.WithContext(ctx).Table(h.table("iam_group_members")).Where("tenant_id = ? AND group_id = ? AND principal_id = ?", tenantID, groupID, principalID).Delete(&organizationmodel.GroupMember{})
			return result.RowsAffected > 0, result.Error
		},

		RemoveEntityRoleBinding: func(ctx context.Context, db *gorm.DB, tenantID, entityID, roleID, principalID guid.ID) (bool, error) {
			track(db)
			result := db.WithContext(ctx).Table(h.table("iam_role_bindings")).Where("tenant_id = ? AND scope_id = ? AND role_id = ? AND principal_id = ?", tenantID, entityID, roleID, principalID).Delete(&coremodel.RoleBinding{})
			return result.RowsAffected > 0, result.Error
		},
	}
}

func (h *governanceHarness) seedTenant(t *testing.T, tenantID guid.ID) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_tenants")).Create(&organizationmodel.Tenant{
		ID: tenantID, Slug: "tenant-" + tenantID.String(), Name: "Tenant " + tenantID.String(), Status: "active", Version: 1,
		CreatedAt: governanceTestNow, UpdatedAt: governanceTestNow,
	}).Error)
}

func (h *governanceHarness) seedMember(t *testing.T, tenantID, principalID guid.ID, name string) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Create(&coremodel.TenantMember{
		TenantID: tenantID, PrincipalID: principalID, DisplayName: name, Email: name + "@example.com", Status: "active",
		CreatedAt: governanceTestNow, UpdatedAt: governanceTestNow,
	}).Error)
}

func (h *governanceHarness) seedRole(t *testing.T, tenantID, roleID guid.ID, name string) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_roles")).Create(&coremodel.Role{
		TenantID: tenantID, ID: roleID, Name: name, NameKey: stringsLower(name), Description: name + " role",
		CreatedAt: governanceTestNow, UpdatedAt: governanceTestNow,
	}).Error)
}

func stringsLower(value string) string {
	result := make([]byte, len(value))
	for i := range value {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		result[i] = c
	}
	return string(result)
}

func (h *governanceHarness) count(t *testing.T, table string, where string, args ...any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table(table)).Where(where, args...).Count(&count).Error)
	return count
}

func TestAccessRequestLifecycleTenantIsolationAndAtomicAudit(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	for _, tenantID := range []guid.ID{1, 2} {
		h.seedTenant(t, tenantID)
		h.seedMember(t, tenantID, 10, "Requester")
		h.seedMember(t, tenantID, 11, "Approver")
		h.seedRole(t, tenantID, 20, "Operator")
	}

	request, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "temporary operations access", AccessExpiresAt: governanceTestNow.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPending, request.Status)
	assert.Nil(t, request.DecidedAt)
	assert.Empty(t, request.DecidedBy)
	foreign, err := h.service.List(t.Context(), 2, 0)
	require.NoError(t, err)
	assert.Empty(t, foreign)
	_, err = h.service.Approve(t.Context(), 2, request.ID, 11, "cross tenant")
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = h.service.Approve(t.Context(), 1, request.ID, 10, "self")
	assert.ErrorIs(t, err, ErrSelfApproval)
	approved, err := h.service.Approve(t.Context(), 1, request.ID, 11, "approved")
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, approved.Status)
	assert.Equal(t, guid.ID(11), approved.DecidedBy)
	assert.Equal(t, int64(1), h.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", 1, request.ID))
	h.mu.Lock()
	assert.Same(t, h.lastGrantDB, h.lastAuditDB)
	h.mu.Unlock()

	_, err = h.service.Reject(t.Context(), 1, request.ID, 11, "late")
	assert.ErrorIs(t, err, ErrStateConflict)
	revoked, err := h.service.Revoke(t.Context(), 1, request.ID, 11, "no longer needed")
	require.NoError(t, err)
	assert.Equal(t, StatusRevoked, revoked.Status)
	assert.Zero(t, h.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", 1, request.ID))
	assert.Equal(t, int64(3), h.count(t, "governance_audit", "tenant_id = ?", 1))

	second, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "short request", AccessExpiresAt: governanceTestNow.Add(2 * time.Hour),
	})
	require.NoError(t, err)
	_, err = h.service.Withdraw(t.Context(), 1, second.ID, 11, "not owner")
	assert.ErrorIs(t, err, ErrRequesterOnly)
	withdrawn, err := h.service.Withdraw(t.Context(), 1, second.ID, 10, "changed mind")
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, withdrawn.Status)
}

// Approving a role access request grants the role, so the approver must be able
// to grant it directly; otherwise an approver widens someone's authority past
// their own.
func TestAccessRequestApprovalRequiresTheApproverToGrantTheRole(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedMember(t, 1, 11, "Approver")
	h.seedRole(t, 1, 20, "Operator")
	request, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "temporary operations access", AccessExpiresAt: governanceTestNow.Add(24 * time.Hour),
	})
	require.NoError(t, err)

	h.guard.requireErr = core.ErrPrivilegeEscalation
	_, err = h.service.Approve(t.Context(), 1, request.ID, 11, "approved")
	require.ErrorIs(t, err, core.ErrPrivilegeEscalation)
	assert.Equal(t, [][]guid.ID{{20}}, h.guard.required)
	assert.Zero(t, h.count(t, "iam_temporary_role_grants", "tenant_id = ?", 1), "the refused approval grants nothing")
	items, err := h.service.List(t.Context(), 1, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, StatusPending, items[0].Status, "the request stays open for an approver who can grant it")

	rejected, err := h.service.Reject(t.Context(), 1, request.ID, 11, "rejected")
	require.NoError(t, err, "rejecting grants nothing and needs no grant authority")
	assert.Equal(t, StatusRejected, rejected.Status)
	assert.Len(t, h.guard.required, 1)

	h.guard.requireErr = nil
	second, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "second attempt", AccessExpiresAt: governanceTestNow.Add(24 * time.Hour),
	})
	require.NoError(t, err)
	h.service.administrators = governanceAdministratorGuard{}
	_, err = h.service.Approve(t.Context(), 1, second.ID, 11, "approved")
	require.ErrorIs(t, err, ErrUnavailable, "a guard that cannot judge grantability fails closed")
	h.service.administrators = h.guard
	approved, err := h.service.Approve(t.Context(), 1, second.ID, 11, "approved")
	require.NoError(t, err)
	assert.Equal(t, StatusApproved, approved.Status)
	assert.Equal(t, int64(1), h.count(t, "iam_temporary_role_grants", "tenant_id = ? AND id = ?", 1, second.ID))
}

func TestAccessRequestAuditFailureRollsBackDecisionAndGrant(t *testing.T) {
	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedMember(t, 1, 11, "Approver")
	h.seedRole(t, 1, 20, "Operator")
	request, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "temporary operations access", AccessExpiresAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	h.mu.Lock()
	h.failEvent = "access_request_approved"
	h.mu.Unlock()
	_, err = h.service.Approve(t.Context(), 1, request.ID, 11, "approved")
	assert.ErrorContains(t, err, "audit unavailable")
	var stored governancemodel.AccessRequest
	require.NoError(t, h.db.Table(h.table("iam_access_requests")).Where("tenant_id = ? AND id = ?", 1, request.ID).Take(&stored).Error)
	assert.Equal(t, StatusPending, stored.Status)
	assert.Nil(t, stored.DecidedAt)
	assert.Zero(t, h.count(t, "iam_temporary_role_grants", "tenant_id = ?", 1))
	assert.Zero(t, h.count(t, "iam_policy_revisions", "tenant_id = ?", 1))
}

func TestAccessRequestValidationAndDerivedExpiry(t *testing.T) {
	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedRole(t, 1, 20, "Operator")
	for _, input := range []CreateAccessRequest{
		{},
		{RoleID: 20, Reason: "x", AccessExpiresAt: governanceTestNow.Add(time.Hour)},
		{RoleID: 20, Reason: "valid", AccessExpiresAt: governanceTestNow},
		{RoleID: 20, Reason: "valid", AccessExpiresAt: governanceTestNow.Add(maxAccessDuration + time.Hour)},
	} {
		_, err := h.service.Create(t.Context(), 1, 10, input)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	_, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 99, Reason: "missing role", AccessExpiresAt: governanceTestNow.Add(time.Hour),
	})
	assert.ErrorIs(t, err, ErrRoleNotFound)

	request, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "valid reason", AccessExpiresAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	h.service.now = func() time.Time { return governanceTestNow.Add(requestTTL + time.Hour) }
	items, err := h.service.List(t.Context(), 1, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, StatusExpired, items[0].Status)
	_, err = h.service.Approve(t.Context(), 1, request.ID, 11, "late")
	assert.ErrorIs(t, err, ErrExpired)
}

func TestGovernanceRepositoriesAndServicesFailClosed(t *testing.T) {
	assert.Panics(t, func() { NewRepository(nil, newGovernanceTestIDs(1)) })
	assert.Panics(t, func() { NewRepository(appenv.NewRuntime(appenv.NewIdentity("test", "1")), nil) })
	assert.Panics(t, func() { NewService(nil, nil, core.RoleGrantStore{}, nil, nil) })
	unguarded := newGovernanceHarness(t, "")
	assert.Panics(t, func() {
		NewService(unguarded.runtime, unguarded.appendAudit, unguarded.grantStore(), nil, newGovernanceTestIDs(1))
	})

	var repo *Repository
	assert.Nil(t, repo.writer())
	_, err := repo.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Error(t, repo.runInTx(t.Context(), func(*Repository) error { return nil }))
	assert.Error(t, (&Repository{}).runInTx(t.Context(), nil))
	_, err = (&Repository{}).nextID()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (&Repository{ids: failingGovernanceIDs{}}).nextID()
	assert.ErrorContains(t, err, "ID source unavailable")
	_, err = (&Repository{ids: governanceIDSourceFunc(func() (int64, error) { return 0, nil })}).nextID()
	assert.ErrorContains(t, err, "must be positive")
	assert.ErrorIs(t, (&Repository{}).lockPolicy(t.Context(), 1), errRepositoryUnavailable)
	assert.ErrorIs(t, (&Repository{}).bumpPolicy(t.Context(), 1, governanceTestNow), errRepositoryUnavailable)

	broken := &Service{repo: &Repository{}, now: time.Now}
	var unavailableService *Service
	_, err = unavailableService.RequestableRoles(t.Context(), 1)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = unavailableService.List(t.Context(), 1, 0)
	assert.ErrorIs(t, err, ErrInvalid)
	_, err = broken.RequestableRoles(t.Context(), 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = broken.List(t.Context(), 1, 0)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = broken.ListReviews(t.Context(), 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = broken.GetReview(t.Context(), 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (&Repository{}).getAccessRequest(t.Context(), 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (&Repository{}).getReview(t.Context(), 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = (&Repository{}).getReviewItem(t.Context(), 1, 1, 1)
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	h := newGovernanceHarness(t, "")
	pinned := h.service.repo.withDB(h.db, false)
	got, err := pinned.pinWriter()
	require.NoError(t, err)
	assert.Same(t, pinned, got)
	assert.NoError(t, pinned.withDB(h.db, true).runInTx(t.Context(), func(nested *Repository) error {
		assert.Same(t, h.db, nested.writer())
		return nil
	}))
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedRole(t, 1, 20, "Operator")
	require.NoError(t, h.db.Table(h.table("iam_role_members")).Create(&coremodel.RoleMember{
		TenantID: 1, RoleID: 20, PrincipalID: 10, CreatedAt: governanceTestNow,
	}).Error)
	_, err = h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "already assigned", AccessExpiresAt: governanceTestNow.Add(time.Hour),
	})
	assert.ErrorIs(t, err, ErrAlreadyGranted)
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("tenant_id = ? AND principal_id = ?", 1, 10).Update("status", "disabled").Error)
	_, err = h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "inactive requester", AccessExpiresAt: governanceTestNow.Add(time.Hour),
	})
	assert.ErrorIs(t, err, ErrRequesterInactive)

	assert.Equal(t, Name, (&Module{}).ModuleName())
	assert.NotNil(t, (&Module{}).MigrationSource().FS)
	assert.Len(t, (&Module{}).MigrationMilestones(), 4)
}

func TestAccessRequestReadFailuresAreReported(t *testing.T) {
	t.Run("requestable roles", func(t *testing.T) {
		h := newGovernanceHarness(t, "")
		require.NoError(t, h.db.Migrator().DropTable(h.table("iam_roles")))
		_, err := h.service.RequestableRoles(t.Context(), 1)
		assert.ErrorContains(t, err, "list requestable roles")
	})

	t.Run("access requests", func(t *testing.T) {
		h := newGovernanceHarness(t, "")
		require.NoError(t, h.db.Migrator().DropTable(h.table("iam_access_requests")))
		_, err := h.service.List(t.Context(), 1, 0)
		assert.ErrorContains(t, err, "list access requests")
	})
}

func TestAccessRequestFailurePathsRemainAtomic(t *testing.T) {
	t.Run("create failures", func(t *testing.T) {
		h := newGovernanceHarness(t, "")
		h.seedTenant(t, 1)
		h.seedMember(t, 1, 10, "Requester")
		h.seedRole(t, 1, 20, "Operator")
		input := CreateAccessRequest{RoleID: 20, Reason: "temporary access", AccessExpiresAt: governanceTestNow.Add(time.Hour)}

		h.service.repo.ids = governanceIDSourceFunc(func() (int64, error) { return 0, nil })
		_, err := h.service.Create(t.Context(), 1, 10, input)
		assert.ErrorContains(t, err, "must be positive")

		h.service.repo.ids = newGovernanceTestIDs(2000)
		h.mu.Lock()
		h.failEvent = "access_request_created"
		h.mu.Unlock()
		_, err = h.service.Create(t.Context(), 1, 10, input)
		assert.ErrorContains(t, err, "audit unavailable")
		assert.Zero(t, h.count(t, "iam_access_requests", "tenant_id = ?", 1))
		assert.Zero(t, h.count(t, "iam_approval_steps", "tenant_id = ?", 1))
	})

	t.Run("decision and revocation failures", func(t *testing.T) {
		h := newGovernanceHarness(t, "")
		h.seedTenant(t, 1)
		h.seedMember(t, 1, 10, "Requester")
		h.seedMember(t, 1, 11, "Approver")
		h.seedRole(t, 1, 20, "Operator")

		_, err := h.service.decide(t.Context(), 0, 1, 11, "", StatusApproved)
		assert.ErrorIs(t, err, ErrInvalid)
		_, err = h.service.decide(t.Context(), 1, 1, 11, "", "unknown")
		assert.ErrorIs(t, err, ErrInvalid)
		_, err = h.service.revoke(t.Context(), 0, 1, 11, "", false)
		assert.ErrorIs(t, err, ErrInvalid)
		_, err = h.service.Approve(t.Context(), 1, 999, 11, "missing")
		assert.ErrorIs(t, err, ErrNotFound)
		_, err = h.service.Revoke(t.Context(), 1, 999, 11, "missing")
		assert.ErrorIs(t, err, ErrNotFound)

		request, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
			RoleID: 20, Reason: "temporary access", AccessExpiresAt: governanceTestNow.Add(time.Hour),
		})
		require.NoError(t, err)
		_, err = h.service.Revoke(t.Context(), 1, request.ID, 11, "not approved")
		assert.ErrorIs(t, err, ErrStateConflict)
		_, err = h.service.Approve(t.Context(), 1, request.ID, 11, "approved")
		require.NoError(t, err)

		h.service.now = func() time.Time { return governanceTestNow.Add(2 * time.Hour) }
		_, err = h.service.Revoke(t.Context(), 1, request.ID, 11, "expired")
		assert.ErrorIs(t, err, ErrExpired)
		h.service.now = func() time.Time { return governanceTestNow }

		normalRevoke := h.service.grants.Revoke
		h.service.grants.Revoke = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) {
			return false, errors.New("grant store unavailable")
		}
		_, err = h.service.Revoke(t.Context(), 1, request.ID, 11, "store failure")
		assert.ErrorContains(t, err, "grant store unavailable")
		h.service.grants.Revoke = normalRevoke
		require.NoError(t, h.db.Table(h.table("iam_temporary_role_grants")).
			Where("tenant_id = ? AND id = ?", 1, request.ID).Delete(&coremodel.TemporaryRoleGrant{}).Error)
		_, err = h.service.Revoke(t.Context(), 1, request.ID, 11, "grant missing")
		assert.ErrorIs(t, err, ErrStateConflict)

		var stored governancemodel.AccessRequest
		require.NoError(t, h.db.Table(h.table("iam_access_requests")).Where("tenant_id = ? AND id = ?", 1, request.ID).Take(&stored).Error)
		assert.Equal(t, StatusApproved, stored.Status)
	})
}

func TestAccessRequestConcurrentDecisionsHaveOneTerminalWinner(t *testing.T) {
	h := newGovernanceHarness(t, "")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedMember(t, 1, 11, "Approver")
	h.seedRole(t, 1, 20, "Operator")
	request, err := h.service.Create(t.Context(), 1, 10, CreateAccessRequest{
		RoleID: 20, Reason: "concurrent decision", AccessExpiresAt: governanceTestNow.Add(time.Hour),
	})
	require.NoError(t, err)
	start := make(chan struct{})
	errorsByDecision := make(chan error, 2)
	var wait sync.WaitGroup
	for _, decision := range []string{StatusApproved, StatusRejected} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if decision == StatusApproved {
				_, err := h.service.Approve(context.Background(), 1, request.ID, 11, "approve")
				errorsByDecision <- err
				return
			}
			_, err := h.service.Reject(context.Background(), 1, request.ID, 11, "reject")
			errorsByDecision <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByDecision)
	successes := 0
	for err := range errorsByDecision {
		if err == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes)
	items, err := h.service.List(t.Context(), 1, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Contains(t, []string{StatusApproved, StatusRejected}, items[0].Status)
	assert.Equal(t, int64(2), h.count(t, "governance_audit", "tenant_id = ?", 1))
}

// TestAccessRequestCreationSurfacesStorageFailuresAtEachLookup drops one table
// at a time so every precondition Create checks is exercised on its failure
// path. A storage fault must be reported, never mistaken for "not a member",
// "no such role", or "already granted" — each of those would silently change
// the answer a requester gets.
func TestAccessRequestCreationSurfacesStorageFailuresAtEachLookup(t *testing.T) {
	h := newGovernanceHarness(t, "sl_")
	h.seedTenant(t, 1)
	h.seedMember(t, 1, 10, "Requester")
	h.seedRole(t, 1, 20, "Operator")
	input := CreateAccessRequest{
		RoleID: 20, Reason: "temporary operations access", AccessExpiresAt: governanceTestNow.Add(24 * time.Hour),
	}

	for _, broken := range []struct {
		table   string
		message string
	}{
		{table: "iam_role_members", message: "check existing role grant"},
		{table: "iam_roles", message: "get requestable role"},
		{table: "iam_tenant_members", message: "check access requester membership"},
	} {
		require.NoError(t, h.db.Exec("DROP TABLE "+h.table(broken.table)).Error)
		_, err := h.service.Create(t.Context(), 1, 10, input)
		require.Error(t, err, broken.table)
		assert.ErrorContains(t, err, broken.message)
		assert.NotErrorIs(t, err, ErrRequesterInactive, broken.table)
		assert.NotErrorIs(t, err, ErrRoleNotFound, broken.table)
		assert.NotErrorIs(t, err, ErrAlreadyGranted, broken.table)
	}
}
