package provisioning

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/provisioning/model"
	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProvisioningRepositoryConstraintsAndCAS(t *testing.T) {
	env := newProvisioningEnvironment(t)
	repo := NewRepository(env.runtime)
	bindForeignTables(t, repo, env.prefix)
	directories, err := repo.listDirectories(t.Context(), env.auth.TenantID)
	require.NoError(t, err)
	directory, err := repo.getDirectory(t.Context(), env.auth.TenantID, env.auth.DirectoryID)
	require.NoError(t, err)
	require.Len(t, directories, 1)
	_, err = repo.getDirectory(t.Context(), env.auth.TenantID, testID("missing"))
	assert.ErrorIs(t, err, ErrDirectoryMissing)
	duplicate := directory
	duplicate.ID = testID("duplicate-directory")
	assert.ErrorIs(t, repo.insertDirectory(t.Context(), &duplicate), ErrDirectoryName)
	directory.Version++
	assert.ErrorIs(t, repo.updateDirectory(t.Context(), &directory, 99), ErrDirectoryVersion)

	credentials, err := repo.listCredentials(t.Context(), env.auth.DirectoryID)
	require.NoError(t, err)
	authRow, err := repo.credentialForAuth(t.Context(), credentials[0].ID, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, env.auth.TenantID, authRow.TenantID)
	_, err = repo.credentialForAuth(t.Context(), testID("missing"), 0, 0)
	assert.ErrorIs(t, err, ErrUnauthorized)

	user, err := env.service.CreateUser(t.Context(), env.auth, activeUserInput("repo-user", "repo-user", "repo@example.test"))
	require.NoError(t, err)
	resource, err := repo.getResource(t.Context(), env.auth.DirectoryID, ResourceUser, parseGUID(user.ID), false)
	require.NoError(t, err)
	byExternal, err := repo.getResourceByExternalKey(t.Context(), env.auth.DirectoryID, ResourceUser, externalKey("repo-user", 0))
	require.NoError(t, err)
	assert.Equal(t, resource.ResourceID, byExternal.ResourceID)
	assert.ErrorIs(t, repo.insertResource(t.Context(), &resource), ErrResourceConflict)
	resource.Version++
	assert.ErrorIs(t, repo.updateResource(t.Context(), &resource, 99), ErrResourceVersion)

	target, err := env.service.CreateTarget(t.Context(), env.auth.TenantID, "Repository", "https://example.test/scim", "secret", nil)
	require.NoError(t, err)
	targetRow, err := repo.getTarget(t.Context(), env.auth.TenantID, parseGUID(target.Target.ID))
	require.NoError(t, err)
	targetRow.Version++
	assert.ErrorIs(t, repo.replaceTarget(t.Context(), &targetRow, 99), ErrTargetVersion)
	assert.ErrorIs(t, repo.deleteTarget(t.Context(), env.auth.TenantID, testID("missing")), ErrTargetMissing)

	assert.Panics(t, func() { NewRepository(nil) })
	tables, _, err := repo.tables()
	require.NoError(t, err)
	assert.Equal(t, model.Directory{}.TableName(), tables.Directories.TableName())
}

func TestProvisioningRepositoryAvailabilityAndTransactionHelpers(t *testing.T) {
	var nilRepo *Repository
	assert.Nil(t, nilRepo.writer())
	assert.Empty(t, nilRepo.prefix())
	_, err := nilRepo.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Error(t, nilRepo.runInTx(t.Context(), func(*Repository) error { return nil }))

	withoutDatabase := NewRepository(appenv.NewRuntime(appenv.NewIdentity("no-db", "1")))
	assert.Nil(t, withoutDatabase.writer())
	_, err = withoutDatabase.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.ErrorIs(t, withoutDatabase.runInTx(t.Context(), func(*Repository) error { return nil }), errRepositoryUnavailable)

	env := newProvisioningEnvironment(t)
	repo := NewRepository(env.runtime)
	bindForeignTables(t, repo, env.prefix)
	pinned, err := repo.pinWriter()
	require.NoError(t, err)
	assert.Same(t, env.db, pinned.writer())
	txRepo := repo.withDB(env.db, true)
	called := false
	require.NoError(t, txRepo.runInTx(context.Background(), func(actual *Repository) error {
		called = true
		assert.Same(t, txRepo, actual)
		return nil
	}))
	assert.True(t, called)
	assert.Error(t, repo.runInTx(t.Context(), nil))
}

func TestProvisioningRepositorySurfacesStorageFailures(t *testing.T) {
	t.Run("targets", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_targets").Error)
		_, err := repo.listTargets(t.Context(), testID("tenant-a"))
		assert.Error(t, err)
		_, err = repo.getTarget(t.Context(), testID("tenant-a"), testID("target"))
		assert.Error(t, err)
		assert.Error(t, repo.insertTarget(t.Context(), &targetRow{ID: 1}))
		assert.Error(t, repo.replaceTarget(t.Context(), &targetRow{ID: 1, TenantID: 1}, 1))
		assert.Error(t, repo.deleteTarget(t.Context(), 1, 1))
	})
	t.Run("target resources", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_target_resources").Error)
		_, err := repo.listTargetResources(t.Context(), 1)
		assert.Error(t, err)
		_, err = repo.getTargetResource(t.Context(), 1, ResourceUser, 1)
		assert.Error(t, err)
		assert.Error(t, repo.upsertTargetResource(t.Context(), &targetResourceRow{TargetID: 1, ResourceType: ResourceUser, ResourceID: 1}))
		assert.Error(t, repo.markTargetResourceDeleted(t.Context(), 1, ResourceUser, 1, provisioningTestNow))
	})
	t.Run("directories and credentials", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_credentials").Error)
		_, err := repo.listCredentials(t.Context(), 1)
		assert.Error(t, err)
		_, err = repo.activeCredentialCount(t.Context(), 1, provisioningTestNow)
		assert.Error(t, err)
		assert.Error(t, repo.insertCredential(t.Context(), &credentialRow{ID: 1}))
		_, err = repo.revokeCredential(t.Context(), 1, 1, provisioningTestNow)
		assert.Error(t, err)
		_, err = repo.credentialForAuth(t.Context(), 1, 0, 0)
		assert.Error(t, err)
		assert.Error(t, repo.touchCredential(t.Context(), 1, provisioningTestNow))
	})
	t.Run("directories", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_directories").Error)
		_, err := repo.listDirectories(t.Context(), 1)
		assert.Error(t, err)
		_, err = repo.getDirectory(t.Context(), 1, 1)
		assert.Error(t, err)
		assert.Error(t, repo.insertDirectory(t.Context(), &directoryRow{ID: 1}))
		assert.Error(t, repo.updateDirectory(t.Context(), &directoryRow{ID: 1, TenantID: 1}, 1))
	})
	t.Run("group members", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_group_members").Error)
		_, err := repo.groupMembers(t.Context(), env.auth.DirectoryID, 1)
		assert.Error(t, err)
	})
	t.Run("resource queries", func(t *testing.T) {
		env := newProvisioningEnvironment(t)
		repo := NewRepository(env.runtime)
		bindForeignTables(t, repo, env.prefix)
		require.NoError(t, env.db.Exec("DROP TABLE "+env.prefix+"iam_scim_resources").Error)
		_, _, err := repo.userPage(t.Context(), 1, ListRequest{StartIndex: 1, Count: 1}, "sqlite")
		assert.Error(t, err)
		_, _, err = repo.groupPage(t.Context(), 1, ListRequest{StartIndex: 1, Count: 1}, "sqlite")
		assert.Error(t, err)
		_, err = repo.getResourceByExternalKey(t.Context(), 1, ResourceUser, "key")
		assert.Error(t, err)
		assert.Error(t, repo.insertResource(t.Context(), &resourceRow{DirectoryID: 1, ResourceType: ResourceUser, ResourceID: 1}))
		assert.Error(t, repo.updateResource(t.Context(), &resourceRow{DirectoryID: 1, ResourceType: ResourceUser, ResourceID: 1}, 1))
	})
}
