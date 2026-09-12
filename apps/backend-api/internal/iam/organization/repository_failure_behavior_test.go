package organization

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/organization/model"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type failingOrganizationIDs struct{ id int64 }

func (source failingOrganizationIDs) Next() (int64, error) {
	if source.id <= 0 {
		return source.id, errors.New("id source failed")
	}
	return source.id, nil
}

func TestOrganizationRepositoriesSurfaceDatabaseFailures(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	repo := NewRepository(h.runtime, newOrganizationTestIDs(1), h.appender)
	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	department := departmentRow{TenantID: 1, ID: 2, Name: "Department", NameKey: "department", Status: StatusActive, Version: 1}
	_, err = repo.listDepartments(t.Context(), 1)
	assert.Error(t, err)
	_, err = repo.getDepartment(t.Context(), 1, 2)
	assert.Error(t, err)
	assert.Error(t, repo.insertDepartment(t.Context(), &department))
	assert.Error(t, repo.updateDepartment(t.Context(), &department, 1))
	assert.Error(t, repo.moveDepartment(t.Context(), 1, 2, nil))
	assert.Error(t, repo.deleteDepartment(t.Context(), 1, 2, 1))
	_, err = repo.isDepartmentDescendant(t.Context(), 1, 2, 3)
	assert.Error(t, err)
	_, err = repo.departmentAncestors(t.Context(), 1, 2)
	assert.Error(t, err)
	_, err = repo.departmentDescendants(t.Context(), 1, 2)
	assert.Error(t, err)
	_, err = repo.countRelationshipSubject(t.Context(), 1, "group", 2)
	assert.Error(t, err)

	group := groupRow{TenantID: 1, ID: 2, Name: "Group", NameKey: "group", GroupType: GroupTypeStatic, Status: StatusActive, Version: 1}
	_, err = repo.listGroups(t.Context(), 1)
	assert.Error(t, err)
	_, err = repo.getGroup(t.Context(), 1, 2)
	assert.Error(t, err)
	assert.Error(t, repo.insertGroup(t.Context(), &group))
	assert.Error(t, repo.updateGroup(t.Context(), &group, 1))
	assert.Error(t, repo.deleteGroup(t.Context(), 1, 2, 1))
	_, err = repo.listDynamicGroupCandidates(t.Context(), 1, 2)
	assert.Error(t, err)
	_, err = repo.listGroupMembers(t.Context(), 1, 2)
	assert.Error(t, err)
	_, err = repo.getGroupMember(t.Context(), 1, 2, 3)
	assert.Error(t, err)
	assert.Error(t, repo.putGroupMember(t.Context(), &model.GroupMember{}))
	_, err = repo.deleteGroupMember(t.Context(), 1, 2, 3)
	assert.Error(t, err)

	position := positionRow{TenantID: 1, ID: 2, Code: "position", Name: "Position", Status: StatusActive, Version: 1}
	_, err = repo.listPositions(t.Context(), 1)
	assert.Error(t, err)
	_, err = repo.getPosition(t.Context(), 1, 2)
	assert.Error(t, err)
	assert.Error(t, repo.insertPosition(t.Context(), &position))
	assert.Error(t, repo.updatePosition(t.Context(), &position, 1))
	assert.Error(t, repo.deletePosition(t.Context(), 1, 2, 1))
	_, err = repo.listPositionMembers(t.Context(), 1, 2)
	assert.Error(t, err)
	_, err = repo.getPositionMember(t.Context(), 1, 2, 3)
	assert.Error(t, err)
	assert.Error(t, repo.putPositionMember(t.Context(), &model.PositionMember{}))
	_, err = repo.deletePositionMember(t.Context(), 1, 2, 3)
	assert.Error(t, err)
}

func TestOrganizationRepositoryStateAndConstructorFailures(t *testing.T) {
	h := newOrganizationHarness(t, "")
	assert.Panics(t, func() { NewRepository(nil, newOrganizationTestIDs(1), h.appender) })
	assert.Panics(t, func() { NewRepository(h.runtime, nil, h.appender) })
	assert.Panics(t, func() { NewRepository(h.runtime, newOrganizationTestIDs(1), nil) })
	assert.Panics(t, func() {
		newBoundGroupService(t, nil, h.appender, &organizationMemberChecker{}, &organizationAdministratorGuard{}, newOrganizationTestIDs(1))
	})
	assert.Panics(t, func() {
		newBoundPositionService(t, h.runtime, h.appender, nil, &organizationAdministratorGuard{}, newOrganizationTestIDs(1))
	})
	assert.Panics(t, func() {
		newBoundTenantService(t, h.runtime, nil, newOrganizationTestIDs(1),
			newTenantBootstrapStub().capability(), newTenantOwnerStub().capability())
	})
	assert.Panics(t, func() {
		newBoundInvitationService(t, h.runtime, h.appender, newOrganizationTestIDs(1), nil, func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
			return 1, nil
		})
	})

	var nilRepo *Repository
	assert.Nil(t, nilRepo.writer())
	assert.Empty(t, nilRepo.prefix())
	_, err := nilRepo.on(h.db)
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = nilRepo.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Error(t, nilRepo.runInTx(t.Context(), func(*Repository) error { return nil }))
	assert.Error(t, h.service.repo.runInTx(t.Context(), nil))

	unavailable := &Repository{}
	_, err = unavailable.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	_, err = unavailable.nextID()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, unavailable.appendAudit(t.Context(), audit.DomainEvent{}), errRepositoryUnavailable)
	assert.ErrorIs(t, unavailable.bumpPolicyRevision(t.Context(), 1, organizationTestNow), errRepositoryUnavailable)
	_, err = unavailable.on(nil)
	assert.ErrorIs(t, err, errRepositoryUnavailable)

	failing := NewRepository(h.runtime, failingOrganizationIDs{}, h.appender)
	_, err = failing.nextID()
	assert.ErrorContains(t, err, "id source failed")
	zero := NewRepository(h.runtime, organizationZeroIDs{}, h.appender)
	_, err = zero.nextID()
	assert.ErrorContains(t, err, "must be positive")

	assert.False(t, isSiblingNameViolation(errors.New("connection closed")))
	assert.True(t, isSiblingNameViolation(errors.New("root_name conflict")))
	assert.False(t, isGroupNameViolation(nil))
	assert.False(t, isGroupNameViolation(errors.New("connection closed")))
	assert.True(t, isGroupNameViolation(errors.New("groups_name conflict")))
	assert.False(t, isPositionCodeViolation(errors.New("connection closed")))
	assert.True(t, isPositionCodeViolation(errors.New("positions_code conflict")))
}

type organizationZeroIDs struct{}

func (organizationZeroIDs) Next() (int64, error) { return 0, nil }

func TestOrganizationServicesSurfaceClosedDatabase(t *testing.T) {
	h := newOrganizationHarness(t, "")
	memberChecker := &organizationMemberChecker{}
	guard := &organizationAdministratorGuard{}
	groups := newBoundGroupService(t, h.runtime, h.appender, memberChecker, guard, newOrganizationTestIDs(10))
	positions := newBoundPositionService(t, h.runtime, h.appender, memberChecker, guard, newOrganizationTestIDs(20))
	tenants, _, _ := newTenantServiceForTest(t, h, newOrganizationTestIDs(30))
	invitations := newBoundInvitationService(t,
		h.runtime, h.appender, newOrganizationTestIDs(40), &invitationCredentialFixture{},
		func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
			return 50, nil
		},
	)
	sqlDB, err := h.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = h.service.List(t.Context(), 1)
	assert.Error(t, err)
	_, err = h.service.Get(t.Context(), 1, 2)
	assert.Error(t, err)
	_, err = h.service.Create(t.Context(), 1, CreateDepartment{Name: "Department"})
	assert.Error(t, err)
	assert.Error(t, h.service.Delete(t.Context(), 1, 2, 1))
	_, err = groups.List(t.Context(), 1)
	assert.Error(t, err)
	_, err = groups.Create(t.Context(), 1, CreateGroup{Name: "Group"})
	assert.Error(t, err)
	_, err = positions.List(t.Context(), 1)
	assert.Error(t, err)
	_, err = positions.Create(t.Context(), 1, CreatePosition{Code: "position", Name: "Position"})
	assert.Error(t, err)
	_, err = tenants.List(t.Context(), false)
	assert.Error(t, err)
	_, err = tenants.Get(t.Context(), 1)
	assert.Error(t, err)
	_, err = tenants.Create(t.Context(), newTenantForTest("tenant", "Tenant"))
	assert.Error(t, err)
	assert.Error(t, tenants.Delete(t.Context(), 1, 1))
	_, err = invitations.List(t.Context(), 1)
	assert.Error(t, err)
	_, _, err = invitations.Create(t.Context(), 1, CreateInvitation{Email: "user@example.com"})
	assert.Error(t, err)
	assert.Error(t, EnsureTenant(t.Context(), h.runtime, 1))

	missingRuntime := appenv.NewRuntime(appenv.NewIdentity("organization-test", "1"))
	assert.Error(t, EnsureTenant(t.Context(), missingRuntime, 1))
}

func TestOrderDepartmentsRejectsCorruptRows(t *testing.T) {
	rows := []departmentRow{
		{TenantID: 1, ID: 2, Name: "First", NameKey: "first", Status: StatusActive},
		{TenantID: 1, ID: 2, Name: "Second", NameKey: "second", Status: StatusActive},
	}
	_, err := orderDepartments(rows)
	assert.ErrorIs(t, err, ErrHierarchyCorrupt)
}

func TestGroupAndPositionServicesRejectInvalidReferences(t *testing.T) {
	h := newOrganizationHarness(t, "")
	members := &organizationMemberChecker{}
	guard := &organizationAdministratorGuard{}
	groups := newBoundGroupService(t, h.runtime, h.appender, members, guard, newOrganizationTestIDs(10))
	positions := newBoundPositionService(t, h.runtime, h.appender, members, guard, newOrganizationTestIDs(20))

	_, err := groups.List(t.Context(), 0)
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, err = groups.Get(t.Context(), 1, 0)
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, err = groups.Update(t.Context(), 1, 2, UpdateGroup{})
	assert.ErrorIs(t, err, ErrGroupInvalid)
	assert.ErrorIs(t, groups.Delete(t.Context(), 1, 2, 0), ErrGroupInvalid)
	_, err = groups.ListMembers(t.Context(), 1, 0)
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, err = groups.PutMember(t.Context(), 1, 2, 0, GroupMemberWindow{})
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, err = groups.DeleteMember(t.Context(), 1, 2, 0)
	assert.ErrorIs(t, err, ErrGroupInvalid)

	_, err = positions.List(t.Context(), 0)
	assert.ErrorIs(t, err, ErrPositionInvalid)
	_, err = positions.Get(t.Context(), 1, 0)
	assert.ErrorIs(t, err, ErrPositionInvalid)
	_, err = positions.Update(t.Context(), 1, 2, UpdatePosition{})
	assert.ErrorIs(t, err, ErrPositionInvalid)
	assert.ErrorIs(t, positions.Delete(t.Context(), 1, 2, 0), ErrPositionInvalid)
	_, err = positions.ListMembers(t.Context(), 1, 0)
	assert.ErrorIs(t, err, ErrPositionInvalid)
	_, err = positions.PutMember(t.Context(), 1, 2, 0, PositionMemberWindow{})
	assert.ErrorIs(t, err, ErrPositionInvalid)
	_, err = positions.DeleteMember(t.Context(), 1, 2, 0)
	assert.ErrorIs(t, err, ErrPositionInvalid)

	failingGroups := newBoundGroupService(t, h.runtime, h.appender, members, guard, failingOrganizationIDs{})
	_, err = failingGroups.Create(t.Context(), 1, CreateGroup{Name: "Group"})
	assert.ErrorContains(t, err, "generate group id")
	failingPositions := newBoundPositionService(t, h.runtime, h.appender, members, guard, failingOrganizationIDs{})
	_, err = failingPositions.Create(t.Context(), 1, CreatePosition{Code: "position", Name: "Position"})
	assert.ErrorContains(t, err, "generate position id")
}
