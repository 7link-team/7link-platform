package organization

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositionNormalization(t *testing.T) {
	t.Parallel()
	_, input, err := normalizePositionCreate(1, CreatePosition{Code: " Platform.Engineer ", Name: " Platform Engineer "})
	require.NoError(t, err)
	assert.Equal(t, "platform.engineer", input.Code)
	assert.Equal(t, "Platform Engineer", input.Name)
	assert.Equal(t, StatusActive, input.Status)

	for _, input := range []CreatePosition{
		{}, {Code: "1invalid", Name: "Name"}, {Code: "invalid code", Name: "Name"},
		{Code: strings.Repeat("a", maxPositionCodeLength+1), Name: "Name"},
		{Code: "valid", Name: "bad\nname"}, {Code: "valid", Name: "Name", Status: "archived"},
	} {
		_, _, err := normalizePositionCreate(1, input)
		assert.ErrorIs(t, err, ErrPositionInvalid)
	}
	code, name := "Updated_Code", " Updated "
	_, _, update, err := normalizePositionUpdate(1, 2, UpdatePosition{Code: &code, Name: &name, Version: 1})
	require.NoError(t, err)
	assert.Equal(t, "updated_code", *update.Code)
	assert.Equal(t, "Updated", *update.Name)
	status, sortOrder := StatusDisabled, 7
	_, _, update, err = normalizePositionUpdate(1, 2, UpdatePosition{
		Code: &code, Name: &name, Status: &status, SortOrder: &sortOrder, Version: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusDisabled, *update.Status)
	assert.Equal(t, 7, *update.SortOrder)

	invalidCode, invalidName := "1invalid", "bad\nname"
	invalidStatus, invalidSortOrder := "archived", -1
	for _, input := range []UpdatePosition{
		{Version: 1}, {Name: &name}, {Code: &invalidCode, Version: 1},
		{Name: &invalidName, Version: 1}, {Status: &invalidStatus, Version: 1},
		{SortOrder: &invalidSortOrder, Version: 1},
	} {
		_, _, _, err := normalizePositionUpdate(1, 2, input)
		assert.ErrorIs(t, err, ErrPositionInvalid)
	}

	zero := time.Time{}
	_, _, _, _, err = normalizePositionMember(1, 2, 3, PositionMemberWindow{EndsAt: &zero})
	assert.ErrorIs(t, err, ErrPositionInvalid)
}

// TestPositionRepositoryGuardsVersionAndCode is the position half of what
// TestGroupRepositoryGuardsVersionAndNameKey pins for groups: the unique code
// and the expected version are enforced by the statement itself, so a caller
// that reached the repository with a stale row -- or with a code another
// position already holds -- is refused rather than silently clobbering it.
func TestPositionRepositoryGuardsVersionAndCode(t *testing.T) {
	h := newOrganizationHarness(t, "sl_")
	service := newBoundPositionService(t, h.runtime, h.appender, &organizationMemberChecker{prefix: h.prefix}, &organizationAdministratorGuard{}, newOrganizationTestIDs(2100))
	service.now = func() time.Time { return organizationTestNow }
	repo := service.repo
	engineer, err := service.Create(t.Context(), 1, CreatePosition{Code: "engineer", Name: "Engineer"})
	require.NoError(t, err)
	manager, err := service.Create(t.Context(), 1, CreatePosition{Code: "manager", Name: "Manager"})
	require.NoError(t, err)

	_, err = service.Create(t.Context(), 1, CreatePosition{Code: " Engineer ", Name: "Duplicate"})
	assert.ErrorIs(t, err, ErrPositionCodeConflict, "codes are compared after normalization, so casing and padding must not open a second slot")

	collision := positionRow{
		TenantID: 1, ID: manager.ID, Code: engineer.Code, Name: manager.Name, Status: manager.Status,
		SortOrder: manager.SortOrder, Version: manager.Version + 1, UpdatedAt: organizationTestNow,
	}
	assert.ErrorIs(t, repo.updatePosition(t.Context(), &collision, manager.Version), ErrPositionCodeConflict)
	untouched, err := service.Get(t.Context(), 1, manager.ID)
	require.NoError(t, err)
	assert.Equal(t, "manager", untouched.Code)

	stale := positionRow{
		TenantID: 1, ID: engineer.ID, Code: "renamed", Name: "Renamed", Status: engineer.Status,
		SortOrder: engineer.SortOrder, Version: engineer.Version + 1, UpdatedAt: organizationTestNow,
	}
	assert.ErrorIs(t, repo.updatePosition(t.Context(), &stale, engineer.Version+1), ErrPositionVersionConflict)
	unchanged, err := service.Get(t.Context(), 1, engineer.ID)
	require.NoError(t, err)
	assert.Equal(t, "engineer", unchanged.Code, "a write carrying the wrong version must not land")
	assert.Equal(t, engineer.Version, unchanged.Version)

	assert.ErrorIs(t, repo.deletePosition(t.Context(), 1, engineer.ID, engineer.Version+1), ErrPositionVersionConflict)
	survivor, err := service.Get(t.Context(), 1, engineer.ID)
	require.NoError(t, err)
	assert.Equal(t, engineer.ID, survivor.ID, "a delete carrying the wrong version must not remove the row")
}
