package federation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFederationRepositoryConstructionAndTransactionGuards(t *testing.T) {
	assert.Panics(t, func() { NewRepository(nil) })
	var nilRepository *Repository
	assert.Nil(t, nilRepository.writer())
	assert.Empty(t, nilRepository.prefix())
	_, err := nilRepository.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Error(t, nilRepository.runInTx(t.Context(), func(*Repository) error { return nil }))

	empty := &Repository{}
	assert.Nil(t, empty.writer())
	_, err = empty.pinWriter()
	assert.ErrorIs(t, err, errRepositoryUnavailable)
	assert.Error(t, empty.runInTx(t.Context(), nil))

	env := newFederationEnvironment(t, "sl_")
	pinned, err := env.service.repo.pinWriter()
	require.NoError(t, err)
	require.NotNil(t, pinned.db)
	again, err := pinned.pinWriter()
	require.NoError(t, err)
	assert.Same(t, pinned, again)
	inTx := pinned.withDB(pinned.db, true)
	called := false
	require.NoError(t, inTx.runInTx(t.Context(), func(actual *Repository) error {
		called = true
		assert.Same(t, inTx, actual)
		return nil
	}))
	assert.True(t, called)
}

func TestFederationRepositoryPropagatesOwnedTableFailures(t *testing.T) {
	t.Run("providers", func(t *testing.T) {
		env := newFederationEnvironment(t)
		repo := env.service.repo
		require.NoError(t, env.db.Exec("DROP TABLE iam_identity_providers").Error)
		row := providerRow{ID: testID("provider"), TenantID: testID("tenant-a")}
		_, err := repo.getProviderByID(t.Context(), row.ID)
		assert.Error(t, err)
		assert.Error(t, repo.insertProvider(t.Context(), &row))
		assert.Error(t, repo.updateProvider(t.Context(), &row))
		assert.Error(t, repo.deleteProvider(t.Context(), row.TenantID, row.ID))
	})

	t.Run("identity links", func(t *testing.T) {
		env := newFederationEnvironment(t)
		repo := env.service.repo
		require.NoError(t, env.db.Exec("DROP TABLE iam_identity_links").Error)
		row := identityLinkRow{ProviderID: testID("provider"), TenantID: testID("tenant-a"), ExternalSubject: "subject"}
		assert.Error(t, repo.insertIdentityLink(t.Context(), &row))
		assert.Error(t, repo.touchIdentityLink(t.Context(), row.ProviderID, row.TenantID, row.ExternalSubject, time.Now().UTC()))
	})

	t.Run("service providers", func(t *testing.T) {
		env := newFederationEnvironment(t)
		repo := env.service.repo
		require.NoError(t, env.db.Exec("DROP TABLE iam_saml_service_providers").Error)
		row := samlSPRow{ID: testID("sp"), TenantID: testID("tenant-a")}
		_, err := repo.listSAMLServiceProviders(t.Context(), row.TenantID)
		assert.Error(t, err)
		_, err = repo.getSAMLServiceProvider(t.Context(), row.TenantID, row.ID)
		assert.Error(t, err)
		assert.Error(t, repo.insertSAMLServiceProvider(t.Context(), &row))
		assert.Error(t, repo.updateSAMLServiceProvider(t.Context(), &row))
		assert.Error(t, repo.deleteSAMLServiceProvider(t.Context(), row.TenantID, row.ID))
	})

	t.Run("signing keys", func(t *testing.T) {
		env := newFederationEnvironment(t)
		repo := env.service.repo
		require.NoError(t, env.db.Exec("DROP TABLE iam_saml_idp_keys").Error)
		row := samlKeyRow{KeyID: "key", CreatedAt: time.Now().UTC()}
		assert.Error(t, repo.insertSAMLKey(t.Context(), &row))
		assert.Error(t, repo.rotateSAMLKeys(t.Context(), &row, time.Now().UTC()))
	})
}

func TestFederationRepositoryPropagatesCoreTableFailures(t *testing.T) {
	t.Run("roles", func(t *testing.T) {
		env := newFederationEnvironment(t)
		require.NoError(t, env.db.Exec("DROP TABLE iam_roles").Error)
		_, err := env.service.repo.roleExists(t.Context(), testID("tenant-a"), testID("role"))
		assert.Error(t, err)
		_, err = env.service.repo.grantDefaultRole(t.Context(), testID("tenant-a"), testID("role"), testID("principal"), time.Now().UTC())
		assert.Error(t, err)
	})

	t.Run("role members", func(t *testing.T) {
		env := newFederationEnvironment(t)
		insertRole(t, env.db, "tenant-a", "role-a")
		require.NoError(t, env.db.Exec("DROP TABLE iam_role_members").Error)
		_, err := env.service.repo.grantDefaultRole(t.Context(), testID("tenant-a"), testID("role-a"), testID("principal"), time.Now().UTC())
		assert.Error(t, err)
	})

	t.Run("policy revisions", func(t *testing.T) {
		env := newFederationEnvironment(t)
		insertRole(t, env.db, "tenant-a", "role-a")
		require.NoError(t, env.db.Exec("DROP TABLE iam_policy_revisions").Error)
		_, err := env.service.repo.grantDefaultRole(t.Context(), testID("tenant-a"), testID("role-a"), testID("principal"), time.Now().UTC())
		assert.Error(t, err)
	})

	t.Run("principals", func(t *testing.T) {
		env := newFederationEnvironment(t)
		require.NoError(t, env.db.Exec("DROP TABLE iam_principals").Error)
		assert.Error(t, env.service.repo.verifyPrincipalAndMember(t.Context(), testID("tenant-a"), testID("principal")))
	})

	t.Run("tenant members", func(t *testing.T) {
		env := newFederationEnvironment(t)
		principal := federationPrincipal{ID: testID("principal"), LoginName: "principal", Email: "principal@example.com", Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		require.NoError(t, env.db.Table("iam_principals").Create(&principal).Error)
		require.NoError(t, env.db.Exec("DROP TABLE iam_tenant_members").Error)
		assert.Error(t, env.service.repo.verifyPrincipalAndMember(t.Context(), testID("tenant-a"), principal.ID))
	})

	t.Run("transaction callback", func(t *testing.T) {
		env := newFederationEnvironment(t)
		sentinel := context.Canceled
		err := env.service.repo.runInTx(t.Context(), func(*Repository) error { return sentinel })
		assert.ErrorIs(t, err, sentinel)
	})
}
