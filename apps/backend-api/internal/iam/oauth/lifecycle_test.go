package oauth

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var oauthErasureTime = time.Date(2026, 9, 6, 16, 0, 0, 0, time.UTC)

func TestOAuthErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	h := newOAuthHarness(t, "sl_")

	assert.ErrorContains(t, ErasePrincipalData(t.Context(), nil, "", 1, oauthErasureTime), "invalid principal erasure transaction")
	assert.ErrorContains(t, ErasePrincipalData(t.Context(), h.db, "", 0, oauthErasureTime), "invalid principal erasure transaction")
	assert.Error(t, ErasePrincipalData(t.Context(), h.db, "bad prefix; DROP TABLE x;--", 1, oauthErasureTime))
}

// TestOAuthErasePrincipalDataDestroysEveryStandingAuthorization covers all four
// tables this owner holds. A refresh token or an outstanding consent is a way
// back into the account, so leaving one behind would mean the principal is
// erased and still able to be acted for.
func TestOAuthErasePrincipalDataDestroysEveryStandingAuthorization(t *testing.T) {
	h := newOAuthHarness(t, "sl_")
	const target, bystander = guid.ID(5101), guid.ID(5102)
	seedOAuthClient(t, h, 601)
	for _, principal := range []guid.ID{target, bystander} {
		seedOAuthPrincipal(t, h, principal)
		seedOAuthAuthorizations(t, h, principal, 601)
	}

	tables := []string{"iam_oauth_consent_requests", "iam_oauth_codes", "iam_refresh_tokens", "iam_oauth_consents"}
	for _, table := range tables {
		require.Equal(t, int64(1), oauthRowCount(t, h, table, target), "%s must start populated", table)
	}

	require.NoError(t, ErasePrincipalData(t.Context(), h.db, h.prefix, target, oauthErasureTime))

	for _, table := range tables {
		assert.Zero(t, oauthRowCount(t, h, table, target), "%s must not keep the erased principal", table)
		assert.Equal(t, int64(1), oauthRowCount(t, h, table, bystander), "%s must keep an unrelated principal", table)
	}
}

func seedOAuthClient(t *testing.T, h *oauthHarness, clientID int64) {
	t.Helper()
	require.NoError(t, h.db.Exec(
		"INSERT INTO "+h.table("iam_oauth_clients")+
			" (id, tenant_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		clientID, oauthFixtureTenant, "Test client", oauthErasureTime, oauthErasureTime,
	).Error)
}

// oauthFixtureTenant is the tenant the harness already seeds. Reusing it keeps
// the fixture inside the referential integrity the schema declares.
const oauthFixtureTenant = 7

func seedOAuthPrincipal(t *testing.T, h *oauthHarness, principal guid.ID) {
	t.Helper()
	require.NoError(t, h.db.Table(h.table("iam_principals")).Create(map[string]any{
		"id": principal, "login_name": "subject-" + principal.String(), "status": "active",
		"display_name": "Subject", "created_at": oauthErasureTime, "updated_at": oauthErasureTime,
	}).Error)
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Create(map[string]any{
		"tenant_id": oauthFixtureTenant, "principal_id": principal, "status": "active",
	}).Error)
}

func seedOAuthAuthorizations(t *testing.T, h *oauthHarness, principal guid.ID, clientID int64) {
	t.Helper()
	suffix := principal.String()
	require.NoError(t, h.db.Exec(
		"INSERT INTO "+h.table("iam_oauth_consents")+
			" (principal_id, client_id, tenant_id, scope, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?)",
		principal, clientID, oauthFixtureTenant, "openid", oauthErasureTime, oauthErasureTime,
	).Error)
	require.NoError(t, h.db.Exec(
		"INSERT INTO "+h.table("iam_refresh_tokens")+
			" (id_hash, family_id, principal_id, client_id, scope, auth_time, created_at, expires_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		"hash-"+suffix, "family-"+suffix, principal, clientID, "openid",
		oauthErasureTime, oauthErasureTime, oauthErasureTime.Add(time.Hour),
	).Error)
	require.NoError(t, h.db.Exec(
		"INSERT INTO "+h.table("iam_oauth_codes")+
			" (code_hash, client_id, principal_id, redirect_uri, scope, code_challenge, auth_time, created_at, expires_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"code-"+suffix, clientID, principal, "https://app.example.test/callback", "openid",
		"challenge-"+suffix, oauthErasureTime, oauthErasureTime, oauthErasureTime.Add(time.Hour),
	).Error)
	require.NoError(t, h.db.Exec(
		"INSERT INTO "+h.table("iam_oauth_consent_requests")+
			" (challenge_hash, client_id, principal_id, redirect_uri, scope, state, code_challenge, created_at, expires_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"request-"+suffix, clientID, principal, "https://app.example.test/callback", "openid",
		"state-"+suffix, "challenge-"+suffix, oauthErasureTime, oauthErasureTime.Add(time.Hour),
	).Error)
}

func oauthRowCount(t *testing.T, h *oauthHarness, table string, principal guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table(h.table(table)).Where("principal_id = ?", principal).Count(&count).Error)
	return count
}

// Erasure runs inside the caller's suspension transaction. A table it cannot
// reach has to abort that transaction rather than report a clean sweep: a
// surviving refresh token or consent is a way back into the account, and the
// caller would commit the suspension believing there is none.
func TestOAuthErasePrincipalDataPropagatesAStorageFailure(t *testing.T) {
	h := newOAuthHarness(t, "sl_")
	const target = guid.ID(5103)
	seedOAuthClient(t, h, 602)
	seedOAuthPrincipal(t, h, target)
	seedOAuthAuthorizations(t, h, target, 602)
	// Consents are the last table the sweep reaches, so the three before it
	// succeed and the failure is the loop reporting a later table, not the first.
	require.NoError(t, h.db.Exec("DROP TABLE "+h.table("iam_oauth_consents")).Error)

	assert.Error(t, ErasePrincipalData(t.Context(), h.db, h.prefix, target, oauthErasureTime))
	assert.Zero(t, oauthRowCount(t, h, "iam_oauth_codes", target),
		"the tables the sweep already reached stay erased; the caller's transaction is what rolls them back")
}
