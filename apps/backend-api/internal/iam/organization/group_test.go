package organization

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupNormalization(t *testing.T) {
	t.Parallel()
	_, input, err := normalizeGroupCreate(1, CreateGroup{Name: " Operators ", Description: " IAM operators "})
	require.NoError(t, err)
	assert.Equal(t, "Operators", input.Name)
	assert.Equal(t, "IAM operators", input.Description)
	assert.Equal(t, GroupTypeStatic, input.Type)
	assert.Equal(t, StatusActive, input.Status)

	rule := MembershipRule(`{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["ACTIVE"]}]}`)
	_, dynamic, err := normalizeGroupCreate(1, CreateGroup{Name: "Dynamic", Type: GroupTypeDynamic, Rule: rule})
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`, string(dynamic.Rule))
	_, _, err = normalizeGroupCreate(1, CreateGroup{Name: "Static", Rule: rule})
	assert.ErrorIs(t, err, ErrGroupRuleType)

	for _, input := range []CreateGroup{
		{}, {Name: "Group", Type: "unknown"}, {Name: "Group", Status: "unknown"},
		{Name: "Group", SortOrder: -1}, {Name: "Group", Description: strings.Repeat("x", maxGroupDescriptionLength+1)},
		{Name: "Dynamic", Type: GroupTypeDynamic},
	} {
		_, _, err := normalizeGroupCreate(1, input)
		assert.Error(t, err)
	}

	name, description := " Updated ", " Description "
	status, sortOrder := StatusDisabled, 10
	updatedRule := MembershipRule(`{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["ACTIVE"]}]}`)
	_, _, update, err := normalizeGroupUpdate(1, 2, UpdateGroup{
		Name: &name, Description: &description, Status: &status, SortOrder: &sortOrder,
		Rule: &updatedRule, Version: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, "Updated", *update.Name)
	assert.Equal(t, "Description", *update.Description)
	assert.JSONEq(t, `{"version":1,"match":"all","conditions":[{"field":"member.status","operator":"in","values":["active"]}]}`, string(*update.Rule))

	invalidName, invalidDescription := "bad\nname", strings.Repeat("x", maxGroupDescriptionLength+1)
	invalidStatus, invalidSortOrder := "archived", -1
	invalidRule := MembershipRule(`{"version":2}`)
	for _, input := range []UpdateGroup{
		{Version: 1}, {Name: &name}, {Name: &invalidName, Version: 1},
		{Description: &invalidDescription, Version: 1}, {Status: &invalidStatus, Version: 1},
		{SortOrder: &invalidSortOrder, Version: 1}, {Rule: &invalidRule, Version: 1},
	} {
		_, _, _, err := normalizeGroupUpdate(1, 2, input)
		assert.Error(t, err)
	}
}

func TestDirectoryMembershipWindowNormalization(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 8, 2, 12, 0, 0, 123456789, time.FixedZone("test", 3600))
	end := start.Add(time.Hour)
	_, _, _, window, err := normalizeGroupMember(1, 2, 3, GroupMemberWindow{StartsAt: &start, EndsAt: &end})
	require.NoError(t, err)
	assert.Equal(t, time.UTC, window.StartsAt.Location())
	assert.Zero(t, window.StartsAt.Nanosecond()%int(time.Millisecond))
	_, _, _, _, err = normalizeGroupMember(1, 2, 3, GroupMemberWindow{StartsAt: &end, EndsAt: &start})
	assert.ErrorIs(t, err, ErrGroupInvalid)
	zero := time.Time{}
	_, _, _, _, err = normalizeGroupMember(1, 2, 3, GroupMemberWindow{StartsAt: &zero})
	assert.ErrorIs(t, err, ErrGroupInvalid)
	_, _, _, _, err = normalizePositionMember(1, 2, 0, PositionMemberWindow{})
	assert.ErrorIs(t, err, ErrPositionInvalid)
}

// TestGroupRepositoryGuardsVersionAndNameKey pins the writes the repository
// refuses on its own account. The service checks the version it just read
// before it calls down, so a stale row never reaches the repository through
// that path -- which is exactly why the guard has to be proven here: it is the
// last thing standing between a concurrent editor whose read went stale and a
// silent overwrite of the edit that won.
func TestGroupRepositoryGuardsVersionAndNameKey(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service := newBoundGroupService(t, h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix}, &organizationAdministratorGuard{}, newOrganizationTestIDs(2000))
	service.now = func() time.Time { return organizationTestNow }
	repo := service.repo
	alpha, err := service.Create(t.Context(), 1, CreateGroup{Name: "Alpha"})
	require.NoError(t, err)
	beta, err := service.Create(t.Context(), 1, CreateGroup{Name: "Beta"})
	require.NoError(t, err)

	stale := groupRow{
		TenantID: 1, ID: alpha.ID, Name: "Renamed", NameKey: nameKey("Renamed"), GroupType: alpha.Type,
		Status: alpha.Status, SortOrder: alpha.SortOrder, Version: alpha.Version + 1, UpdatedAt: organizationTestNow,
	}
	assert.ErrorIs(t, repo.updateGroup(t.Context(), &stale, alpha.Version+1), ErrGroupVersionConflict)
	unchanged, err := service.Get(t.Context(), 1, alpha.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alpha", unchanged.Name, "a write carrying the wrong version must not land")
	assert.Equal(t, alpha.Version, unchanged.Version)

	collision := groupRow{
		TenantID: 1, ID: beta.ID, Name: "Alpha", NameKey: nameKey("Alpha"), GroupType: beta.Type,
		Status: beta.Status, SortOrder: beta.SortOrder, Version: beta.Version + 1, UpdatedAt: organizationTestNow,
	}
	assert.ErrorIs(t, repo.updateGroup(t.Context(), &collision, beta.Version), ErrGroupNameConflict)
	renamed, err := service.Get(t.Context(), 1, beta.ID)
	require.NoError(t, err)
	assert.Equal(t, "Beta", renamed.Name)

	assert.ErrorIs(t, repo.deleteGroup(t.Context(), 1, alpha.ID, alpha.Version+1), ErrGroupVersionConflict)
	survivor, err := service.Get(t.Context(), 1, alpha.ID)
	require.NoError(t, err)
	assert.Equal(t, alpha.ID, survivor.ID, "a delete carrying the wrong version must not remove the row")
}
