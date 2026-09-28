package authz

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRegistryContainsBasicIAM(t *testing.T) {
	r := DefaultRegistry()

	for _, code := range []string{
		"platform_administer",
		"tenant_administer",
		"merchant_view",
		"store_view",
		"user_view",
		"role_view",
		"role_manage_assignee",
		"dept_view",
		"group_manage_member",
		"menu_view",
		"menu_bind_permission",
		"access_review_view",
		"access_review_create",
		"access_review_decide",
		"access_review_manage",
	} {
		_, ok := r.Find(code)
		assert.True(t, ok, "missing %s", code)
	}
}

func TestRegistryRejectsDuplicateCodes(t *testing.T) {
	_, err := NewRegistry(
		Action{Resource: "store", Verb: "view"},
		Action{Resource: "store", Verb: "view"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestRegistryValidation(t *testing.T) {
	cases := []Action{
		{},
		{Resource: "Store", Verb: "view"},
		{Resource: "store", Verb: "View"},
		{Resource: "store", Verb: "view", Code: "store:view"},
		{Resource: "store", Verb: "view", Code: "administer"},
		{Resource: "store", Verb: "view", AllowedRelations: []string{"unknown"}},
		{Resource: "store", Verb: "view", AllowedRelations: []string{"owner", "owner"}},
	}
	for _, tc := range cases {
		_, err := NewRegistry(tc)
		require.Error(t, err)
	}
}

func TestRegistryRelationshipGrants(t *testing.T) {
	action := DefaultRegistry().MustFind("store_view")
	assert.Equal(t, []string{"editor", "owner", "viewer"}, action.AllowedRelations)
	assert.Empty(t, DefaultRegistry().MustFind("store_create").AllowedRelations)
}

func TestRegistryRejectsGeneratedRelationCollision(t *testing.T) {
	_, err := NewRegistry(Action{Resource: "store", Verb: "view_role"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "suffix _role is reserved")
}

func TestRegistryFindSortAndGuardCode(t *testing.T) {
	r, err := NewRegistry(
		Action{Resource: "store", Verb: "update"},
		Action{Resource: "merchant", Verb: "view"},
	)
	require.NoError(t, err)

	assert.Equal(t, "store_view", PermissionCode("store", "view"))
	assert.Equal(t, "store_update", Guard{Resource: "store", Verb: "update"}.Code())

	found, ok := r.Find("merchant_view")
	require.True(t, ok)
	assert.Equal(t, "merchant", found.Resource)
	assert.Equal(t, "merchant_view", r.MustFind("merchant_view").Code)
	assert.Equal(t, []string{"merchant_view", "store_update"}, []string{r.All()[0].Code, r.All()[1].Code})
	assert.Panics(t, func() { r.MustFind("missing") })
}

func TestRegisterInitializesZeroRegistry(t *testing.T) {
	var r Registry
	require.NoError(t, r.Register(Action{Resource: "store", Verb: "view"}))
	_, ok := r.Find("store_view")
	assert.True(t, ok)
}

func TestDefaultRegistryDeclaresNoBusinessModulePermissions(t *testing.T) {
	r := DefaultRegistry()
	for _, action := range r.All() {
		assert.NotEqual(t, "todo", action.Resource, "task permissions belong to the task module")
		assert.NotEqual(t, "file", action.Resource, "file permissions belong to the files module")
		assert.NotEqual(t, "system_file", action.Resource, "file permissions belong to the files module")
	}
	assert.Empty(t, r.Fields("todo"))
}

func TestContributeAddsActionsAndFieldsAsOneUnit(t *testing.T) {
	r := DefaultRegistry()
	before := len(r.All())
	field := Field{Resource: "task", Name: "title", Mutability: "readWrite", Returned: "default", Configurable: true}
	require.NoError(t, r.Contribute([]Action{{Resource: "task", Verb: "view", Scope: ActionScopeTenant}}, []Field{field}))
	action, ok := r.Find("task_view")
	require.True(t, ok)
	assert.Equal(t, ActionScopeTenant, action.Scope)
	_, ok = r.FindField("task", "title")
	assert.True(t, ok)

	// A rejected contribution leaves nothing behind: neither its valid actions
	// nor its fields, whichever declaration failed.
	for name, contribution := range map[string]struct {
		actions []Action
		fields  []Field
	}{
		"duplicate action": {actions: []Action{{Resource: "task", Verb: "export"}, {Resource: "task", Verb: "view"}}},
		"invalid action":   {actions: []Action{{Resource: "task", Verb: "export"}, {Resource: "Task", Verb: "delete"}}},
		"duplicate field":  {actions: []Action{{Resource: "task", Verb: "export"}}, fields: []Field{field}},
		"invalid field":    {actions: []Action{{Resource: "task", Verb: "export"}}, fields: []Field{{Resource: "task", Name: "x"}}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, r.Contribute(contribution.actions, contribution.fields))
			_, exported := r.Find("task_export")
			assert.False(t, exported)
			assert.Len(t, r.All(), before+1)
			assert.Len(t, r.Fields("task"), 1)
		})
	}

	var zero Registry
	require.NoError(t, zero.Contribute([]Action{{Resource: "task", Verb: "view"}}, []Field{field}))
	_, ok = zero.Find("task_view")
	assert.True(t, ok)
	var missing *Registry
	assert.Error(t, missing.Contribute(nil, nil))
}
