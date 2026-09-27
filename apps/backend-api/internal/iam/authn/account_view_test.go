package authn

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/policyx"
	"7link.com/platform/pkg/extension/webauthnx"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type accountViewHTTPVerifier struct{ service *Service }

func TestAccountViewVerificationRejectsNoncanonicalStoredMode(t *testing.T) {
	s, h := newAccountViewService(t, "corrupt_")
	created, err := s.CreateAccountView(t.Context(), "session=browser-101", AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202})
	require.NoError(t, err)
	// Bypass storage guards only in this disposable database to test verifier defenses.
	require.NoError(t, h.db.Exec("DROP TRIGGER corrupt_authn_account_views_immutable").Error)
	require.NoError(t, h.db.Exec("PRAGMA ignore_check_constraints = ON").Error)
	for _, mode := range []string{"", "write", "READ_ONLY"} {
		require.NoError(t, h.db.Table(h.repository.names.AccountViewTokens.String()).Where("id = ?", created.ID).Update("mode", mode).Error)
		claims, err := s.AuthenticateAccountView(t.Context(), created.Token)
		assert.ErrorIs(t, err, authnext.ErrInvalidToken)
		assert.Nil(t, claims)
	}
}

func (v accountViewHTTPVerifier) Authenticate(ctx context.Context, authorization, _ string) (*authnext.Claims, error) {
	raw, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok {
		return nil, authnext.ErrInvalidBearer
	}
	return v.service.AuthenticateAccountView(ctx, raw)
}

func TestAccountViewTokenThroughActualPersonalHTTPHandlers(t *testing.T) {
	s, h := newAccountViewService(t, "httpview_")
	_, api := humatest.New(t)
	RegisterHTTP(api, accountViewHTTPVerifier{s}, s)
	created, err := s.CreateAccountView(t.Context(), "session=browser-101", AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202})
	require.NoError(t, err)
	authorization := "Authorization: Bearer " + created.Token
	cookie := "Cookie: session=browser-101"
	var before, after []model.Session
	require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&before).Error)
	me := api.Get("/me", authorization, cookie)
	require.Equal(t, http.StatusOK, me.Code, me.Body.String())
	assert.Contains(t, me.Body.String(), `"subject":"202"`)
	assert.NotContains(t, me.Body.String(), created.Token)
	sessions := api.Get("/principals/202/sessions", authorization, cookie)
	require.Equal(t, http.StatusOK, sessions.Code, sessions.Body.String())
	assert.Contains(t, sessions.Body.String(), tokenHash("browser-202"))
	assert.NotContains(t, sessions.Body.String(), tokenHash("browser-101"))
	assert.NotContains(t, sessions.Body.String(), `"current":true`)
	foreign := api.Get("/principals/101/sessions", authorization, cookie)
	require.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
	denied := api.Delete("/sessions/"+tokenHash("browser-202"), authorization, cookie)
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	logout := api.Delete("/sessions/current", authorization, cookie)
	require.Equal(t, http.StatusForbidden, logout.Code, logout.Body.String())
	require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&after).Error)
	assert.Equal(t, before, after)
	require.NoError(t, s.RevokeAccountView(t.Context(), "session=browser-101", created.ID))
	revoked := api.Get("/me", authorization, cookie)
	require.Equal(t, http.StatusUnauthorized, revoked.Code, revoked.Body.String())
}

func TestAccountViewPersonalReadsNeverSubstituteCookieOwner(t *testing.T) {
	s, h := newAccountViewService(t, "read_")
	require.NoError(t, h.db.Table(h.repository.foreign.directory.Tenants.String()).Create(map[string]any{"id": 1, "status": "active"}).Error)
	var err error
	s.cfg.Passkey.Enabled = true
	s.passkeys, err = webauthnx.New(webauthnx.Config{
		RPID: "app.example.test", DisplayName: "Identity", Origins: []string{"https://app.example.test"},
	})
	require.NoError(t, err)
	for _, id := range []guid.ID{101, 202} {
		require.NoError(t, h.db.Table(h.repository.names.Passkeys.String()).Create(&model.Passkey{
			IDHash: tokenHash("passkey-" + id.String()), PrincipalID: id, Name: "Key " + id.String(), CredentialCiphertext: "sealed",
			CreatedAt: repositoryTestNow, UpdatedAt: repositoryTestNow,
		}).Error)
		require.NoError(t, h.db.Table(h.repository.names.PersonalTokens.String()).Create(&model.PersonalToken{
			ID: id, TokenHash: tokenHash("pat-" + id.String()), PrincipalID: id, TenantID: 1, Name: "Token " + id.String(),
			Permissions: `["role_view"]`, CredentialVersion: 1, CreatedAt: repositoryTestNow, ExpiresAt: repositoryTestNow.Add(time.Hour),
		}).Error)
	}
	require.NoError(t, h.db.Table(h.repository.names.Credentials.String()).Where("principal_id = ?", 202).
		Updates(map[string]any{"mfa_required": true, "totp_secret": "sealed"}).Error)
	created, err := s.CreateAccountView(t.Context(), "session=browser-101", AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202})
	require.NoError(t, err)
	claims, err := s.AuthenticateAccountView(t.Context(), created.Token)
	require.NoError(t, err)
	ctx := authnext.WithClaims(t.Context(), claims)
	var before, after []model.Session
	require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&before).Error)
	for _, cookie := range []string{"session=browser-101", "session=browser-202", "session=invalid", ""} {
		_, authenticateErr := s.Authenticate(ctx, "", cookie)
		assert.ErrorIs(t, authenticateErr, authnext.ErrInvalidCredentials, "ordinary authentication cannot replace view identity with a cookie owner")
		_, authenticateErr = s.authenticateBrowserSession(ctx, cookie, true)
		assert.ErrorIs(t, authenticateErr, authnext.ErrInvalidCredentials, "security workflows cannot fall back to browser authentication from a view")
		sessions, readErr := s.ListSessions(ctx, "Bearer "+created.Token, cookie)
		require.NoError(t, readErr)
		require.Len(t, sessions, 1)
		assert.Equal(t, tokenHash("browser-202"), sessions[0].ID)
		assert.False(t, sessions[0].Current, "a view must not mark any target browser session current")
		_, readErr = s.GetSession(ctx, "", cookie, tokenHash("browser-101"))
		assert.ErrorIs(t, readErr, ErrSessionNotFound)
		status, readErr := s.MFAStatus(ctx, "", cookie)
		require.NoError(t, readErr)
		assert.True(t, status.TOTPEnabled)
		keys, readErr := s.ListPasskeys(ctx, "", cookie)
		require.NoError(t, readErr)
		require.Len(t, keys, 1)
		assert.Equal(t, "Key 202", keys[0].Name)
		tokens, count, readErr := s.ListPersonalTokens(ctx, cookie, 0, 10)
		require.NoError(t, readErr)
		assert.EqualValues(t, 1, count)
		require.Len(t, tokens, 1)
		assert.Equal(t, "Token 202", tokens[0].Name)
	}
	require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&after).Error)
	assert.Equal(t, before, after)
	invalid := *claims
	invalid.AccountView = &authnext.AccountViewIdentity{ID: 50, PrincipalID: 101, ScopeType: "tenant", ScopeID: 1}
	_, err = s.ListSessions(authnext.WithClaims(t.Context(), &invalid), "", "session=browser-101")
	assert.ErrorIs(t, err, authnext.ErrInvalidToken, "invalid personal scope cannot fall back to the operator cookie")
}

func newAccountViewService(t *testing.T, prefix string) (*Service, *authnRepositoryHarness) {
	t.Helper()
	h, _ := accountViewFixture(t, prefix)
	h.seedCredential(t, 101, "operator-hash")
	h.seedCredential(t, 202, "target-hash")
	authorizer := AccountViewAuthorizer(func(ctx context.Context, tx *gorm.DB, actor, target guid.ID, scope string, id guid.ID) error {
		require.NotNil(t, tx)
		assert.Equal(t, guid.ID(101), actor)
		assert.Equal(t, guid.ID(202), target)
		assert.Equal(t, "personal", scope)
		assert.Equal(t, guid.ID(202), id)
		claims, ok := authnext.FromContext(ctx)
		require.True(t, ok)
		assert.Nil(t, claims.AccountView)
		assert.Equal(t, actor, claims.PrincipalID)
		assert.Equal(t, 1, claims.ACR)
		assert.Equal(t, []string{"pwd"}, claims.AMR)
		trusted := policyx.TrustedFromContext(ctx, repositoryTestNow)
		assert.Equal(t, 1, trusted.ACR)
		return nil
	})
	s, err := NewWebService(localAuthnConfig(), h.repository, WithAccountViewAuthorizer(authorizer), WithMemberTenantResolver(boundMemberTenantResolver(t, h.runtime)))
	require.NoError(t, err)
	s.now = func() time.Time { return repositoryTestNow }
	return s, h
}

func TestAccountViewAuthorizerUsesLockedTransaction(t *testing.T) {
	s, h := newAccountViewService(t, "txview_")
	pool, err := h.db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	previous := s.accountViewAuthorizer
	checks := 0
	s.accountViewAuthorizer = func(ctx context.Context, tx *gorm.DB, actor, target guid.ID, scope string, id guid.ID) error {
		checks++
		var count int64
		if err := tx.WithContext(ctx).Table(h.repository.foreign.principal.Principals.String()).Where("id = ? AND status = ?", target, "active").Count(&count).Error; err != nil {
			return err
		}
		require.EqualValues(t, 1, count)
		return previous(ctx, tx, actor, target, scope, id)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	created, err := s.CreateAccountView(ctx, "session=browser-101", AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202})
	require.NoError(t, err)
	_, err = s.Authenticate(ctx, "Bearer "+created.Token, "")
	require.NoError(t, err)
	assert.Equal(t, 2, checks)
}

func TestAccountViewServiceIssuesVerifiesAndRevokesWithoutSessions(t *testing.T) {
	for _, prefix := range []string{"", "view_"} {
		t.Run(prefixName(prefix), func(t *testing.T) {
			s, h := newAccountViewService(t, prefix)
			var before, after []model.Session
			require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&before).Error)
			for _, mode := range []string{"", "read_write"} {
				created, err := s.CreateAccountView(t.Context(), "session=browser-101", AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202, Mode: mode})
				require.NoError(t, err)
				assert.True(t, strings.HasPrefix(created.Token, "avt_"))
				assert.Equal(t, repositoryTestNow.Add(30*time.Minute), created.ExpiresAt)
				var row model.AccountViewToken
				require.NoError(t, h.db.Table(h.repository.names.AccountViewTokens.String()).Where("id = ?", created.ID).Take(&row).Error)
				assert.Equal(t, tokenHash(created.Token), row.TokenHash)
				assert.NotEqual(t, created.Token, row.TokenHash)
				assert.Equal(t, int64(1), row.ActorCredentialVersion)
				assert.Equal(t, int64(1), row.TargetCredentialVersion)
				claims, err := s.AuthenticateAccountView(t.Context(), created.Token)
				require.NoError(t, err)
				assert.Equal(t, guid.ID(202), claims.PrincipalID)
				assert.Equal(t, "target", claims.PreferredUsername)
				assert.Equal(t, guid.ID(101), claims.AccountView.PrincipalID)
				assert.Equal(t, "personal", claims.AccountView.ScopeType)
				assert.Equal(t, guid.ID(202), claims.AccountView.ScopeID)
				assert.Equal(t, mode == "", claims.AccountView.ReadOnly)
				assert.Zero(t, claims.ACR)
				assert.Empty(t, claims.AMR)
				assert.True(t, claims.AuthTime.IsZero())
				assert.Empty(t, claims.ClientID)
				assert.Empty(t, claims.OAuthGrantID)
				dispatched, err := s.Authenticate(t.Context(), "Bearer "+created.Token, "session=browser-101")
				require.NoError(t, err)
				assert.Equal(t, claims.AccountView, dispatched.AccountView)
				require.NoError(t, s.RevokeAccountView(t.Context(), "session=browser-101", created.ID))
				require.NoError(t, s.RevokeAccountView(t.Context(), "session=browser-101", created.ID))
				_, err = s.AuthenticateAccountView(t.Context(), created.Token)
				assert.ErrorIs(t, err, authnext.ErrInvalidToken)
			}
			require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Order("id_hash").Find(&after).Error)
			assert.Equal(t, before, after)
			for _, event := range h.audits {
				assert.Equal(t, guid.ID(101), event.PrincipalID)
				assert.Equal(t, "202", event.Detail["viewed_principal_id"])
				assert.NotEmpty(t, event.Detail["account_view_id"])
				assert.NotContains(t, event.Detail, "token")
				assert.NotContains(t, event.Detail, "token_hash")
			}
			assert.Len(t, h.audits, 8, "idempotent revocation must not report a second mutation")
		})
	}
}

func TestAccountViewServiceBoundsAndNestedDenial(t *testing.T) {
	s, h := newAccountViewService(t, "")
	request := AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202}
	for _, change := range []func(*AccountViewRequest){
		func(r *AccountViewRequest) { r.PrincipalID = -1 },
		func(r *AccountViewRequest) { r.ScopeID = -1 },
		func(r *AccountViewRequest) { r.ScopeID = 101 },
		func(r *AccountViewRequest) { r.ScopeType = "unknown" },
		func(r *AccountViewRequest) { r.Mode = "write" },
		func(r *AccountViewRequest) { r.ExpiresAt = repositoryTestNow },
		func(r *AccountViewRequest) { r.ExpiresAt = repositoryTestNow.Add(31 * time.Minute) },
	} {
		input := request
		change(&input)
		_, err := s.CreateAccountView(t.Context(), "session=browser-101", input)
		assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	}
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 202, AccountView: &authnext.AccountViewIdentity{ID: 501, PrincipalID: 101, ScopeType: "personal", ScopeID: 202}})
	_, err := s.CreateAccountView(ctx, "session=browser-101", request)
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	assert.ErrorIs(t, s.RevokeAccountView(ctx, "session=browser-101", 501), authnext.ErrInvalidCredentials)
	_, err = s.AuthenticateAccountView(ctx, "avt_wrong")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	for _, cookie := range []string{"", "session=avt_wrong", "session=wrong"} {
		_, err := s.CreateAccountView(t.Context(), cookie, request)
		assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	}
	for _, raw := range []string{"", "pat_secret", "avt_wrong", "Bearer avt_wrong", "avt_" + strings.Repeat("A", 44)} {
		_, err := s.AuthenticateAccountView(t.Context(), raw)
		assert.ErrorIs(t, err, authnext.ErrInvalidToken)
	}
	require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Where("id_hash = ?", tokenHash("browser-101")).Update("expires_at", repositoryTestNow.Add(5*time.Minute)).Error)
	created, err := s.CreateAccountView(t.Context(), "session=browser-101", request)
	require.NoError(t, err)
	assert.Equal(t, repositoryTestNow.Add(5*time.Minute), created.ExpiresAt)
	require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Where("id_hash = ?", tokenHash("browser-101")).Update("absolute_expires_at", repositoryTestNow.Add(2*time.Minute)).Error)
	created, err = s.CreateAccountView(t.Context(), "session=browser-101", request)
	require.NoError(t, err)
	assert.Equal(t, repositoryTestNow.Add(2*time.Minute), created.ExpiresAt)
	s.accountViewAuthorizer = nil
	_, err = s.CreateAccountView(t.Context(), "session=browser-101", request)
	assert.ErrorIs(t, err, authnext.ErrUnavailable)
	_, err = s.AuthenticateAccountView(t.Context(), created.Token)
	assert.ErrorIs(t, err, authnext.ErrUnavailable)
	assert.ErrorIs(t, s.RevokeAccountView(t.Context(), "session=browser-101", created.ID), authnext.ErrUnavailable)
}

func TestAccountViewServiceIssuanceDenialLeavesNoRecord(t *testing.T) {
	for _, reason := range []string{"authority", "enrollment"} {
		t.Run(reason, func(t *testing.T) {
			s, h := newAccountViewService(t, "")
			if reason == "authority" {
				s.accountViewAuthorizer = AccountViewAuthorizer(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) error {
					return authnext.ErrInvalidCredentials
				})
			} else {
				require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Where("id_hash = ?", tokenHash("browser-101")).Update("acr", 0).Error)
			}
			created, err := s.CreateAccountView(t.Context(), "session=browser-101", AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202})
			require.Error(t, err)
			assert.Empty(t, created.Token)
			var count int64
			require.NoError(t, h.db.Table(h.repository.names.AccountViewTokens.String()).Count(&count).Error)
			assert.Zero(t, count)
			assert.Empty(t, h.audits)
		})
	}
}

func TestAccountViewServiceLiveStateAndAuditRollback(t *testing.T) {
	for _, scenario := range []string{"authority", "actor-version", "target-version", "actor-disabled", "target-disabled", "session-revoked", "session-expired", "token-expired", "enrollment-session", "use-audit"} {
		t.Run(scenario, func(t *testing.T) {
			s, h := newAccountViewService(t, "")
			created, err := s.CreateAccountView(t.Context(), "session=browser-101", AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202})
			require.NoError(t, err)
			switch scenario {
			case "authority":
				s.accountViewAuthorizer = AccountViewAuthorizer(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) error {
					return authnext.ErrInvalidCredentials
				})
			case "actor-version", "target-version":
				id := guid.ID(101)
				if scenario == "target-version" {
					id = 202
				}
				require.NoError(t, h.db.Table(h.repository.names.Credentials.String()).Where("principal_id = ?", id).Update("credential_version", 2).Error)
			case "actor-disabled", "target-disabled":
				id := guid.ID(101)
				if scenario == "target-disabled" {
					id = 202
				}
				require.NoError(t, h.db.Table("iam_principals").Where("id = ?", id).Update("status", "disabled").Error)
			case "session-revoked":
				require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Where("id_hash = ?", tokenHash("browser-101")).Update("revoked_at", repositoryTestNow).Error)
			case "session-expired":
				require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Where("id_hash = ?", tokenHash("browser-101")).Update("expires_at", repositoryTestNow).Error)
			case "token-expired":
				s.now = func() time.Time { return created.ExpiresAt }
			case "enrollment-session":
				require.NoError(t, h.db.Table(h.repository.names.Sessions.String()).Where("id_hash = ?", tokenHash("browser-101")).Update("acr", 0).Error)
			case "use-audit":
				h.auditErr = assert.AnError
			}
			claims, err := s.AuthenticateAccountView(t.Context(), created.Token)
			require.Error(t, err)
			assert.Nil(t, claims)
			var row model.AccountViewToken
			require.NoError(t, h.db.Table(h.repository.names.AccountViewTokens.String()).Where("id = ?", created.ID).Take(&row).Error)
			assert.Nil(t, row.LastUsedAt, "denied use cannot commit last-use state")
		})
	}
	s, h := newAccountViewService(t, "")
	request := AccountViewRequest{PrincipalID: 202, ScopeType: "personal", ScopeID: 202}
	h.auditErr = assert.AnError
	created, err := s.CreateAccountView(t.Context(), "session=browser-101", request)
	require.ErrorIs(t, err, assert.AnError)
	assert.Empty(t, created.Token)
	var count int64
	require.NoError(t, h.db.Table(h.repository.names.AccountViewTokens.String()).Count(&count).Error)
	assert.Zero(t, count)
	h.auditErr = nil
	created, err = s.CreateAccountView(t.Context(), "session=browser-101", request)
	require.NoError(t, err)
	h.auditErr = assert.AnError
	assert.ErrorIs(t, s.RevokeAccountView(t.Context(), "session=browser-101", created.ID), assert.AnError)
	h.auditErr = nil
	_, err = s.AuthenticateAccountView(t.Context(), created.Token)
	require.NoError(t, err, "failed revocation audit must roll back revocation")
	assert.ErrorIs(t, s.RevokeAccountView(t.Context(), "session=browser-202", created.ID), authnext.ErrInvalidToken)
}
