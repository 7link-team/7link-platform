package organization

import (
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var _ GroupProvisioner = (*groupProvisionerFixture)(nil)

type groupProvisionerFixture struct{}

func (*groupProvisionerFixture) CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, ProvisionedGroupInput) (Group, error) {
	return Group{}, nil
}

func (*groupProvisionerFixture) ReplaceProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID, ProvisionedGroupInput) (Group, error) {
	return Group{}, nil
}

func (*groupProvisionerFixture) DisableProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID) (Group, error) {
	return Group{}, nil
}

func (*groupProvisionerFixture) Get(context.Context, guid.ID, guid.ID) (Group, error) {
	return Group{}, nil
}

func (*groupProvisionerFixture) ListMembers(context.Context, guid.ID, guid.ID) ([]GroupMember, error) {
	return nil, nil
}

func TestContractConsumersUseEveryMethod(t *testing.T) {
	t.Parallel()

	service := GroupProvisioner(&groupProvisionerFixture{})
	_, _ = service.CreateProvisionedTo(context.Background(), nil, 0, ProvisionedGroupInput{})
	_, _ = service.ReplaceProvisionedTo(context.Background(), nil, 0, 0, ProvisionedGroupInput{})
	_, _ = service.DisableProvisionedTo(context.Background(), nil, 0, 0)
	_, _ = service.Get(context.Background(), 0, 0)
	_, _ = service.ListMembers(context.Background(), 0, 0)
}

func TestContractDTOFields(t *testing.T) {
	t.Parallel()

	consumeGroupFields(Group{})
	consumeGroupMemberFields(GroupMember{})
	consumeProvisionedGroupInputFields(ProvisionedGroupInput{})
	assertFieldCount(t, reflect.TypeFor[Group](), 11)
	assertFieldCount(t, reflect.TypeFor[GroupMember](), 8)
	assertFieldCount(t, reflect.TypeFor[ProvisionedGroupInput](), 3)
	assertJSONTags(t, reflect.TypeFor[Group](), []string{
		"id",
		"tenant_id",
		"name",
		"type",
		"membership_rule,omitempty",
		"description,omitempty",
		"status",
		"sort_order",
		"version",
		"created_at",
		"updated_at",
	})
	assertJSONTags(t, reflect.TypeFor[GroupMember](), []string{
		"group_id",
		"principal_id",
		"display_name",
		"email,omitempty",
		"starts_at,omitempty",
		"ends_at,omitempty",
		"created_at",
		"updated_at",
	})
}

func TestMembershipRuleJSONRoundTrip(t *testing.T) {
	t.Parallel()

	wantRule := `{"version":1,"condition":{"field":"email"}}`
	encoded, err := json.Marshal(Group{Rule: MembershipRule(wantRule)})
	require.NoError(t, err)
	require.JSONEq(t, `{"id":null,"tenant_id":null,"name":"","type":"","membership_rule":`+wantRule+`,"status":"","sort_order":0,"version":0,"created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z"}`, string(encoded))

	var decoded Group
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.JSONEq(t, wantRule, string(decoded.Rule))
}

func TestMembershipRuleRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	var rule MembershipRule
	require.Error(t, rule.UnmarshalJSON([]byte("{")))
}

func TestModuleMigrationContract(t *testing.T) {
	module := &Module{}
	require.Equal(t, Name, module.ModuleName())
	source := module.MigrationSource()
	require.Equal(t, "goose_organization", source.TableName)
	files, err := fs.ReadDir(source.FS, "sql/postgres")
	require.NoError(t, err)
	require.Len(t, files, 9)
	milestones := module.MigrationMilestones()
	require.Len(t, milestones, 3)
	require.Equal(t, "principal-erasure", milestones[2].Name)
	require.Equal(t, int64(9), milestones[2].Version)
	require.Len(t, milestones[2].Dependencies, 1)
	require.Equal(t, "identity", milestones[2].Dependencies[0].Module)
	require.Equal(t, "lifecycle", milestones[2].Dependencies[0].Name)
	require.Equal(t, "base", milestones[0].Name)
	require.Equal(t, int64(7), milestones[0].Version)
	require.Equal(t, "constraints", milestones[1].Name)
	require.Equal(t, int64(8), milestones[1].Version)
	require.Len(t, milestones[1].Dependencies, 1)
	require.Equal(t, "iam", milestones[1].Dependencies[0].Module)
	require.Equal(t, "base", milestones[1].Dependencies[0].Name)
}

func consumeGroupFields(value Group) {
	var id guid.ID = value.ID
	var tenantID guid.ID = value.TenantID
	var name string = value.Name
	var groupType string = value.Type
	var rule MembershipRule = value.Rule
	var description string = value.Description
	var status string = value.Status
	var sortOrder int = value.SortOrder
	var version int64 = value.Version
	var createdAt time.Time = value.CreatedAt
	var updatedAt time.Time = value.UpdatedAt
	_, _, _, _, _, _, _, _, _, _, _ = id, tenantID, name, groupType, rule, description, status, sortOrder, version, createdAt, updatedAt
}

func consumeGroupMemberFields(value GroupMember) {
	var groupID guid.ID = value.GroupID
	var principalID guid.ID = value.PrincipalID
	var displayName string = value.DisplayName
	var email string = value.Email
	var startsAt *time.Time = value.StartsAt
	var endsAt *time.Time = value.EndsAt
	var createdAt time.Time = value.CreatedAt
	var updatedAt time.Time = value.UpdatedAt
	_, _, _, _, _, _, _, _ = groupID, principalID, displayName, email, startsAt, endsAt, createdAt, updatedAt
}

func consumeProvisionedGroupInputFields(value ProvisionedGroupInput) {
	var displayName string = value.DisplayName
	var active bool = value.Active
	var memberIDs []guid.ID = value.MemberIDs
	_, _, _ = displayName, active, memberIDs
}

func assertFieldCount(t *testing.T, typ reflect.Type, expected int) {
	t.Helper()
	if actual := typ.NumField(); actual != expected {
		t.Fatalf("%s field count = %d, want %d", typ, actual, expected)
	}
}

func assertJSONTags(t *testing.T, typ reflect.Type, expected []string) {
	t.Helper()
	if typ.NumField() != len(expected) {
		t.Fatalf("%s field count = %d, want %d JSON tags", typ, typ.NumField(), len(expected))
	}
	for index, want := range expected {
		field := typ.Field(index)
		if got := field.Tag.Get("json"); got != want {
			t.Fatalf("%s.%s JSON tag = %q, want %q", typ, field.Name, got, want)
		}
	}
}
