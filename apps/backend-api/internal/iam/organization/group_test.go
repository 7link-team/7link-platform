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
