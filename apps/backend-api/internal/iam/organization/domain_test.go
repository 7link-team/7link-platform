package organization

import (
	"strings"
	"testing"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDepartmentNormalization(t *testing.T) {
	t.Parallel()
	tenantID := guid.ID(1)
	gotTenant, input, err := normalizeCreate(tenantID, CreateDepartment{Name: " Engineering ", SortOrder: 10})
	require.NoError(t, err)
	assert.Equal(t, tenantID, gotTenant)
	assert.Equal(t, "Engineering", input.Name)
	assert.Equal(t, StatusActive, input.Status)

	for _, item := range []struct {
		tenant guid.ID
		input  CreateDepartment
	}{
		{0, CreateDepartment{Name: "Engineering"}},
		{tenantID, CreateDepartment{}},
		{tenantID, CreateDepartment{Name: strings.Repeat("x", 129)}},
		{tenantID, CreateDepartment{Name: "bad\nname"}},
		{tenantID, CreateDepartment{Name: "Engineering", ParentID: -1}},
		{tenantID, CreateDepartment{Name: "Engineering", Status: "archived"}},
		{tenantID, CreateDepartment{Name: "Engineering", SortOrder: -1}},
		{tenantID, CreateDepartment{Name: "Engineering", SortOrder: maxSortOrder + 1}},
	} {
		_, _, err := normalizeCreate(item.tenant, item.input)
		assert.ErrorIs(t, err, ErrInvalid)
	}

	name, status, order := " Platform ", StatusDisabled, 5
	_, _, update, err := normalizeUpdate(tenantID, 2, UpdateDepartment{Name: &name, Status: &status, SortOrder: &order, Version: 1})
	require.NoError(t, err)
	assert.Equal(t, "Platform", *update.Name)
	for _, input := range []UpdateDepartment{
		{Version: 1},
		{ParentID: idPointer(-1), Version: 1},
		{Name: stringPointer(" "), Version: 1},
		{Status: stringPointer("archived"), Version: 1},
		{SortOrder: intPointer(-1), Version: 1},
	} {
		_, _, _, err := normalizeUpdate(tenantID, 2, input)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	assert.Equal(t, "engineering", nameKey(" Engineering "))
}

func stringPointer(value string) *string { return &value }
func intPointer(value int) *int          { return &value }
func idPointer(value guid.ID) *guid.ID   { return &value }
