package identity

import (
	"context"
	"reflect"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var (
	_ InvitationPrincipalCreator = func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
		return 0, nil
	}
	_ IdentityProvisioner = (*identityProvisionerFixture)(nil)
)

type identityProvisionerFixture struct{}

func (*identityProvisionerFixture) CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, ProvisionedPrincipalInput) (Principal, error) {
	return Principal{}, nil
}

func (*identityProvisionerFixture) ReplaceProvisionedTo(context.Context, *gorm.DB, guid.ID, guid.ID, ProvisionedPrincipalInput) (Principal, error) {
	return Principal{}, nil
}

func (*identityProvisionerFixture) Get(context.Context, guid.ID, guid.ID) (Principal, error) {
	return Principal{}, nil
}

func (*identityProvisionerFixture) GetProvisioned(context.Context, guid.ID, guid.ID) (ProvisioningPrincipal, error) {
	return ProvisioningPrincipal{}, nil
}

func (*identityProvisionerFixture) EnsureExternalPrincipal(context.Context, *gorm.DB, guid.ID, string, string, time.Time) (guid.ID, bool, error) {
	return 0, false, nil
}

func TestContractConsumersUseEveryMethod(t *testing.T) {
	t.Parallel()

	creator := InvitationPrincipalCreator(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) (guid.ID, error) {
		return 0, nil
	})
	_, _ = creator(context.Background(), nil, 0, "", "", "", "")

	service := IdentityProvisioner(&identityProvisionerFixture{})
	_, _ = service.CreateProvisionedTo(context.Background(), nil, 0, ProvisionedPrincipalInput{})
	_, _ = service.ReplaceProvisionedTo(context.Background(), nil, 0, 0, ProvisionedPrincipalInput{})
	_, _ = service.Get(context.Background(), 0, 0)
	_, _ = service.GetProvisioned(context.Background(), 0, 0)
	_, _, _ = service.EnsureExternalPrincipal(context.Background(), nil, 0, "", "", time.Time{})
}

func TestContractDTOFields(t *testing.T) {
	t.Parallel()

	consumePrincipalFields(Principal{})
	consumeProvisionedPrincipalInputFields(ProvisionedPrincipalInput{})
	assertFieldCount(t, reflect.TypeFor[Principal](), 10)
	assertFieldCount(t, reflect.TypeFor[ProvisionedPrincipalInput](), 5)
	assertJSONTags(t, reflect.TypeFor[Principal](), []string{
		"id",
		"login_name",
		"email,omitempty",
		"email_verified",
		"activation_required",
		"display_name",
		"avatar,omitempty",
		"status",
		"created_at",
		"updated_at",
	})
}

func consumePrincipalFields(value Principal) {
	var id guid.ID = value.ID
	var loginName string = value.LoginName
	var email string = value.Email
	var emailVerified bool = value.EmailVerified
	var activationRequired bool = value.ActivationRequired
	var displayName string = value.DisplayName
	var avatar *Avatar = value.Avatar
	var status string = value.Status
	var createdAt time.Time = value.CreatedAt
	var updatedAt time.Time = value.UpdatedAt
	_, _, _, _, _, _, _, _, _, _ = id, loginName, email, emailVerified, activationRequired, displayName, avatar, status, createdAt, updatedAt
}

func consumeProvisionedPrincipalInputFields(value ProvisionedPrincipalInput) {
	var loginName string = value.LoginName
	var displayName string = value.DisplayName
	var email string = value.Email
	var passwordHash string = value.PasswordHash
	var active bool = value.Active
	_, _, _, _, _ = loginName, displayName, email, passwordHash, active
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
