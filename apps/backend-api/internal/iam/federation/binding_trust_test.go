package federation

import (
	"context"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn"
	authnmodel "7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/federation/model"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestLegacyBindingRequiresReviewedOwnershipAndIndependentRepair(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	row, err := env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	link := model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID,
		ExternalSubject: "external-user-1", CreatedAt: now, UpdatedAt: now, LastLoginAt: now}
	require.NoError(t, env.db.Create(&link).Error)
	_, err = env.service.authenticate(t.Context(), row, idTokenClaims{Subject: link.ExternalSubject}, "oidc")
	assert.ErrorIs(t, err, ErrIdentityOwnershipRequired)
	options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationIdentityLink, provider.ID.String(), "federation", "")
	require.NoError(t, err)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, options.Proof, identityLinkID(provider.ID, link.ExternalSubject), "", "https://iam.example"+callbackURL(provider.ID))
	assert.Error(t, err)
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", "https://iam.example"+callbackURL(provider.ID))
	assert.ErrorIs(t, err, ErrIdentityOwnershipRequired)
	inventory, err := env.service.InventoryLegacyIdentityLinks(t.Context())
	require.NoError(t, err)
	require.Len(t, inventory, 1)
	assert.Equal(t, int64(1), inventory[0].UnresolvedLinkCount)
	env.auditErr = assert.AnError
	assert.ErrorIs(t, env.service.RecordLegacyOwnershipEvidence(t.Context(), principalID, "reviewed pre-existing credential and mailbox ownership: case 42"), assert.AnError)
	require.NoError(t, env.db.Take(&link).Error)
	assert.Empty(t, link.RecoveryEvidence)
	env.auditErr = nil
	require.NoError(t, env.service.RecordLegacyOwnershipEvidence(t.Context(), principalID, "reviewed pre-existing credential and mailbox ownership: case 42"))
	require.NoError(t, env.db.Take(&link).Error)
	assert.Equal(t, BindingUnknown, link.BindingProvenance)
	_, err = env.service.authenticate(t.Context(), row, idTokenClaims{Subject: link.ExternalSubject}, "oidc")
	assert.ErrorIs(t, err, ErrIdentityOwnershipRequired)
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, proof, "", "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
	require.NoError(t, err)
	require.NoError(t, env.db.Take(&link).Error)
	assert.Equal(t, BindingPrincipalVerified, link.BindingProvenance)
	assert.Equal(t, principalID, link.PrincipalID)
	assert.Equal(t, now.Unix(), link.CreatedAt.Unix())
	assert.NotEmpty(t, link.RecoveryEvidence)
	var members, roles, links int64
	require.NoError(t, env.db.Table("iam_tenant_members").Count(&members).Error)
	require.NoError(t, env.db.Table("iam_role_members").Count(&roles).Error)
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Count(&links).Error)
	assert.Equal(t, int64(1), members)
	assert.Zero(t, roles)
	assert.Equal(t, int64(1), links)
	session, err := env.service.authenticate(t.Context(), row, idTokenClaims{Subject: link.ExternalSubject}, "oidc")
	require.NoError(t, err)
	assert.NotEmpty(t, session)
}

func TestLegacyQuarantineInvalidatesOnceAndPreservesSource(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	require.NoError(t, env.db.AutoMigrate(&authnmodel.PersonalToken{}, &authnmodel.AccountViewToken{}, &authnmodel.MFAChallenge{}, &authnmodel.PasskeyChallenge{}, &authnmodel.StepUpChallenge{}, &authnmodel.PasswordRecoveryToken{}, &authnmodel.EmailVerificationToken{}))
	instance := authn.NewModule(env.runtime, authn.Config{})
	instance.Service = authenticator
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	var err error
	env.service.revokeCredentials, err = module.Resolve[identity.PrincipalCredentialsRevoker](services)
	require.NoError(t, err)
	oauthCalls := 0
	env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { oauthCalls++; return nil }
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	now := time.Now().UTC()
	link := model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID,
		ExternalSubject: "legacy", CreatedAt: now, UpdatedAt: now, LastLoginAt: now}
	require.NoError(t, env.db.Create(&link).Error)
	view := authnmodel.AccountViewToken{ID: 501, TokenHash: strings.Repeat("a", 64), ActorID: principalID, PrincipalID: principalID,
		ActorSessionHash: strings.Repeat("b", 64), ScopeType: "personal", ScopeID: principalID,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, env.db.Create(&view).Error)
	proof := identityPasswordProof(t, authenticator, cookie, authn.ReauthenticationIdentityLink, provider.ID.String())
	assert.ErrorIs(t, env.service.recoveryEligible(t.Context(), env.db, principalID), authnext.ErrInvalidRecovery)
	env.auditErr = assert.AnError
	assert.ErrorIs(t, env.service.QuarantineLegacyIdentityLinks(t.Context()), assert.AnError)
	require.NoError(t, env.db.Take(&view, view.ID).Error)
	assert.Nil(t, view.RevokedAt, "a failed quarantine must roll back view-token revocation")
	_, err = authenticator.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	env.auditErr = nil
	require.NoError(t, env.service.QuarantineLegacyIdentityLinks(t.Context()))
	require.NoError(t, env.db.Take(&view, view.ID).Error)
	assert.NotNil(t, view.RevokedAt)
	_, err = authenticator.Authenticate(t.Context(), "", cookie)
	assert.Error(t, err)
	require.NoError(t, env.db.Take(&link).Error)
	assert.Equal(t, BindingUnknown, link.BindingProvenance)
	assert.NotNil(t, link.QuarantinedAt)
	assert.Error(t, authenticator.ConsumeReauthenticationOn(t.Context(), env.db, principalID, cookie, proof, authn.ReauthenticationIdentityLink, provider.ID.String()))
	session, err := authenticator.CreateSession(t.Context(), principalID, now, authnext.Assurance{AuthTime: now, Level: 1, Methods: []string{"pwd"}})
	require.NoError(t, err)
	before := oauthCalls
	require.NoError(t, env.service.QuarantineLegacyIdentityLinks(t.Context()))
	assert.Equal(t, before, oauthCalls)
	_, err = authenticator.Authenticate(t.Context(), "", authenticator.SessionCookie(session))
	require.NoError(t, err)
}

func TestProviderSourceReplacementInvalidatesTrustedBindingAtomically(t *testing.T) {
	env := newFederationEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	env.idp.secret = "client-secret"
	result, _, _, _ := env.completeOIDC(t, provider)
	var principalID guid.ID
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Select("principal_id").Scan(&principalID).Error)
	credentials, oauth := 0, 0
	env.service.revokeCredentials = func(context.Context, *gorm.DB, guid.ID) error { credentials++; return nil }
	env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { oauth++; return nil }
	input := env.providerInput("Renamed", provider.Issuer)
	_, err := env.service.UpdateProvider(t.Context(), provider.TenantID, provider.ID, input)
	require.NoError(t, err)
	assert.Zero(t, credentials)
	input.ClientID = "replacement-client"
	env.auditErr = assert.AnError
	_, err = env.service.UpdateProvider(t.Context(), provider.TenantID, provider.ID, input)
	assert.ErrorIs(t, err, assert.AnError)
	current, err := env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	assert.Equal(t, provider.ClientID, current.ClientID)
	var link model.IdentityLink
	require.NoError(t, env.db.Take(&link).Error)
	assert.Nil(t, link.QuarantinedAt)
	env.auditErr = nil
	_, err = env.service.UpdateProvider(t.Context(), provider.TenantID, provider.ID, input)
	require.NoError(t, err)
	assert.Positive(t, credentials)
	assert.Positive(t, oauth)
	require.NoError(t, env.db.Take(&link).Error)
	assert.NotNil(t, link.QuarantinedAt)
	current, err = env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	_, err = env.service.authenticate(t.Context(), current, idTokenClaims{Subject: link.ExternalSubject}, "oidc")
	assert.ErrorIs(t, err, ErrIdentityOwnershipRequired)
	assert.NotEmpty(t, result.SessionToken)
	assert.Equal(t, principalID, link.PrincipalID)
}

func TestExistingEmailCannotJITBindAnotherProvider(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		label := "default"
		if prefix != "" {
			label = "prefixed"
		}
		t.Run(label, func(t *testing.T) {
			env := newFederationEnvironment(t, prefix)
			first := env.createProvider(t, testID("tenant-a"), env.providerInput("First", env.idp.issuer))
			env.idp.secret = "client-secret"
			env.completeOIDC(t, first)
			second := env.createProvider(t, testID("tenant-b"), env.providerInput("Second", "https://another.example"))
			row, err := env.service.repo.getProviderByID(t.Context(), second.ID)
			require.NoError(t, err)
			verified := true
			_, err = env.service.authenticate(t.Context(), row, idTokenClaims{Subject: "unrelated", Email: "user@example.com", EmailVerified: &verified}, "oidc")
			assert.ErrorIs(t, err, ErrIdentityOwnershipRequired)
			for _, table := range []string{"iam_principals", "iam_identity_links", "iam_tenant_members"} {
				var count int64
				require.NoError(t, env.db.Table(prefix+table).Count(&count).Error)
				assert.Equal(t, int64(1), count, table)
			}
			assert.Len(t, env.web.sessions, 1)
		})
	}
}

func TestFederatedProofCannotAuthorizeAnotherIdentityLink(t *testing.T) {
	env, authenticator, principalID, cookie := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	other := env.createProvider(t, testID("tenant-a"), env.providerInput("Other", env.idp.issuer+"/other"))
	row, err := env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, env.db.Create(&model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID,
		ExternalSubject: "external-user-1", BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: securityProviderFingerprint(row),
		CreatedAt: now, UpdatedAt: now, LastLoginAt: now}).Error)
	options, err := authenticator.BeginReauthentication(t.Context(), cookie, authn.ReauthenticationIdentityLink, other.ID.String(), "federation", "")
	require.NoError(t, err)
	start, err := env.service.StartIdentityOperation(t.Context(), cookie, provider.ID, options.Proof, identityLinkID(provider.ID, "external-user-1"), "", "https://iam.example"+callbackURL(provider.ID))
	require.NoError(t, err)
	state := verifyIdentityOIDC(t, env, provider, start)
	_, err = env.service.CompleteIdentityOperation(t.Context(), provider.ID, state, cookie+"; "+start.StateCookie)
	require.NoError(t, err)
	_, err = env.service.StartIdentityOperation(t.Context(), cookie, other.ID, options.Proof, "", "", "https://iam.example"+callbackURL(other.ID))
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	// A verified upstream proof remains bound to current source trust when a
	// different sensitive operation consumes it later.
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Where("provider_id = ?", provider.ID).Update("security_fingerprint", "stale").Error)
	assert.ErrorIs(t, authenticator.ConsumeReauthenticationOn(t.Context(), env.db, principalID, cookie, options.Proof, authn.ReauthenticationIdentityLink, other.ID.String()), authnext.ErrInvalidCredentials)
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Where("provider_id = ?", provider.ID).Update("security_fingerprint", securityProviderFingerprint(row)).Error)
	require.NoError(t, authenticator.ConsumeReauthenticationOn(t.Context(), env.db, principalID, cookie, options.Proof, authn.ReauthenticationIdentityLink, other.ID.String()))
	var links int64
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Count(&links).Error)
	assert.Equal(t, int64(1), links)
}

type pausedFederationSessionWriter struct {
	authnextWebService
	entered chan struct{}
	release chan struct{}
}

func (s *pausedFederationSessionWriter) CreateSessionOn(ctx context.Context, tx *gorm.DB, id guid.ID, now time.Time, assurance authnext.Assurance) (string, error) {
	close(s.entered)
	select {
	case <-s.release:
		return s.authnextWebService.CreateSessionOn(ctx, tx, id, now, assurance)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestSourceReplacementRacingSessionIssuanceLeavesNoUsableOldSession(t *testing.T) {
	env, authenticator, principalID, _ := newIdentityLinkEnvironment(t)
	require.NoError(t, env.db.AutoMigrate(&authnmodel.PersonalToken{}, &authnmodel.AccountViewToken{}, &authnmodel.MFAChallenge{}, &authnmodel.PasskeyChallenge{}, &authnmodel.StepUpChallenge{}, &authnmodel.PasswordRecoveryToken{}, &authnmodel.EmailVerificationToken{}))
	instance := authn.NewModule(env.runtime, authn.Config{})
	instance.Service = authenticator
	services := module.NewServices()
	require.NoError(t, instance.ProvideServices(services))
	require.NoError(t, services.Seal())
	var err error
	env.service.revokeCredentials, err = module.Resolve[identity.PrincipalCredentialsRevoker](services)
	require.NoError(t, err)
	env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	row, err := env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, env.db.Create(&model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID,
		ExternalSubject: "existing", BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: securityProviderFingerprint(row),
		CreatedAt: now, UpdatedAt: now, LastLoginAt: now}).Error)
	paused := &pausedFederationSessionWriter{authnextWebService: authenticator, entered: make(chan struct{}), release: make(chan struct{})}
	env.service.authn = paused
	type loginResult struct {
		session string
		err     error
	}
	loginDone := make(chan loginResult, 1)
	go func() {
		session, err := env.service.authenticate(t.Context(), row, idTokenClaims{Subject: "existing"}, "oidc")
		loginDone <- loginResult{session, err}
	}()
	select {
	case <-paused.entered:
	case result := <-loginDone:
		t.Fatalf("session barrier not reached: %v", result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("session barrier timed out")
	}
	updateStarted := make(chan struct{})
	updateDone := make(chan error, 1)
	input := env.providerInput("OIDC", provider.Issuer)
	input.ClientID = "replacement-client"
	go func() {
		close(updateStarted)
		_, err := env.service.UpdateProvider(t.Context(), provider.TenantID, provider.ID, input)
		updateDone <- err
	}()
	<-updateStarted
	close(paused.release)
	result := <-loginDone
	require.NoError(t, result.err)
	require.NoError(t, <-updateDone)
	_, err = authenticator.Authenticate(t.Context(), "", authenticator.SessionCookie(result.session))
	assert.Error(t, err)
	var link model.IdentityLink
	require.NoError(t, env.db.Take(&link).Error)
	assert.NotNil(t, link.QuarantinedAt)
}

func TestFederationSessionCannotEscapeBindingTransactionRollback(t *testing.T) {
	env, authenticator, principalID, _ := newIdentityLinkEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	row, err := env.service.repo.getProviderByID(t.Context(), provider.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, env.db.Create(&model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: principalID,
		ExternalSubject: "existing", BindingProvenance: BindingPrincipalVerified, SecurityFingerprint: securityProviderFingerprint(row),
		CreatedAt: now, UpdatedAt: now, LastLoginAt: now}).Error)
	var session string
	err = env.db.Transaction(func(tx *gorm.DB) error {
		original := env.service.repo
		env.service.repo = original.withDB(tx, true)
		defer func() { env.service.repo = original }()
		var err error
		session, err = env.service.authenticate(t.Context(), row, idTokenClaims{Subject: "existing"}, "oidc")
		if err != nil {
			return err
		}
		return assert.AnError
	})
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotEmpty(t, session)
	_, err = authenticator.Authenticate(t.Context(), "", authenticator.SessionCookie(session))
	assert.Error(t, err)
	var sessions int64
	require.NoError(t, env.db.Model(&authnmodel.Session{}).Count(&sessions).Error)
	assert.Equal(t, int64(1), sessions)
}

func TestProviderReplacementLocksOwnersBeforeProviderAndRetriesNewOwner(t *testing.T) {
	env := newFederationEnvironment(t)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	now := time.Now().UTC()
	link := model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: 101,
		ExternalSubject: "first", CreatedAt: now, UpdatedAt: now, LastLoginAt: now}
	require.NoError(t, env.db.Create(&link).Error)
	inserted := false
	require.NoError(t, env.db.Callback().Query().After("gorm:query").Register("test_new_binding_after_snapshot", func(db *gorm.DB) {
		if inserted || !strings.Contains(db.Statement.SQL.String(), "DISTINCT") || db.Statement.Table != "iam_identity_links" {
			return
		}
		inserted = true
		second := link
		second.PrincipalID, second.ExternalSubject = 102, "second"
		require.NoError(t, env.db.Create(&second).Error)
	}))
	t.Cleanup(func() { require.NoError(t, env.db.Callback().Query().Remove("test_new_binding_after_snapshot")) })
	var locked, revoked []guid.ID
	env.service.repo.principals.Lock = func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		var current model.Provider
		require.NoError(t, tx.Table("iam_identity_providers").Where("id = ?", provider.ID).Take(&current).Error)
		// Reentrant invalidation locks run after mutation; admission locks run before it.
		if len(revoked) == 0 && current.ClientID == provider.ClientID {
			locked = append(locked, id)
		}
		return nil
	}
	env.service.revokeCredentials = func(_ context.Context, _ *gorm.DB, id guid.ID) error {
		revoked = append(revoked, id)
		return nil
	}
	env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	providerLockAttempts := 0
	require.NoError(t, env.db.Callback().Update().Before("gorm:update").Register("test_principal_before_provider_lock", func(db *gorm.DB) {
		updates, ok := db.Statement.Dest.(map[string]any)
		if !ok || db.Statement.Table != "iam_identity_providers" || len(updates) != 1 {
			return
		}
		expression, ok := updates["updated_at"].(clause.Expr)
		if !ok || expression.SQL != "updated_at" {
			return
		}
		providerLockAttempts++
		if providerLockAttempts == 1 {
			assert.Equal(t, []guid.ID{101}, locked, "first provider lock follows the snapshotted principal lock")
		} else {
			assert.Equal(t, []guid.ID{101, 101, 102}, locked, "retry provider lock follows every current principal lock")
		}
	}))
	t.Cleanup(func() { require.NoError(t, env.db.Callback().Update().Remove("test_principal_before_provider_lock")) })
	input := env.providerInput("OIDC", provider.Issuer)
	input.ClientID = "replacement"
	_, err := env.service.UpdateProvider(t.Context(), provider.TenantID, provider.ID, input)
	require.NoError(t, err)
	assert.True(t, inserted)
	assert.Equal(t, 2, providerLockAttempts)
	assert.Equal(t, []guid.ID{101, 101, 102}, locked, "new owner requires rollback and sorted principal locks before provider mutation")
	assert.Equal(t, []guid.ID{101, 102}, revoked)
	var quarantined int64
	require.NoError(t, env.db.Model(&model.IdentityLink{}).Where("quarantined_at IS NOT NULL").Count(&quarantined).Error)
	assert.Equal(t, int64(2), quarantined)
}

func TestBindingTrustCapabilitiesFailClosed(t *testing.T) {
	for _, owner := range []*Module{nil, {}} {
		_, err := owner.InventoryLegacyIdentityLinks(t.Context())
		assert.ErrorIs(t, err, ErrUnavailable)
		assert.ErrorIs(t, owner.RecordLegacyOwnershipEvidence(t.Context(), 1, "review"), ErrUnavailable)
		assert.ErrorIs(t, owner.QuarantineLegacyIdentityLinks(t.Context()), ErrUnavailable)
	}
	env := newFederationEnvironment(t)
	for _, input := range []struct {
		id       guid.ID
		evidence string
	}{{0, "review"}, {1, " "}, {1, strings.Repeat("x", 4097)}} {
		assert.ErrorIs(t, env.service.RecordLegacyOwnershipEvidence(t.Context(), input.id, input.evidence), ErrIdentityOwnershipRequired)
	}
	env.service.repo.principals.Lock = nil
	assert.ErrorIs(t, env.service.RecordLegacyOwnershipEvidence(t.Context(), 1, "review"), ErrUnavailable)
	assert.ErrorIs(t, env.service.invalidatePrincipalOn(t.Context(), env.service.repo, 1, time.Now()), ErrUnavailable)
	env.service.repo.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return assert.AnError }
	assert.ErrorIs(t, env.service.RecordLegacyOwnershipEvidence(t.Context(), 1, "review"), assert.AnError)
	env.service.revokeCredentials = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	assert.ErrorIs(t, env.service.invalidatePrincipalOn(t.Context(), env.service.repo, 1, time.Now()), assert.AnError)
	env.service.repo.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	assert.ErrorIs(t, env.service.RecordLegacyOwnershipEvidence(t.Context(), 1, "review"), ErrIdentityOwnershipRequired)
	env.service.revokeCredentials = func(context.Context, *gorm.DB, guid.ID) error { return assert.AnError }
	assert.ErrorIs(t, env.service.invalidatePrincipalOn(t.Context(), env.service.repo, 1, time.Now()), assert.AnError)
	env.service.revokeCredentials = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return assert.AnError }
	assert.ErrorIs(t, env.service.invalidatePrincipalOn(t.Context(), env.service.repo, 1, time.Now()), assert.AnError)
	assert.ErrorIs(t, env.service.proofSourceTrusted(t.Context(), env.db, 1, "invalid", ""), authnext.ErrInvalidCredentials)
	assert.ErrorIs(t, env.service.proofSourceTrusted(t.Context(), env.db, 1, identityLinkID(42, "missing"), ""), ErrProviderNotFound)
	provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
	assert.ErrorIs(t, env.service.proofSourceTrusted(t.Context(), env.db, 1, identityLinkID(provider.ID, "missing"), ""), authnext.ErrInvalidCredentials)
	require.NoError(t, env.db.Table("iam_identity_providers").Where("id = ?", provider.ID).Update("status", ProviderDisabled).Error)
	assert.ErrorIs(t, env.service.proofSourceTrusted(t.Context(), env.db, 1, identityLinkID(provider.ID, "missing"), ""), authnext.ErrInvalidCredentials)
}

func TestLegacyQuarantineFailsClosedAndRechecksConcurrentCutover(t *testing.T) {
	for _, failure := range []string{"missing-lock", "lock-error", "revocation-error", "already-quarantined", "storage-error"} {
		t.Run(failure, func(t *testing.T) {
			env := newFederationEnvironment(t)
			provider := env.createProvider(t, testID("tenant-a"), env.providerInput("OIDC", env.idp.issuer))
			now := time.Now().UTC()
			link := model.IdentityLink{ProviderID: provider.ID, TenantID: provider.TenantID, PrincipalID: 101,
				ExternalSubject: "legacy", CreatedAt: now, UpdatedAt: now, LastLoginAt: now}
			require.NoError(t, env.db.Create(&link).Error)
			calls := 0
			env.service.repo.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return nil }
			env.service.revokeCredentials = func(context.Context, *gorm.DB, guid.ID) error { calls++; return nil }
			env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return nil }
			switch failure {
			case "missing-lock":
				env.service.repo.principals.Lock = nil
			case "lock-error":
				env.service.repo.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return assert.AnError }
			case "revocation-error":
				env.service.revokeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return assert.AnError }
			case "already-quarantined":
				env.service.repo.principals.Lock = func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
					return tx.Model(&model.IdentityLink{}).Where("principal_id = ?", id).Update("quarantined_at", now).Error
				}
			case "storage-error":
				require.NoError(t, env.db.Migrator().DropTable(&model.IdentityLink{}))
			}
			owner := &Module{service: env.service}
			err := owner.QuarantineLegacyIdentityLinks(t.Context())
			if failure == "already-quarantined" {
				require.NoError(t, err)
				assert.Zero(t, calls, "a concurrently completed cutover must not revoke twice")
				return
			}
			require.Error(t, err)
			if failure != "storage-error" {
				require.NoError(t, env.db.Take(&link).Error)
				assert.Nil(t, link.QuarantinedAt, "failed invalidation cannot mark cutover complete")
			}
		})
	}
}

func TestLegacyEvidenceStorageFailureAndOwnerForwarding(t *testing.T) {
	env := newFederationEnvironment(t)
	owner := &Module{service: env.service}
	inventory, err := owner.InventoryLegacyIdentityLinks(t.Context())
	require.NoError(t, err)
	assert.Empty(t, inventory)
	env.service.repo.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	require.NoError(t, env.db.Migrator().DropTable(&model.IdentityLink{}))
	assert.Error(t, owner.RecordLegacyOwnershipEvidence(t.Context(), 1, "review"))
	assert.Error(t, env.service.recoveryEligible(t.Context(), env.db, 1))
	require.NoError(t, env.db.Migrator().DropTable(&model.Provider{}))
	_, err = env.service.repo.lockProvider(t.Context(), 1)
	assert.Error(t, err)
}
