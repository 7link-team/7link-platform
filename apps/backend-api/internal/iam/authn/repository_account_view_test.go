package authn

import (
	"testing"
	"time"

	"7link.com/platform/internal/iam/authn/model"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func accountViewFixture(t *testing.T, prefix string) (*authnRepositoryHarness, model.AccountViewToken) {
	t.Helper()
	h := newAuthnRepositoryHarness(t, prefix)
	h.seedPrincipal(t, 101, "operator")
	h.seedPrincipal(t, 202, "target")
	h.seedPrincipal(t, 303, "other")
	for _, id := range []guid.ID{101, 202} {
		session := model.Session{
			IDHash: tokenHash("browser-" + id.String()), PrincipalID: id,
			CreatedAt: repositoryTestNow, AuthTime: repositoryTestNow, LastSeenAt: repositoryTestNow,
			ACR: 1, AMR: "pwd", ExpiresAt: repositoryTestNow.Add(time.Hour), AbsoluteExpiresAt: repositoryTestNow.Add(2 * time.Hour),
		}
		require.NoError(t, h.repository.createSession(t.Context(), &session))
	}
	return h, model.AccountViewToken{
		ID: 501, TokenHash: tokenHash("avt_dedicated-secret"), ActorID: 101, PrincipalID: 202,
		ActorSessionHash: tokenHash("browser-101"), ActorCredentialVersion: 1, TargetCredentialVersion: 1,
		ScopeType: "personal", ScopeID: 202, CreatedAt: repositoryTestNow, ExpiresAt: repositoryTestNow.Add(30 * time.Minute),
	}
}

func TestAccountViewTokenStorageIsolationAndRevocation(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run(prefixName(prefix), func(t *testing.T) {
			h, row := accountViewFixture(t, prefix)
			repo := h.repository
			var before, after []model.Session
			require.NoError(t, h.db.Table(repo.names.Sessions.String()).Order("id_hash").Find(&before).Error)
			require.NoError(t, repo.createAccountViewToken(t.Context(), &row))
			assert.Equal(t, "read_only", row.Mode)
			stored, err := repo.activeAccountViewToken(t.Context(), row.TokenHash, repositoryTestNow)
			require.NoError(t, err)
			assert.Equal(t, row.ActorID, stored.ActorID)
			assert.Equal(t, row.PrincipalID, stored.PrincipalID)
			assert.NotEqual(t, "avt_dedicated-secret", stored.TokenHash)
			_, err = repo.activeAccountViewToken(t.Context(), "avt_dedicated-secret", repositoryTestNow)
			assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
			usedAt := repositoryTestNow.Add(time.Minute)
			touched, err := repo.touchAccountViewToken(t.Context(), row.ID, usedAt)
			require.NoError(t, err)
			assert.True(t, touched)
			stored, err = repo.activeAccountViewToken(t.Context(), row.TokenHash, usedAt)
			require.NoError(t, err)
			require.NotNil(t, stored.LastUsedAt)
			assert.True(t, stored.LastUsedAt.Equal(usedAt))
			touched, err = repo.touchAccountViewToken(t.Context(), row.ID, repositoryTestNow)
			require.NoError(t, err)
			assert.False(t, touched, "an older request must not move last-use time backwards")
			require.NoError(t, h.db.Table(repo.names.Sessions.String()).Order("id_hash").Find(&after).Error)
			assert.Equal(t, before, after, "view use must not touch either browser session")
			changed, err := repo.revokeAccountViewToken(t.Context(), row.ID, 303, usedAt)
			require.NoError(t, err)
			assert.False(t, changed)
			changed, err = repo.revokeAccountViewToken(t.Context(), row.ID, row.ActorID, usedAt)
			require.NoError(t, err)
			assert.True(t, changed)
			_, err = repo.activeAccountViewToken(t.Context(), row.TokenHash, usedAt)
			assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
			changed, err = repo.revokeAccountViewToken(t.Context(), row.ID, row.ActorID, usedAt.Add(time.Minute))
			require.NoError(t, err)
			assert.False(t, changed)
			assert.Error(t, h.db.Table(repo.names.AccountViewTokens.String()).Where("id = ?", row.ID).Update("revoked_at", nil).Error)
			var personalCount int64
			require.NoError(t, h.db.Table(repo.names.PersonalTokens.String()).Count(&personalCount).Error)
			assert.Zero(t, personalCount)
		})
	}
}

func TestAccountViewTokenCannotRetargetEscalateOrExtend(t *testing.T) {
	h, row := accountViewFixture(t, "")
	require.NoError(t, h.repository.createAccountViewToken(t.Context(), &row))
	for column, value := range map[string]any{
		"id": 999, "actor_id": 303, "principal_id": 303, "mode": "read_write",
		"scope_type": "tenant", "scope_id": 303, "token_hash": tokenHash("replacement"),
		"actor_session_hash": tokenHash("browser-202"), "actor_credential_version": 2,
		"target_credential_version": 2, "created_at": repositoryTestNow.Add(time.Minute),
		"expires_at": repositoryTestNow.Add(2 * time.Hour),
	} {
		t.Run(column, func(t *testing.T) {
			assert.Error(t, h.db.Table(h.repository.names.AccountViewTokens.String()).Where("id = ?", row.ID).Update(column, value).Error)
		})
	}
	row.ID, row.TokenHash, row.Mode = 502, tokenHash("explicit-write-view"), "read_write"
	require.NoError(t, h.repository.createAccountViewToken(t.Context(), &row))
	stored, err := h.repository.activeAccountViewToken(t.Context(), row.TokenHash, repositoryTestNow)
	require.NoError(t, err)
	assert.Equal(t, "read_write", stored.Mode)
	row.ID, row.TokenHash, row.Mode = 503, tokenHash("database-default-view"), ""
	require.NoError(t, h.db.Omit("mode").Create(&row).Error)
	stored, err = h.repository.activeAccountViewToken(t.Context(), row.TokenHash, repositoryTestNow)
	require.NoError(t, err)
	assert.Equal(t, "read_only", stored.Mode, "database default must be read-only when a writer omits mode")
}

func TestAccountViewTokenRejectsExpiredRevokedAndWrongOperatorSessions(t *testing.T) {
	for _, scenario := range []string{"not-yet-issued", "expired-token", "expired-session", "revoked-session", "wrong-session-owner", "missing-session"} {
		t.Run(scenario, func(t *testing.T) {
			h, row := accountViewFixture(t, "")
			at := repositoryTestNow
			switch scenario {
			case "not-yet-issued":
				at = row.CreatedAt.Add(-time.Millisecond)
			case "expired-token":
				at = row.ExpiresAt
			case "expired-session":
				require.NoError(t, h.db.Model(&model.Session{}).Where("id_hash = ?", row.ActorSessionHash).Update("expires_at", at).Error)
			case "revoked-session":
				_, err := h.repository.revokeSession(t.Context(), row.ActorID, row.ActorSessionHash, at)
				require.NoError(t, err)
			case "wrong-session-owner":
				row.ActorSessionHash = tokenHash("browser-202")
			case "missing-session":
				row.ActorSessionHash = tokenHash("missing-browser")
			}
			require.NoError(t, h.repository.createAccountViewToken(t.Context(), &row))
			_, err := h.repository.activeAccountViewToken(t.Context(), row.TokenHash, at)
			assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
		})
	}
}

func TestAccountViewPrincipalRevocationIncludesActorAndTarget(t *testing.T) {
	h, row := accountViewFixture(t, "")
	rows := []model.AccountViewToken{row, row, row}
	rows[1].ID, rows[1].TokenHash, rows[1].ActorID, rows[1].PrincipalID = 502, tokenHash("reverse-view"), 202, 101
	rows[2].ID, rows[2].TokenHash, rows[2].ActorID, rows[2].PrincipalID = 503, tokenHash("unrelated-view"), 202, 303
	for i := range rows {
		require.NoError(t, h.repository.createAccountViewToken(t.Context(), &rows[i]))
	}
	require.ErrorIs(t, h.repository.runInTx(t.Context(), func(repo *Repository) error {
		require.NoError(t, repo.revokePrincipalAccountViews(t.Context(), 101, repositoryTestNow))
		return assert.AnError
	}), assert.AnError)
	var stored []model.AccountViewToken
	require.NoError(t, h.db.Order("id").Find(&stored).Error)
	for _, item := range stored {
		assert.Nil(t, item.RevokedAt)
	}
	require.NoError(t, h.repository.revokePrincipalAccountViews(t.Context(), 101, repositoryTestNow))
	require.NoError(t, h.db.Order("id").Find(&stored).Error)
	assert.NotNil(t, stored[0].RevokedAt)
	assert.NotNil(t, stored[1].RevokedAt)
	assert.Nil(t, stored[2].RevokedAt)
	require.NoError(t, h.repository.revokePrincipalAccountViews(t.Context(), 101, repositoryTestNow.Add(time.Minute)))
	require.NoError(t, ErasePrincipalData(t.Context(), h.db, "", 101, repositoryTestNow))
	stored = nil
	require.NoError(t, h.db.Order("id").Find(&stored).Error)
	require.Len(t, stored, 1)
	assert.EqualValues(t, 503, stored[0].ID)
}

func TestAccountViewTokenCannotRecreateAnErasedActorOrTarget(t *testing.T) {
	for _, principalID := range []guid.ID{101, 202} {
		t.Run(principalID.String(), func(t *testing.T) {
			h, row := accountViewFixture(t, "sl_")
			require.NoError(t, h.db.Exec("INSERT INTO sl_iam_principal_lifecycles (principal_id, version, retain_until, legal_hold, reference, anonymized_at, updated_at) VALUES (?, 1, ?, 0, '', ?, ?)",
				principalID, repositoryTestNow, repositoryTestNow, repositoryTestNow).Error)
			err := h.repository.createAccountViewToken(t.Context(), &row)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "permanently anonymized")
		})
	}
}
