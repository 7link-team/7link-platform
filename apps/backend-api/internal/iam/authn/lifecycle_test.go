package authn

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// authnErasureTime is the instant the seeded rows carry. Erasure does not read
// it, so any fixed value keeps the fixtures readable.
var authnErasureTime = time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

func TestAuthnErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")

	assert.ErrorIs(t, ErasePrincipalData(t.Context(), nil, "", 1, authnErasureTime), errRepositoryUnavailable,
		"erasure without a transaction cannot be part of the caller's atomic unit")
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), harness.db, "", 0, authnErasureTime), errRepositoryUnavailable)
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), harness.db, "", -1, authnErasureTime), errRepositoryUnavailable)

	// A prefix reaches the statement as an identifier, so a rejected one must
	// not become a table name.
	assert.Error(t, ErasePrincipalData(t.Context(), harness.db, "bad prefix; DROP TABLE x;--", 1, authnErasureTime))
}

func TestAuthnErasePrincipalDataRemovesSecretsAndSparesEveryoneElse(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run(prefixName(prefix), func(t *testing.T) {
			harness := newAuthnRepositoryHarness(t, prefix)
			const target, bystander = guid.ID(1), guid.ID(2)
			harness.seedPrincipal(t, target, "target")
			harness.seedPrincipal(t, bystander, "bystander")
			seedAuthnPrincipalData(t, harness, prefix, target, "pending")
			seedAuthnPrincipalData(t, harness, prefix, bystander, "pending")

			require.NoError(t, ErasePrincipalData(t.Context(), harness.db, prefix, target, authnErasureTime))

			for _, table := range []string{"iam_credentials", "iam_recovery_codes", "iam_notification_outbox"} {
				assert.Zero(t, authnRowCount(t, harness, prefix, table, target),
					"%s must not keep the erased principal", table)
				assert.Equal(t, int64(1), authnRowCount(t, harness, prefix, table, bystander),
					"%s must keep an unrelated principal", table)
			}
		})
	}
}

func TestAuthnErasePrincipalDataRefusesWhileANotificationIsDelivering(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	const target = guid.ID(1)
	harness.seedPrincipal(t, target, "target")
	seedAuthnPrincipalData(t, harness, "", target, "delivering")

	// A notification handed to a sender is already outside this transaction. If
	// erasure deleted the row anyway, the recipient's address would survive in
	// the sender rather than here, which is the one place erasure cannot reach.
	assert.ErrorIs(t, ErasePrincipalData(t.Context(), harness.db, "", target, authnErasureTime), ErrNotificationInFlight)
	assert.Equal(t, int64(1), authnRowCount(t, harness, "", "iam_credentials", target),
		"a refused erasure must leave the principal intact")
}

func seedAuthnPrincipalData(t *testing.T, harness *authnRepositoryHarness, prefix string, id guid.ID, notificationStatus string) {
	t.Helper()
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_credentials (principal_id, password_hash, password_changed_at, updated_at) VALUES (?, ?, ?, ?)",
		id, "not-a-real-hash", authnErasureTime, authnErasureTime,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_recovery_codes (principal_id, code_hash, created_at) VALUES (?, ?, ?)",
		id, "not-a-real-code-digest", authnErasureTime,
	).Error)
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_notification_outbox"+
			" (id, principal_id, kind, recipient, payload_ciphertext, status, available_at, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		int64(id)*100, id, "verification", "person@example.test", "not-real-ciphertext",
		notificationStatus, authnErasureTime, authnErasureTime, authnErasureTime,
	).Error)
}

func authnRowCount(t *testing.T, harness *authnRepositoryHarness, prefix, table string, id guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, harness.db.Table(prefix+table).Where("principal_id = ?", id).Count(&count).Error)
	return count
}

func prefixName(prefix string) string {
	if prefix == "" {
		return "plain"
	}
	return "prefixed"
}
