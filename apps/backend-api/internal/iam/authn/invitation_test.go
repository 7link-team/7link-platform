package authn

import (
	"encoding/base64"
	"testing"

	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvitationCredentialLifecycle(t *testing.T) {
	harness := newAuthnRepositoryHarness(t, "")
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	service, err := NewWebService(authnext.Config{
		Enabled: true, Issuer: "https://iam.example.test", SigningKey: key,
		Web: authnext.WebConfig{
			Enabled: true, PostLoginURL: "https://app.example.test/home",
			AllowedReturnURLs: []string{"https://app.example.test/home"},
		},
		MFA: authnext.MFAConfig{EncryptionKey: key},
	}, harness.repository)
	require.NoError(t, err)

	id := guid.ID(1001)
	token, digest, err := service.IssueInvitationToken(id)
	require.NoError(t, err)
	assert.Contains(t, token, "inv1_"+id.String()+".")
	assert.NotContains(t, digest, token)
	recomputed, err := service.InvitationTokenDigest(token)
	require.NoError(t, err)
	assert.Equal(t, digest, recomputed)

	otherToken, otherDigest, err := service.IssueInvitationToken(id)
	require.NoError(t, err)
	assert.NotEqual(t, token, otherToken)
	assert.NotEqual(t, digest, otherDigest)
	_, err = service.InvitationTokenDigest("invalid")
	assert.Error(t, err)
	_, _, err = service.IssueInvitationToken(0)
	assert.Error(t, err)
}
