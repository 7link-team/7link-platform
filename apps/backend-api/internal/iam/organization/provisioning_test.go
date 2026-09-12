package organization

import (
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGroupProvisionerUsesCallerTransaction(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	h.seedMember(t, 1, 10, "Alice")
	h.seedMember(t, 1, 11, "Bob")
	service := newBoundGroupService(t,
		h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix},
		&organizationAdministratorGuard{}, newOrganizationTestIDs(700),
	)
	service.now = func() time.Time { return organizationTestNow }

	var created Group
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = service.CreateProvisionedTo(t.Context(), tx, 1, ProvisionedGroupInput{
			DisplayName: " Directory Operators ", Active: true, MemberIDs: []guid.ID{10, 10, 11},
		})
		if err != nil {
			return err
		}
		var count int64
		require.NoError(t, tx.Table(h.table("iam_group_members")).Where("group_id = ?", created.ID).Count(&count).Error)
		assert.EqualValues(t, 2, count)
		return nil
	}))
	assert.Equal(t, "Directory Operators", created.Name)
	assert.Equal(t, GroupTypeStatic, created.Type)
	assert.Equal(t, StatusActive, created.Status)
	assert.EqualValues(t, 1, h.revision(t, 1))

	replaced, err := service.ReplaceProvisionedTo(t.Context(), h.db, 1, created.ID, ProvisionedGroupInput{
		DisplayName: "Directory Viewers", Active: false, MemberIDs: []guid.ID{11},
	})
	require.NoError(t, err)
	assert.Equal(t, "Directory Viewers", replaced.Name)
	assert.Equal(t, StatusDisabled, replaced.Status)
	members, err := service.ListMembers(t.Context(), 1, created.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.EqualValues(t, 11, members[0].PrincipalID)

	disabled, err := service.DisableProvisionedTo(t.Context(), h.db, 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, replaced.ID, disabled.ID)
	assert.Equal(t, replaced.Status, disabled.Status)
	assert.Equal(t, replaced.Version, disabled.Version, "disabling an already disabled group is idempotent")
	assert.True(t, replaced.UpdatedAt.Equal(disabled.UpdatedAt))
}

func TestGroupProvisionerRollbackAndTenantValidation(t *testing.T) {
	h := newOrganizationHarness(t, "")
	h.seedMember(t, 1, 10, "Alice")
	service := newBoundGroupService(t,
		h.runtime, h.appender, &organizationMemberChecker{},
		&organizationAdministratorGuard{}, newOrganizationTestIDs(800),
	)
	rollback := errors.New("rollback caller")
	err := h.db.Transaction(func(tx *gorm.DB) error {
		_, err := service.CreateProvisionedTo(t.Context(), tx, 1, ProvisionedGroupInput{
			DisplayName: "Operators", Active: true, MemberIDs: []guid.ID{10},
		})
		require.NoError(t, err)
		return rollback
	})
	assert.ErrorIs(t, err, rollback)
	var count int64
	require.NoError(t, h.db.Table("iam_groups").Count(&count).Error)
	assert.Zero(t, count)
	assert.Zero(t, h.revision(t, 1))

	_, err = service.CreateProvisionedTo(t.Context(), nil, 1, ProvisionedGroupInput{DisplayName: "Operators"})
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, err = service.CreateProvisionedTo(t.Context(), h.db, 1, ProvisionedGroupInput{DisplayName: "Operators", MemberIDs: []guid.ID{20}})
	assert.ErrorIs(t, err, ErrGroupMemberInactive)
	_, err = service.CreateProvisionedTo(t.Context(), h.db, 2, ProvisionedGroupInput{DisplayName: "Operators", MemberIDs: []guid.ID{10}})
	assert.ErrorIs(t, err, ErrGroupMemberInactive)

	dynamic, err := service.Create(t.Context(), 1, CreateGroup{
		Name: "Dynamic", Type: GroupTypeDynamic,
		Rule: MembershipRule(`{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`),
	})
	require.NoError(t, err)
	_, err = service.ReplaceProvisionedTo(t.Context(), h.db, 1, dynamic.ID, ProvisionedGroupInput{DisplayName: "Nope"})
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, err = service.DisableProvisionedTo(t.Context(), h.db, 1, dynamic.ID)
	assert.ErrorIs(t, err, ErrGroupInvalid)
}

func TestNormalizeProvisionedGroup(t *testing.T) {
	t.Parallel()
	input, members, err := normalizeProvisionedGroup(ProvisionedGroupInput{
		DisplayName: " Team ", MemberIDs: []guid.ID{3, 2, 3},
	})
	require.NoError(t, err)
	assert.Equal(t, "Team", input.DisplayName)
	assert.Equal(t, []guid.ID{3, 2}, members)
	assert.Equal(t, StatusActive, provisionedGroupStatus(true))
	assert.Equal(t, StatusDisabled, provisionedGroupStatus(false))
	for _, input := range []ProvisionedGroupInput{
		{}, {DisplayName: "Team", MemberIDs: []guid.ID{0}},
		{DisplayName: "Team", MemberIDs: make([]guid.ID, 1001)},
	} {
		_, _, err := normalizeProvisionedGroup(input)
		assert.ErrorIs(t, err, ErrGroupInvalid)
	}
}

func TestDisableProvisionedGroupUsesCallerTransaction(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	guard := &organizationAdministratorGuard{}
	service := newBoundGroupService(t,
		h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix},
		guard, newOrganizationTestIDs(900),
	)
	service.now = func() time.Time { return organizationTestNow }
	created, err := service.CreateProvisionedTo(t.Context(), h.db, 1, ProvisionedGroupInput{
		DisplayName: "Operators", Active: true,
	})
	require.NoError(t, err)

	var disabled Group
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var disableErr error
		disabled, disableErr = service.DisableProvisionedTo(t.Context(), tx, 1, created.ID)
		return disableErr
	}))
	assert.Equal(t, StatusDisabled, disabled.Status)
	assert.EqualValues(t, 2, disabled.Version)
	assert.Equal(t, 1, guard.verified)
	assert.EqualValues(t, 2, h.revision(t, 1))

	for _, call := range []func() error{
		func() error { _, err := service.DisableProvisionedTo(t.Context(), nil, 1, created.ID); return err },
		func() error { _, err := service.DisableProvisionedTo(t.Context(), h.db, 0, created.ID); return err },
		func() error { _, err := service.DisableProvisionedTo(t.Context(), h.db, 1, 0); return err },
	} {
		assert.ErrorIs(t, call(), ErrGroupInvalid)
	}
	_, err = service.DisableProvisionedTo(t.Context(), h.db, 1, 9999)
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestProvisionedGroupReplacementAndGuardFailureRollback(t *testing.T) {
	h := newOrganizationHarness(t, "")
	h.seedMember(t, 1, 10, "Alice")
	h.seedMember(t, 1, 11, "Bob")
	guard := &organizationAdministratorGuard{}
	service := newBoundGroupService(t,
		h.runtime, h.appender, &organizationMemberChecker{}, guard, newOrganizationTestIDs(950),
	)
	service.now = func() time.Time { return organizationTestNow }
	created, err := service.CreateProvisionedTo(t.Context(), h.db, 1, ProvisionedGroupInput{
		DisplayName: "Operators", Active: true, MemberIDs: []guid.ID{10},
	})
	require.NoError(t, err)

	unchanged, err := service.ReplaceProvisionedTo(t.Context(), h.db, 1, created.ID, ProvisionedGroupInput{
		DisplayName: "Operators", Active: true, MemberIDs: []guid.ID{10},
	})
	require.NoError(t, err)
	assert.Equal(t, created.Version, unchanged.Version)
	assert.EqualValues(t, 1, h.revision(t, 1))

	added, err := service.ReplaceProvisionedTo(t.Context(), h.db, 1, created.ID, ProvisionedGroupInput{
		DisplayName: "Operators", Active: true, MemberIDs: []guid.ID{10, 11},
	})
	require.NoError(t, err)
	assert.Equal(t, created.Version, added.Version, "member-only replacement does not alter the group row")
	assert.Zero(t, guard.verified)
	assert.EqualValues(t, 2, h.revision(t, 1))

	guard.verifyErr = assert.AnError
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, replaceErr := service.ReplaceProvisionedTo(t.Context(), tx, 1, created.ID, ProvisionedGroupInput{
			DisplayName: "Renamed", Active: false, MemberIDs: []guid.ID{11},
		})
		return replaceErr
	})
	assert.ErrorIs(t, err, assert.AnError)
	stored, err := service.Get(t.Context(), 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Operators", stored.Name)
	assert.Equal(t, StatusActive, stored.Status)
	members, err := service.ListMembers(t.Context(), 1, created.ID)
	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.EqualValues(t, 2, h.revision(t, 1))

	guard.verifyErr = nil
	guard.err = errors.New("guard unavailable")
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, disableErr := service.DisableProvisionedTo(t.Context(), tx, 1, created.ID)
		return disableErr
	})
	assert.ErrorContains(t, err, "guard unavailable")
	stored, err = service.Get(t.Context(), 1, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusActive, stored.Status)
}
