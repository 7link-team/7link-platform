package organization

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var organizationErasureTime = time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)

func TestOrganizationErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	harness := newOrganizationHarness(t, "")

	assert.ErrorContains(t, ErasePrincipalData(t.Context(), nil, "", 1, organizationErasureTime), "invalid principal erasure transaction")
	assert.ErrorContains(t, ErasePrincipalData(t.Context(), harness.db, "", 0, organizationErasureTime), "invalid principal erasure transaction")
	assert.Error(t, ErasePrincipalData(t.Context(), harness.db, "bad prefix; DROP TABLE x;--", 1, organizationErasureTime))
}

func TestOrganizationErasePrincipalDataClearsAcceptedInvitationsOnly(t *testing.T) {
	for _, prefix := range []string{"", "sl_"} {
		t.Run(organizationPrefixName(prefix), func(t *testing.T) {
			harness := newOrganizationHarness(t, prefix)
			harness.seedTenant(t, guid.ID(900), "active")
			target, bystander := guid.ID(31), guid.ID(32)
			accepted := seedInvitation(t, harness, prefix, 1, "target@example.test", &target)
			otherPerson := seedInvitation(t, harness, prefix, 2, "other@example.test", &bystander)
			// An invitation nobody accepted names a recipient who may never have
			// become a principal, so it is not this principal's to erase.
			pending := seedInvitation(t, harness, prefix, 3, "invited@example.test", nil)

			require.NoError(t, ErasePrincipalData(t.Context(), harness.db, prefix, target, organizationErasureTime))

			email, key := readInvitation(t, harness, prefix, accepted)
			assert.Empty(t, email, "the accepted invitation must not keep the erased address")
			assert.Empty(t, key, "the lookup key is as identifying as the address it indexes")

			email, key = readInvitation(t, harness, prefix, otherPerson)
			assert.Equal(t, "other@example.test", email, "an unrelated invitation is untouched")
			assert.NotEmpty(t, key)

			email, _ = readInvitation(t, harness, prefix, pending)
			assert.Equal(t, "invited@example.test", email, "an unaccepted invitation belongs to its recipient")
		})
	}
}

func seedInvitation(t *testing.T, harness *organizationHarness, prefix string, id int64, email string, acceptedBy *guid.ID) int64 {
	t.Helper()
	var accepted any
	if acceptedBy != nil {
		accepted = *acceptedBy
	}
	require.NoError(t, harness.db.Exec(
		"INSERT INTO "+prefix+"iam_invitations"+
			" (tenant_id, id, email, email_key, token_hmac, status, expires_at, accepted_by, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)",
		int64(900), id, email, email, "not-a-real-token-hmac-"+email, organizationErasureTime,
		accepted, organizationErasureTime, organizationErasureTime,
	).Error)
	return id
}

func readInvitation(t *testing.T, harness *organizationHarness, prefix string, id int64) (string, string) {
	t.Helper()
	var row struct {
		Email    string
		EmailKey string
	}
	require.NoError(t, harness.db.Table(prefix+"iam_invitations").
		Select("email, email_key").Where("id = ?", id).Scan(&row).Error)
	return row.Email, row.EmailKey
}

func organizationPrefixName(prefix string) string {
	if prefix == "" {
		return "plain"
	}
	return "prefixed"
}
