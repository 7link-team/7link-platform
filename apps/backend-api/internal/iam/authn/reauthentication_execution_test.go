package authn

import (
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFullAutoProofUsesCanonicalPurposeTargetFreshnessAndSingleUse(t *testing.T) {
	s, h, cookie := authenticatedLocalService(t)
	claims, err := s.Authenticate(t.Context(), "", cookie)
	require.NoError(t, err)
	p, err := s.BeginReauthentication(t.Context(), cookie, ReauthenticationExecutionFullAuto, "execution:99", "password", "")
	require.NoError(t, err)
	consume := func(purpose, target string) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			return s.ConsumeReauthenticationOn(t.Context(), tx, claims.PrincipalID, cookie, p.Proof, purpose, target)
		})
	}
	require.Error(t, consume(ReauthenticationExecutionFullAuto, "execution:99"))
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, p.Proof, "correct horse battery staple", "", "", nil))
	require.Error(t, consume(ReauthenticationScopeChange, "execution:99"))
	require.Error(t, consume(ReauthenticationExecutionFullAuto, "execution:100"))
	require.NoError(t, consume(ReauthenticationExecutionFullAuto, "execution:99"))
	require.Error(t, consume(ReauthenticationExecutionFullAuto, "execution:99"))
	p, err = s.BeginReauthentication(t.Context(), cookie, ReauthenticationExecutionFullAuto, "execution:100", "password", "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyReauthentication(t.Context(), cookie, p.Proof, "correct horse battery staple", "", "", nil))
	oldNow := s.now
	s.now = func() time.Time { return oldNow().Add(6 * time.Minute) }
	require.Error(t, consume(ReauthenticationExecutionFullAuto, "execution:100"))
}
func TestFullAutoPurposeSchemaIsIdempotent(t *testing.T) {
	s, _, _ := authenticatedLocalService(t)
	_, api := humatest.New(t)
	registerReauthenticationHTTP(api, s)
	RegisterExecutionReauthentication(api)
	RegisterExecutionReauthentication(api)
	require.True(t, validReauthenticationPurpose(ReauthenticationExecutionFullAuto))
}
