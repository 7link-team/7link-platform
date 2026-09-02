package identity

import (
	"context"
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

func (*identityProvisionerFixture) EnsureExternalPrincipal(context.Context, *gorm.DB, guid.ID, string, string, time.Time) (guid.ID, bool, error) {
	return 0, false, nil
}
