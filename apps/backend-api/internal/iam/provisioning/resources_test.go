package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProvisionedResourceRestoreAndValidationPaths(t *testing.T) {
	env := newProvisioningEnvironment(t)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("restore-user", "restore-user", "restore@example.test"))
	require.NoError(t, err)
	second, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("second-user", "second-user", "second@example.test"))
	require.NoError(t, err)
	_, err = env.service.CreateUser(t.Context(), env.auth, activeUserInput("restore-user", "duplicate", "duplicate@example.test"))
	assert.ErrorIs(t, err, ErrResourceConflict)

	group, err := env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, ExternalID: "restore-group", DisplayName: "Restore Group", Members: []SCIMGroupMember{{Value: user.ID}, {Value: second.ID}}})
	require.NoError(t, err)
	_, err = env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, ExternalID: "restore-group", DisplayName: "Duplicate"})
	assert.ErrorIs(t, err, ErrResourceConflict)

	require.NoError(t, env.service.DeleteUser(t.Context(), env.auth, parseGUID(user.ID), 1))
	restored, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("restore-user", "restore-user", "restore@example.test"))
	require.NoError(t, err)
	assert.Equal(t, user.ID, restored.ID)
	assert.Equal(t, weakETag(3), restored.Meta.Version)
	_, err = env.service.PatchUser(t.Context(), env.auth, parseGUID(restored.ID), PatchRequest{}, 3)
	assert.ErrorIs(t, err, ErrInvalidPath)
	require.NoError(t, env.service.DeleteGroup(t.Context(), env.auth, parseGUID(group.ID), 1))
	restoredGroup, err := env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, ExternalID: "restore-group", DisplayName: "Restored Group", Members: []SCIMGroupMember{{Value: restored.ID}}})
	require.NoError(t, err)
	assert.Equal(t, group.ID, restoredGroup.ID)
	assert.Equal(t, weakETag(3), restoredGroup.Meta.Version)
	_, err = env.service.PatchGroup(t.Context(), env.auth, parseGUID(restoredGroup.ID), PatchRequest{}, 3)
	assert.ErrorIs(t, err, ErrInvalidPath)

	_, err = env.service.CreateUser(t.Context(), env.auth, UserInput{Schemas: []string{UserSchema}})
	assert.ErrorIs(t, err, ErrInvalidSCIM)
	_, err = env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Invalid", Members: []SCIMGroupMember{{Value: wireID("missing")}}})
	assert.ErrorIs(t, err, organization.ErrGroupMemberInactive)
	require.NoError(t, env.db.Table(env.prefix+"iam_principals").Where("id = ?", second.ID).Update("status", "disabled").Error)
	_, err = env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Inactive", Members: []SCIMGroupMember{{Value: second.ID}}})
	assert.ErrorIs(t, err, organization.ErrGroupMemberInactive)
	_, err = env.service.ReplaceUser(t.Context(), env.auth, 0, activeUserInput("x", "x", "x@example.test"), 1)
	assert.ErrorIs(t, err, ErrInvalidSCIM)
	_, err = env.service.ReplaceGroup(t.Context(), env.auth, 0, GroupInput{Schemas: []string{GroupSchema}, DisplayName: "x"}, 1)
	assert.ErrorIs(t, err, ErrInvalidSCIM)
	assert.ErrorIs(t, env.service.DeleteUser(t.Context(), env.auth, 0, 1), ErrInvalidSCIM)
	assert.ErrorIs(t, env.service.DeleteGroup(t.Context(), env.auth, 0, 1), ErrInvalidSCIM)
	_, err = env.service.PatchGroup(t.Context(), env.auth, testID("missing"), PatchRequest{}, 1)
	assert.ErrorIs(t, err, ErrResourceMissing)
}

func TestDirectoryCredentialLifecycleAndFailures(t *testing.T) {
	env := newProvisioningEnvironment(t)
	directories, err := env.service.ListDirectories(t.Context(), env.auth.TenantID)
	require.NoError(t, err)
	directory := directories[0]
	unchanged, err := env.service.ReplaceDirectory(t.Context(), env.auth.TenantID, directory.ID, directory.Name, directory.Status, directory.Version)
	require.NoError(t, err)
	assert.Equal(t, directory.Version, unchanged.Version)
	_, err = env.service.CreateDirectory(t.Context(), testID("missing-tenant"), "Missing")
	assert.ErrorIs(t, err, ErrInvalidDirectory)
	_, err = env.service.CreateDirectory(t.Context(), env.auth.TenantID, "primary idp")
	assert.ErrorIs(t, err, ErrDirectoryName)

	expiresAt := provisioningTestNow.Add(time.Hour)
	expiring, err := env.service.CreateCredential(t.Context(), env.auth.TenantID, env.auth.DirectoryID, "expiring", &expiresAt)
	require.NoError(t, err)
	expiredAt := provisioningTestNow.Add(-time.Second)
	_, err = env.service.CreateCredential(t.Context(), env.auth.TenantID, env.auth.DirectoryID, "expired", &expiredAt)
	assert.ErrorIs(t, err, ErrInvalidDirectory)
	for index := 0; index < maxActiveCredentials-2; index++ {
		_, err = env.service.CreateCredential(t.Context(), env.auth.TenantID, env.auth.DirectoryID, fmt.Sprintf("credential-%d", index), nil)
		require.NoError(t, err)
	}
	_, err = env.service.CreateCredential(t.Context(), env.auth.TenantID, env.auth.DirectoryID, "over-limit", nil)
	assert.ErrorIs(t, err, ErrCredentialLimit)

	env.service.now = func() time.Time { return expiresAt.Add(time.Second) }
	_, err = env.service.Authenticate(t.Context(), "Bearer "+expiring.Token)
	assert.ErrorIs(t, err, ErrUnauthorized)
	env.service.now = func() time.Time { return provisioningTestNow }
	for _, authorization := range []string{"Basic x", "Bearer malformed", "Bearer " + env.auth.CredentialID.String() + ".wrong-secret"} {
		_, err = env.service.Authenticate(t.Context(), authorization)
		assert.ErrorIs(t, err, ErrUnauthorized)
	}
	require.NoError(t, env.service.RevokeCredential(t.Context(), env.auth.TenantID, env.auth.DirectoryID, env.auth.CredentialID))
	_, err = env.service.Authenticate(t.Context(), "Bearer "+env.token)
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.ErrorIs(t, env.service.RevokeCredential(t.Context(), env.auth.TenantID, env.auth.DirectoryID, testID("missing")), ErrCredentialMissing)

	updated, err := env.service.ReplaceDirectory(t.Context(), env.auth.TenantID, directory.ID, directory.Name, DirectoryDisabled, directory.Version)
	require.NoError(t, err)
	_, err = env.service.CreateCredential(t.Context(), env.auth.TenantID, directory.ID, "disabled", nil)
	assert.ErrorIs(t, err, ErrInvalidDirectory)
	_, err = env.service.ReplaceDirectory(t.Context(), env.auth.TenantID, directory.ID, directory.Name, DirectoryActive, directory.Version)
	assert.ErrorIs(t, err, ErrDirectoryVersion)
	assert.Equal(t, DirectoryDisabled, updated.Status)
}

func TestCredentialTenantIsolationIncludesBulkDirectoryAndTarget(t *testing.T) {
	env := newProvisioningEnvironmentWithPrefix(t, "sl_")
	directoryB, err := env.service.CreateDirectory(t.Context(), testID("tenant-b"), "Tenant B")
	require.NoError(t, err)
	credentialB, err := env.service.CreateCredential(t.Context(), testID("tenant-b"), directoryB.ID, "tenant-b", nil)
	require.NoError(t, err)
	authB, err := env.service.Authenticate(t.Context(), "Bearer "+credentialB.Token)
	require.NoError(t, err)
	userB, err := env.service.CreateUser(t.Context(), authB, activeUserInput("tenant-b-user", "tenant-b-user", "b@example.test"))
	require.NoError(t, err)
	groupB, err := env.service.CreateGroup(t.Context(), authB, GroupInput{Schemas: []string{GroupSchema}, DisplayName: "Tenant B Group", Members: []SCIMGroupMember{{Value: userB.ID}}})
	require.NoError(t, err)
	targetB, err := env.service.CreateTarget(t.Context(), testID("tenant-b"), "Tenant B Target", "https://example.test/scim", "secret")
	require.NoError(t, err)

	_, err = env.service.GetUser(t.Context(), env.auth, parseGUID(userB.ID))
	assert.ErrorIs(t, err, ErrResourceMissing)
	_, err = env.service.GetGroup(t.Context(), env.auth, parseGUID(groupB.ID))
	assert.ErrorIs(t, err, ErrResourceMissing)
	_, err = env.service.ListCredentials(t.Context(), env.auth.TenantID, directoryB.ID)
	assert.ErrorIs(t, err, ErrDirectoryMissing)
	_, err = env.service.ReplaceDirectory(t.Context(), env.auth.TenantID, directoryB.ID, "Stolen", DirectoryDisabled, 1)
	assert.ErrorIs(t, err, ErrDirectoryMissing)
	_, err = env.service.ReplaceTarget(t.Context(), env.auth.TenantID, parseGUID(targetB.Target.ID), "Stolen", targetB.Target.BaseURL, TargetDisabled, "", 1)
	assert.ErrorIs(t, err, ErrTargetMissing)
	assert.ErrorIs(t, env.service.DeleteTarget(t.Context(), env.auth.TenantID, parseGUID(targetB.Target.ID)), ErrTargetMissing)

	update, err := json.Marshal(activeUserInput("tenant-b-user", "stolen", "stolen@example.test"))
	require.NoError(t, err)
	bulk, err := env.service.Bulk(t.Context(), env.auth, BulkRequest{Schemas: []string{BulkSchema}, Operations: []BulkOperation{{Method: http.MethodPut, Path: "/Users/" + userB.ID, Version: weakETag(1), Data: update}}})
	require.NoError(t, err)
	require.Len(t, bulk.Operations, 1)
	assert.Equal(t, "404", bulk.Operations[0].Status)
	unchanged, err := env.service.GetUser(t.Context(), authB, parseGUID(userB.ID))
	require.NoError(t, err)
	assert.Equal(t, "tenant-b-user", unchanged.UserName)
	assert.Equal(t, weakETag(1), unchanged.Meta.Version)
}

func TestConcurrentResourceETagUpdateHasOneWinner(t *testing.T) {
	env := newProvisioningEnvironment(t)
	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("concurrent", "concurrent", "concurrent@example.test"))
	require.NoError(t, err)
	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	var wg sync.WaitGroup
	for _, display := range []string{"Winner A", "Winner B"} {
		wg.Add(1)
		go func(display string) {
			defer wg.Done()
			<-start
			input := activeUserInput("concurrent", "concurrent", "concurrent@example.test")
			input.DisplayName = display
			_, err := env.service.ReplaceUser(context.Background(), env.auth, parseGUID(user.ID), input, 1)
			errorsByCall <- err
		}(display)
	}
	close(start)
	wg.Wait()
	close(errorsByCall)
	successes, conflicts := 0, 0
	for err := range errorsByCall {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrResourceVersion) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent update error: %v", err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	current, err := env.service.GetUser(t.Context(), env.auth, parseGUID(user.ID))
	require.NoError(t, err)
	assert.Equal(t, weakETag(2), current.Meta.Version)
}

type failingIdentityProvisioner struct {
	identity.IdentityProvisioner
	err error
}

func (p failingIdentityProvisioner) CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, identity.ProvisionedPrincipalInput) (identity.Principal, error) {
	return identity.Principal{}, p.err
}

type failingGroupProvisioner struct {
	organization.GroupProvisioner
	err error
}

func (p failingGroupProvisioner) CreateProvisionedTo(context.Context, *gorm.DB, guid.ID, organization.ProvisionedGroupInput) (organization.Group, error) {
	return organization.Group{}, p.err
}

func TestProviderFailuresRollBackProvisioningMappings(t *testing.T) {
	env := newProvisioningEnvironment(t)
	providerErr := errors.New("provider unavailable")
	originalIdentity := env.service.identity
	env.service.identity = failingIdentityProvisioner{IdentityProvisioner: originalIdentity, err: providerErr}
	_, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("failed-user", "failed-user", "failed@example.test"))
	assert.ErrorIs(t, err, providerErr)
	var count int64
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_resources").Where("external_id = ?", "failed-user").Count(&count).Error)
	assert.Zero(t, count)

	env.service.identity = originalIdentity
	originalGroups := env.service.groups
	env.service.groups = failingGroupProvisioner{GroupProvisioner: originalGroups, err: providerErr}
	_, err = env.service.CreateGroup(t.Context(), env.auth, GroupInput{Schemas: []string{GroupSchema}, ExternalID: "failed-group", DisplayName: "Failed Group"})
	assert.ErrorIs(t, err, providerErr)
	require.NoError(t, env.db.Table(env.prefix+"iam_scim_resources").Where("external_id = ?", "failed-group").Count(&count).Error)
	assert.Zero(t, count)
}
