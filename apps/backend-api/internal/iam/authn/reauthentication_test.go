package authn

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/iam/identity"
	authnext "7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReauthenticationHTTPRequiresCookieAndCSRFAndVerifiesProof(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	httpSubject := httpPrincipalID(t, service, cookie)
	router, api := humatest.New(t)
	registerReauthenticationHTTP(api, service)
	request := func(method, path, body, origin, authorization string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("Authorization", authorization)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	methods := request(http.MethodGet, "/principals/"+httpSubject+"/reauthentication/methods", "", "", "")
	require.Equal(t, http.StatusOK, methods.Code)
	assert.Contains(t, methods.Body.String(), `"password"`)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodGet, "/principals/"+httpSubject+"/reauthentication/methods", "", "", "Bearer invalid").Code)
	body := `{"purpose":"principal_close","target":"self","method":"password"}`
	assert.Equal(t, http.StatusForbidden, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/challenges", body, "https://evil.example", "").Code)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/challenges", body, "https://app.example.test", "Bearer invalid").Code)
	started := request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/challenges", body, "https://app.example.test", "")
	require.Equal(t, http.StatusCreated, started.Code)
	var envelope struct {
		Data ReauthenticationOptions `json:"data"`
	}
	require.NoError(t, json.Unmarshal(started.Body.Bytes(), &envelope))
	verify, err := json.Marshal(map[string]string{"proof": envelope.Data.Proof, "password": "correct horse battery staple"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/verifications", string(verify), "https://evil.example", "").Code)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/verifications", string(verify), "https://app.example.test", "Bearer invalid").Code)
	require.Equal(t, http.StatusCreated, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/verifications", string(verify), "https://app.example.test", "").Code)
	assert.Equal(t, http.StatusUnauthorized, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/verifications", string(verify), "https://app.example.test", "").Code)
	require.NoError(t, harness.db.Migrator().DropTable(&model.ReauthenticationChallenge{}))
	assert.Equal(t, http.StatusInternalServerError, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/challenges", body, "https://app.example.test", "").Code)
	assert.Equal(t, http.StatusInternalServerError, request(http.MethodPost, "/principals/"+httpSubject+"/reauthentication/verifications", string(verify), "https://app.example.test", "").Code)
}

func TestReauthenticationDatabaseWriteFailuresPreserveReusableProof(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	require.NoError(t, err)
	for _, table := range []string{"iam_principals", "iam_sessions", "iam_reauth_challenges"} {
		t.Run(table, func(t *testing.T) {
			require.NoError(t, harness.db.Callback().Update().Before("gorm:update").Register("reauth_write_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == table {
					tx.AddError(assert.AnError)
				}
			}))
			assert.ErrorIs(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil), assert.AnError)
			require.NoError(t, harness.db.Callback().Update().Remove("reauth_write_failure"))
			var stored model.ReauthenticationChallenge
			require.NoError(t, harness.db.Take(&stored, "id_hash = ?", tokenHash(options.Proof)).Error)
			assert.Nil(t, stored.VerifiedAt)
			assert.Nil(t, stored.ConsumedAt)
			assert.Zero(t, stored.Attempts, "database outages must not consume password attempts")
		})
	}
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
	require.NoError(t, harness.db.Callback().Update().Before("gorm:update").Register("reauth_consume_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "iam_reauth_challenges" {
			tx.AddError(assert.AnError)
		}
	}))
	consume := func() error {
		return harness.db.Transaction(func(tx *gorm.DB) error {
			return service.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, options.Proof, ReauthenticationPrincipalClose, "self")
		})
	}
	assert.ErrorIs(t, consume(), assert.AnError)
	require.NoError(t, harness.db.Callback().Update().Remove("reauth_consume_failure"))
	require.NoError(t, consume())
	assert.ErrorIs(t, service.ConsumeReauthenticationOn(t.Context(), harness.db, claims.PrincipalID, cookie, options.Proof, "unsupported", "self"), authnext.ErrInvalidCredentials)
}

func TestReauthenticationCurrentPrincipalAndSessionAreRequiredUnderLock(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	hash := currentSessionHash(cookie, service.web.CookieName)
	_, err = service.lockReauthenticationSession(t.Context(), harness.repository, claims.PrincipalID, "missing-session")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	_, err = service.lockReauthenticationSession(t.Context(), harness.repository, claims.PrincipalID+1, hash)
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	require.NoError(t, harness.db.Callback().Query().Before("gorm:query").Register("reauth_principal_read_failure", func(tx *gorm.DB) {
		tx.AddError(assert.AnError)
	}))
	_, err = service.lockReauthenticationSession(t.Context(), harness.repository, claims.PrincipalID, hash)
	assert.ErrorIs(t, err, assert.AnError)
	require.NoError(t, harness.db.Callback().Query().Remove("reauth_principal_read_failure"))
	require.NoError(t, harness.db.Table("iam_principals").Where("id = ?", claims.PrincipalID).Update("status", "disabled").Error)
	_, err = service.lockReauthenticationSession(t.Context(), harness.repository, claims.PrincipalID, hash)
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestReauthenticationCredentialStorageFailureIsNotAnAuthenticationFailure(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	httpSubject := httpPrincipalID(t, service, cookie)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	require.NoError(t, err)
	require.NoError(t, harness.db.Callback().Query().Before("gorm:query").Register("reauth_credential_read_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "iam_credentials" {
			tx.AddError(assert.AnError)
		}
	}))
	_, err = service.AvailableReauthenticationMethods(t.Context(), cookie)
	assert.ErrorIs(t, err, assert.AnError)
	_, err = service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	assert.ErrorIs(t, err, assert.AnError)
	assert.ErrorIs(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil), assert.AnError)
	_, err = service.localLoginMethodsOn(t.Context(), harness.db, claims.PrincipalID)
	assert.ErrorIs(t, err, assert.AnError)
	router, api := humatest.New(t)
	registerReauthenticationHTTP(api, service)
	req := httptest.NewRequest(http.MethodGet, "/principals/"+httpSubject+"/reauthentication/methods", nil)
	req.Header.Set("Cookie", cookie)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	require.NoError(t, harness.db.Callback().Query().Remove("reauth_credential_read_failure"))
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
}

func TestReauthenticationPasskeyAvailabilityFailsClosedOnStorageLoss(t *testing.T) {
	service, harness, principalID, _ := newPasskeyService(t)
	session, err := service.CreateSession(t.Context(), principalID, service.now(), assurancePassword(service.now()))
	require.NoError(t, err)
	cookie := service.SessionCookie(session)
	_, err = service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "passkey", "https://evil.example")
	assert.ErrorIs(t, err, ErrCSRF)
	require.NoError(t, harness.db.Migrator().DropTable(&model.Passkey{}))
	_, err = service.AvailableReauthenticationMethods(t.Context(), cookie)
	assert.Error(t, err)
	_, err = service.localLoginMethodsOn(t.Context(), harness.db, principalID)
	assert.Error(t, err)
}

func TestReauthenticationCorruptCredentialStorageCannotVerify(t *testing.T) {
	service, harness, cookie, _ := enabledMFAService(t)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	require.NoError(t, err)
	var credential model.Credential
	require.NoError(t, harness.db.Take(&credential).Error)
	require.NoError(t, harness.db.Model(&model.Credential{}).Where("principal_id = ?", credential.PrincipalID).Update("password_hash", "corrupt").Error)
	assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "123456", "", nil))
	require.NoError(t, harness.db.Model(&model.Credential{}).Where("principal_id = ?", credential.PrincipalID).Updates(map[string]any{"password_hash": credential.PasswordHash, "totp_secret": "corrupt"}).Error)
	assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "123456", "", nil))
	var proof model.ReauthenticationChallenge
	require.NoError(t, harness.db.Take(&proof, "id_hash = ?", tokenHash(options.Proof)).Error)
	assert.Nil(t, proof.VerifiedAt)
	assert.Zero(t, proof.Attempts, "corrupted server-side credentials are not a bad password attempt")
}

func TestReauthenticationRequiredMFAWithoutEnrollmentCannotUsePasswordAlone(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	require.NoError(t, harness.db.Model(&model.Credential{}).Where("1 = 1").Update("mfa_required", true).Error)
	methods, err := service.AvailableReauthenticationMethods(t.Context(), cookie)
	require.NoError(t, err)
	assert.True(t, methods.MFARequired)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	require.NoError(t, err)
	assert.ErrorIs(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil), authnext.ErrInvalidCredentials)
}

func TestReauthenticationFederationCapabilityAndLocalMethodAvailability(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	methods, err := service.AvailableReauthenticationMethods(t.Context(), cookie)
	require.NoError(t, err)
	assert.Equal(t, []string{"password"}, methods.Methods)
	count, err := service.localLoginMethodsOn(t.Context(), harness.db, claims.PrincipalID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationTenantLeave, "tenant", "federation", "")
	require.NoError(t, err)
	binding, err := service.inspectFederatedReauthentication(t.Context(), cookie, options.Proof)
	require.NoError(t, err)
	harness.auditErr = assert.AnError
	assert.Error(t, harness.db.Transaction(func(tx *gorm.DB) error {
		return service.verifyFederatedReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, binding.ChallengeHash, "trusted-link", strings.Repeat("a", 64))
	}))
	harness.auditErr = nil
	require.NoError(t, harness.db.Transaction(func(tx *gorm.DB) error {
		return service.verifyFederatedReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, binding.ChallengeHash, "trusted-link", strings.Repeat("a", 64))
	}))
	assert.Error(t, harness.db.Transaction(func(tx *gorm.DB) error {
		return service.verifyFederatedReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, binding.ChallengeHash, "trusted-link", strings.Repeat("a", 64))
	}))
	_, err = service.inspectFederatedReauthentication(t.Context(), cookie, options.Proof)
	assert.Error(t, err)
	assert.ErrorIs(t, service.verifyFederatedReauthenticationOn(t.Context(), nil, claims.PrincipalID, cookie, binding.ChallengeHash, "trusted-link", strings.Repeat("a", 64)), authnext.ErrUnavailable)
	_, err = service.localLoginMethodsOn(t.Context(), nil, claims.PrincipalID)
	assert.ErrorIs(t, err, authnext.ErrUnavailable)
	require.NoError(t, harness.db.Delete(&model.Credential{}, "principal_id = ?", claims.PrincipalID).Error)
	methods, err = service.AvailableReauthenticationMethods(t.Context(), cookie)
	require.NoError(t, err)
	assert.Empty(t, methods.Methods)
	count, err = service.localLoginMethodsOn(t.Context(), harness.db, claims.PrincipalID)
	require.NoError(t, err)
	assert.Zero(t, count)
	_, err = service.BeginReauthentication(t.Context(), cookie, ReauthenticationTenantLeave, "tenant", "password", "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestReauthenticationRejectsInvalidInputsAndStorageFailure(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	for _, input := range []struct{ purpose, target, method string }{
		{"unknown", "target", "password"},
		{ReauthenticationPrincipalClose, "", "password"},
		{ReauthenticationPrincipalClose, strings.Repeat("x", 513), "password"},
		{ReauthenticationPrincipalClose, "self", "unknown"},
		{ReauthenticationPrincipalClose, "self", "passkey"},
	} {
		_, err := service.BeginReauthentication(t.Context(), cookie, input.purpose, input.target, input.method, "")
		assert.Error(t, err)
	}
	_, err := service.AvailableReauthenticationMethods(t.Context(), "")
	assert.Error(t, err)
	_, err = service.BeginReauthentication(t.Context(), "", ReauthenticationPrincipalClose, "self", "password", "")
	assert.Error(t, err)
	assert.Error(t, service.VerifyReauthentication(t.Context(), "", "proof", "password", "", "", nil))
	_, err = service.inspectFederatedReauthentication(t.Context(), "", "proof")
	assert.Error(t, err)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	assert.ErrorIs(t, service.ConsumeReauthenticationOn(t.Context(), nil, claims.PrincipalID, cookie, "proof", ReauthenticationPrincipalClose, "self"), authnext.ErrUnavailable)
	_, err = service.lockReauthenticationSession(t.Context(), harness.repository, 0, "")
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, "missing", "password", "", "", nil))
	password, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	require.NoError(t, err)
	_, err = service.inspectFederatedReauthentication(t.Context(), cookie, password.Proof)
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	assert.Error(t, harness.db.Transaction(func(tx *gorm.DB) error {
		return service.verifyFederatedReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, tokenHash(password.Proof), "trusted-link", strings.Repeat("a", 64))
	}))
	harness.auditErr = assert.AnError
	_, err = service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	assert.Error(t, err)
	harness.auditErr = nil
	require.NoError(t, harness.db.Migrator().DropTable(&model.ReauthenticationChallenge{}))
	_, err = service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	assert.Error(t, err)
	assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, password.Proof, "correct horse battery staple", "", "", nil))
}

func TestReauthenticationProofIsVerifiedScopedSessionBoundAndOneTime(t *testing.T) {
	service, harness, cookie := authenticatedLocalService(t)
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationIdentityLink, "provider-1", "password", "")
	require.NoError(t, err)
	consume := func(cookie, purpose, target string) error {
		return harness.db.Transaction(func(tx *gorm.DB) error {
			return service.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, options.Proof, purpose, target)
		})
	}
	assert.ErrorIs(t, consume(cookie, ReauthenticationIdentityLink, "provider-1"), authnext.ErrInvalidCredentials)
	assert.ErrorIs(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "wrong", "", "", nil), authnext.ErrInvalidCredentials)
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
	assert.ErrorIs(t, consume(cookie, ReauthenticationPersonalTokenCreate, "provider-1"), authnext.ErrInvalidCredentials)
	assert.ErrorIs(t, consume(cookie, ReauthenticationIdentityLink, "provider-2"), authnext.ErrInvalidCredentials)
	other, err := service.CreateSession(t.Context(), claims.PrincipalID, service.now(), assurancePassword(service.now()))
	require.NoError(t, err)
	assert.ErrorIs(t, consume(service.SessionCookie(other), ReauthenticationIdentityLink, "provider-1"), authnext.ErrInvalidCredentials)
	harness.auditErr = assert.AnError
	assert.Error(t, consume(cookie, ReauthenticationIdentityLink, "provider-1"))
	harness.auditErr = nil
	require.NoError(t, consume(cookie, ReauthenticationIdentityLink, "provider-1"))
	assert.ErrorIs(t, consume(cookie, ReauthenticationIdentityLink, "provider-1"), authnext.ErrInvalidCredentials)
}

func TestReauthenticationRequiresNewMFAAndInvalidatesChangedCredentials(t *testing.T) {
	service, harness, cookie, secret := enabledMFAService(t)
	service.now = func() time.Time { return repositoryTestNow.Add(30 * time.Second) }
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationPrincipalClose, "self", "password", "")
	require.NoError(t, err)
	assert.ErrorIs(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil), authnext.ErrInvalidCredentials)
	require.NoError(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", mustTOTP(t, secret, service.now()), "", nil))
	claims, err := service.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	require.NoError(t, harness.db.Model(&model.Credential{}).Where("principal_id = ?", claims.PrincipalID).Update("credential_version", gorm.Expr("credential_version + 1")).Error)
	err = harness.db.Transaction(func(tx *gorm.DB) error {
		return service.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, options.Proof, ReauthenticationPrincipalClose, "self")
	})
	assert.ErrorIs(t, err, authnext.ErrInvalidCredentials)
}

func TestReauthenticationRejectsExpiredExhaustedAndFederatedForgery(t *testing.T) {
	service, _, cookie := authenticatedLocalService(t)
	options, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationIdentityLink, "provider", "password", "")
	require.NoError(t, err)
	for range reauthenticationAttempts {
		assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "wrong", "", "", nil))
	}
	assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, options.Proof, "correct horse battery staple", "", "", nil))
	federated, err := service.BeginReauthentication(t.Context(), cookie, ReauthenticationIdentityLink, "provider", "federation", "")
	require.NoError(t, err)
	assert.Error(t, service.VerifyReauthentication(t.Context(), cookie, federated.Proof, "correct horse battery staple", "", "", nil))
	_, err = service.inspectFederatedReauthentication(t.Context(), cookie, federated.Proof)
	require.NoError(t, err)
	service.now = func() time.Time { return repositoryTestNow.Add(reauthenticationTTL + time.Second) }
	_, err = service.inspectFederatedReauthentication(t.Context(), cookie, federated.Proof)
	assert.Error(t, err)
}

// TestReauthenticationPrincipalLockMatchesTheOwnersCapability pins the one
// statement authn writes to a table identity owns purely to take a row lock,
// and proves it is character-for-character the statement identity's own
// PrincipalStateStore.LockActive sends. That equality is what lets authn stop
// naming the principal table and call the owner's capability instead without
// changing a single byte of SQL.
func TestReauthenticationPrincipalLockMatchesTheOwnersCapability(t *testing.T) {
	const frozen = `UPDATE "sl_iam_principals" SET "updated_at"=updated_at WHERE id = $1 AND status = $2`

	harness := newAuthnRepositoryHarness(t, "sl_")
	repo, recorded := newFrozenSQLRecorder(t, harness)
	service, _ := newLocalService(t)

	_, _ = service.lockReauthenticationSession(t.Context(), repo, 101, "session-hash")
	statements := recorded()
	require.NotEmpty(t, statements)
	assert.Equal(t, frozen, statements[0], "the lock is the first statement of the transaction")

	// The owner's capability, resolved the way a deployment resolves it.
	services := module.NewServices()
	require.NoError(t, identity.NewModule(harness.runtime, identity.Config{}).ProvideServices(services))
	require.NoError(t, services.Seal())
	principals, err := module.Resolve[identity.PrincipalStateStore](services)
	require.NoError(t, err)

	owner, ownerRecorded := newFrozenSQLRecorder(t, harness)
	require.NoError(t, principals.LockActive(t.Context(), owner.writer(), 101))
	assert.Equal(t, []string{frozen}, ownerRecorded())
}
