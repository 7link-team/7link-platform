package federation

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var federationErasureTime = time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

func TestFederationErasePrincipalDataRefusesUnusableArguments(t *testing.T) {
	env := newFederationEnvironment(t)

	assert.ErrorContains(t, ErasePrincipalData(t.Context(), nil, "", 1, federationErasureTime), "invalid principal erasure transaction")
	assert.ErrorContains(t, ErasePrincipalData(t.Context(), env.db, "", 0, federationErasureTime), "invalid principal erasure transaction")
	assert.Error(t, ErasePrincipalData(t.Context(), env.db, "bad prefix; DROP TABLE x;--", 1, federationErasureTime))
}

func TestFederationErasePrincipalDataRemovesUpstreamIdentityAndSparesEveryoneElse(t *testing.T) {
	env := newFederationEnvironment(t)
	target, bystander := guid.ID(4101), guid.ID(4102)
	seedIdentityLink(t, env, 91, target, "upstream-subject-a", "target@idp.example.test")
	// The same person can arrive from more than one provider, and erasure has to
	// account for all of them rather than the first one it finds.
	seedIdentityLink(t, env, 92, target, "upstream-subject-b", "target@idp.example.test")
	seedIdentityLink(t, env, 91, bystander, "upstream-subject-c", "other@idp.example.test")

	require.NoError(t, ErasePrincipalData(t.Context(), env.db, env.prefix, target, federationErasureTime))

	// The link is deleted rather than blanked: an external subject identifier is
	// the upstream's name for this person, so a redacted row would still say
	// which account at the provider this used to be.
	assert.Zero(t, identityLinkCount(t, env, target), "no upstream identity may survive")
	assert.Equal(t, int64(1), identityLinkCount(t, env, bystander), "an unrelated principal keeps its link")
}

func seedIdentityLink(t *testing.T, env *federationEnvironment, providerID int64, principal guid.ID, subject, email string) {
	t.Helper()
	require.NoError(t, env.db.Exec(
		"INSERT INTO "+env.prefix+"iam_identity_links"+
			" (provider_id, tenant_id, principal_id, external_subject, email, display_name, last_login_at, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		providerID, 7, principal, subject, email, "Upstream Person",
		federationErasureTime, federationErasureTime, federationErasureTime,
	).Error)
}

func identityLinkCount(t *testing.T, env *federationEnvironment, principal guid.ID) int64 {
	t.Helper()
	var count int64
	require.NoError(t, env.db.Table(env.prefix+"iam_identity_links").
		Where("principal_id = ?", principal).Count(&count).Error)
	return count
}
