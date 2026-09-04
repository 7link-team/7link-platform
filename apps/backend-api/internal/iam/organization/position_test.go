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
