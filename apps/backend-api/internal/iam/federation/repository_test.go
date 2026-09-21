package federation

import (
	"context"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFederationRepositoryConstructionAndTransactionGuards(t *testing.T) {
	assert.Panics(t, func() { NewRepository(nil) })
	var nilRepository *Repository
	assert.Nil(t, nilRepository.writer())
	assert.Empty(t, (&Repository{}).names)
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

// TestFederationRepositoryMethodsFailClosedWithoutWriter pins what every owned
// table entry point answers when no database is bound. The query builder would
// otherwise be handed a nil writer, so a deployment whose datasource never
// opened would panic somewhere inside gorm instead of reporting that federation
// storage is unavailable.
func TestFederationRepositoryMethodsFailClosedWithoutWriter(t *testing.T) {
	repo := &Repository{}
	now := federationTestNow
	provider := &providerRow{ID: testID("provider"), TenantID: testID("tenant-a")}
	link := &identityLinkRow{ProviderID: provider.ID, TenantID: provider.TenantID, ExternalSubject: "subject"}
	serviceProvider := &samlSPRow{ID: testID("sp"), TenantID: provider.TenantID}
	key := &samlKeyRow{KeyID: "key", CreatedAt: now}
	checks := map[string]func() error{
		"list providers":     func() error { _, err := repo.listProviders(t.Context(), provider.TenantID); return err },
		"get provider":       func() error { _, err := repo.getProvider(t.Context(), provider.TenantID, provider.ID); return err },
		"get provider by id": func() error { _, err := repo.getProviderByID(t.Context(), provider.ID); return err },
		"insert provider":    func() error { return repo.insertProvider(t.Context(), provider) },
		"update provider":    func() error { return repo.updateProvider(t.Context(), provider) },
		"delete provider":    func() error { return repo.deleteProvider(t.Context(), provider.TenantID, provider.ID) },
		"identity link": func() error {
			_, err := repo.identityLink(t.Context(), link.ProviderID, link.TenantID, link.ExternalSubject)
			return err
		},
		"insert identity link": func() error { return repo.insertIdentityLink(t.Context(), link) },
		"touch identity link": func() error {
			return repo.touchIdentityLink(t.Context(), link.ProviderID, link.TenantID, link.ExternalSubject, now)
		},
		"list service providers": func() error {
			_, err := repo.listSAMLServiceProviders(t.Context(), serviceProvider.TenantID)
			return err
		},
		"get service provider": func() error {
			_, err := repo.getSAMLServiceProvider(t.Context(), serviceProvider.TenantID, serviceProvider.ID)
			return err
		},
		"get service provider by id": func() error {
			_, err := repo.getSAMLServiceProviderByID(t.Context(), serviceProvider.ID)
			return err
		},
		"active service provider": func() error {
			_, err := repo.activeSAMLServiceProvider(t.Context(), serviceProvider.TenantID, "https://sp.example/metadata")
			return err
		},
		"insert service provider": func() error { return repo.insertSAMLServiceProvider(t.Context(), serviceProvider) },
		"update service provider": func() error { return repo.updateSAMLServiceProvider(t.Context(), serviceProvider) },
		"delete service provider": func() error {
			return repo.deleteSAMLServiceProvider(t.Context(), serviceProvider.TenantID, serviceProvider.ID)
		},
		"latest signing key": func() error { _, err := repo.latestSAMLKey(t.Context()); return err },
		"insert signing key": func() error { return repo.insertSAMLKey(t.Context(), key) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, check(), errRepositoryUnavailable)
		})
	}
}

// TestFederationTouchIdentityLinkRefusesALinkThatIsNoLongerThere covers the
// verification read behind the login-time touch. The update alone cannot tell
// "the link is gone" from "the row already held these values" -- MySQL reports
// no affected rows for both -- so the count decides, and a subject with no link
// is refused rather than logged in on a link somebody removed.
func TestFederationTouchIdentityLinkRefusesALinkThatIsNoLongerThere(t *testing.T) {
	env := newFederationEnvironment(t)
	repo := env.service.repo
	const provider, subject = int64(91), "upstream-subject-a"
	tenant := guid.ID(7)
	seedIdentityLink(t, env, provider, testID("principal"), subject, "person@idp.example.test")

	login := federationErasureTime.Add(time.Hour)
	require.NoError(t, repo.touchIdentityLink(t.Context(), guid.ID(provider), tenant, subject, login))
	stored, err := repo.identityLink(t.Context(), guid.ID(provider), tenant, subject)
	require.NoError(t, err)
	assert.Equal(t, login.UTC(), stored.LastLoginAt.UTC(), "a matched link records the login it was touched for")

	assert.ErrorIs(t,
		repo.touchIdentityLink(t.Context(), guid.ID(provider), tenant, "subject-that-never-linked", login),
		ErrPrincipalInactive)
	assert.ErrorIs(t,
		repo.touchIdentityLink(t.Context(), guid.ID(provider), testID("tenant-b"), subject, login),
		ErrPrincipalInactive, "a link in another tenant is not this tenant's to touch")
}
