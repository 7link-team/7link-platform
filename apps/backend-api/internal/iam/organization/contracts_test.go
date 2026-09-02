package organization

import (
	"context"

	"7link.com/platform/internal/infra/guid"
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
