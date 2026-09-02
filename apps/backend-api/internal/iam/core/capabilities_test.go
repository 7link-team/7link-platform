package core_test

import (
	"context"
	"reflect"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFrozenIAMCapabilitiesAreDistinct locks the complete Phase 0 capability
// set. module.Services keys by exact reflect.Type, so provider and consumer
// wiring must use these canonical types rather than structural lookalikes.
func TestFrozenIAMCapabilitiesAreDistinct(t *testing.T) {
	capabilities := []reflect.Type{
		reflect.TypeFor[guid.Source](),
		reflect.TypeFor[audit.Appender](),
		reflect.TypeFor[*authz.Registrar](),
		reflect.TypeFor[core.AuthorizationEvaluator](),
		reflect.TypeFor[authz.PermissionChecker](),
		reflect.TypeFor[core.ActiveMemberChecker](),
		reflect.TypeFor[core.AdministratorGuard](),
		reflect.TypeFor[authn.InvitationCredentials](),
		reflect.TypeFor[identity.InvitationPrincipalCreator](),
		reflect.TypeFor[core.RoleGrantStore](),
		reflect.TypeFor[identity.IdentityProvisioner](),
		reflect.TypeFor[organization.GroupProvisioner](),
		reflect.TypeFor[authn.WebService](),
	}

	require.Len(t, capabilities, 13)
	seen := make(map[reflect.Type]struct{}, len(capabilities))
	for _, capability := range capabilities {
		require.NotNil(t, capability)
		_, duplicate := seen[capability]
		assert.False(t, duplicate, "duplicate capability type %v", capability)
		seen[capability] = struct{}{}
	}
	assert.Equal(t, 6, reflect.TypeFor[core.RoleGrantStore]().NumField())
}

type permissionCheckerFixture interface {
	authz.PermissionChecker
	authz.EntityPermissionChecker
}

var _ permissionCheckerFixture = (*completePermissionChecker)(nil)

type completePermissionChecker struct{}

func (*completePermissionChecker) Check(_ context.Context, _ guid.ID, _ string, _ guid.ID) (bool, error) {
	return false, nil
}

func (*completePermissionChecker) CheckPlatform(_ context.Context, _ string, _ guid.ID) (bool, error) {
	return false, nil
}

func (*completePermissionChecker) CheckEntity(_ context.Context, _, _ guid.ID, _ string, _ guid.ID) (bool, error) {
	return false, nil
}
