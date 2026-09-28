package identity

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func configureClosureProcessing(h *identityHarness) {
	h.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return nil }
	h.service.closeCredentials = func(ctx context.Context, tx *gorm.DB, id guid.ID) error {
		return tx.WithContext(ctx).Table(h.table("iam_credentials")).Where("principal_id = ?", id).Update("credential_version", gorm.Expr("credential_version + 1")).Error
	}
	h.service.closeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return nil }
}

func TestClosureProcessingZeroTenantAndRestoreCannotReviveSecurityState(t *testing.T) {
	h, p := closureHarness(t)
	configureClosureProcessing(h)
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ?", p.ID).Delete(map[string]any{}).Error)
	h.seedSession(t, 1, p.ID)
	h.seedRefreshToken(t, "refresh", p.ID)
	h.seedRecoveryToken(t, 1, p.ID)
	h.seedVerificationToken(t, 1, p.ID)
	_, err := h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.NoError(t, err)
	ctx := authnext.WithClaims(t.Context(), &authnext.Claims{PrincipalID: 99, SubjectType: authnext.SubjectTypePrincipal})
	request, err := h.service.ProcessClosureRequest(ctx, p.ID, 1)
	require.NoError(t, err)
	require.Equal(t, "processing", request.Status)
	require.Equal(t, "disabled", h.principal(t, p.ID).Status)
	require.EqualValues(t, 2, h.credentialVersion(t, p.ID))
	require.Empty(t, h.guard.calls)
	policy, err := h.service.GetPrincipalLifecycle(ctx, p.ID)
	require.NoError(t, err)
	require.Zero(t, policy.Version)
	for _, table := range []string{"iam_sessions", "iam_refresh_tokens"} {
		var active int64
		require.NoError(t, h.db.Table(h.table(table)).Where("principal_id = ? AND revoked_at IS NULL", p.ID).Count(&active).Error)
		require.Zero(t, active)
	}
	for _, table := range []string{"iam_password_recovery_tokens", "iam_email_verification_tokens"} {
		var active int64
		require.NoError(t, h.db.Table(h.table(table)).Where("principal_id = ? AND consumed_at IS NULL", p.ID).Count(&active).Error)
		require.Zero(t, active)
	}
	events := h.events()
	last := events[len(events)-1]
	require.Equal(t, guid.ID(99), last.PrincipalID)
	require.Equal(t, p.ID, last.TargetID)
	_, err = h.service.ProcessClosureRequest(ctx, p.ID, 2)
	require.ErrorIs(t, err, ErrClosureConflict)
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error { return nil }
	policy, err = h.service.SetPrincipalRetention(ctx, p.ID, 0, time.Now().Add(-time.Hour), false, "closure-case")
	require.NoError(t, err)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.NoError(t, err)
	completed, err := h.service.GetClosureRequest(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
}

func TestClosureProcessingPlatformAndTenantGuardsRollback(t *testing.T) {
	for _, platform := range []bool{true, false} {
		t.Run(map[bool]string{true: "platform", false: "tenant"}[platform], func(t *testing.T) {
			h, p := closureHarness(t)
			configureClosureProcessing(h)
			_, err := h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
			require.NoError(t, err)
			want := core.ErrLastTenantAdministrator
			if platform {
				want = core.ErrPrincipalHasPlatformAccess
				h.service.platformAccessGuard = func(context.Context, *gorm.DB, guid.ID) error { return want }
			} else {
				h.guard.verifyErr = want
			}
			_, err = h.service.ProcessClosureRequest(t.Context(), p.ID, 1)
			require.ErrorIs(t, err, want)
			require.Equal(t, "active", h.principal(t, p.ID).Status)
			require.EqualValues(t, 1, h.credentialVersion(t, p.ID))
			state, err := h.service.GetClosureRequest(t.Context(), p.ID)
			require.NoError(t, err)
			require.Equal(t, "pending", state.Status)
		})
	}
}

func TestClosureProcessingFailuresAreAtomic(t *testing.T) {
	for _, stage := range []string{"administrator", "credentials", "oauth", "audit", "security-storage", "membership-storage", "principal-write", "request-write", "request-write-suppressed"} {
		t.Run(stage, func(t *testing.T) {
			h, p := closureHarness(t)
			configureClosureProcessing(h)
			_, err := h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
			require.NoError(t, err)
			want := errors.New("processing dependency unavailable")
			switch stage {
			case "administrator":
				h.guard.protectErr = want
			case "credentials":
				h.service.closeCredentials = func(context.Context, *gorm.DB, guid.ID) error { return want }
			case "oauth":
				h.service.closeOAuth = func(context.Context, *gorm.DB, guid.ID) error { return want }
			case "audit":
				h.auditErr = want
			case "security-storage":
				require.NoError(t, h.db.Exec("DROP TABLE close_iam_password_recovery_tokens").Error)
			case "membership-storage":
				require.NoError(t, h.db.Exec("DROP TABLE close_iam_tenant_members").Error)
			case "principal-write":
				require.NoError(t, h.db.Exec("CREATE TRIGGER reject_principal_disable BEFORE UPDATE OF status ON close_iam_principals BEGIN SELECT RAISE(ABORT, 'storage rejected'); END").Error)
			case "request-write":
				require.NoError(t, h.db.Exec("CREATE TRIGGER reject_closure_processing BEFORE UPDATE ON close_iam_closure_requests BEGIN SELECT RAISE(ABORT, 'storage rejected'); END").Error)
			case "request-write-suppressed":
				require.NoError(t, h.db.Exec("CREATE TRIGGER suppress_closure_processing BEFORE UPDATE ON close_iam_closure_requests BEGIN SELECT RAISE(IGNORE); END").Error)
			}
			_, err = h.service.ProcessClosureRequest(t.Context(), p.ID, 1)
			require.Error(t, err)
			if stage == "request-write-suppressed" {
				require.ErrorIs(t, err, ErrClosureConflict)
			}
			require.Equal(t, "active", h.principal(t, p.ID).Status)
			require.EqualValues(t, 1, h.credentialVersion(t, p.ID))
			state, err := h.service.GetClosureRequest(t.Context(), p.ID)
			require.NoError(t, err)
			require.Equal(t, "pending", state.Status)
		})
	}
}

// TestClosureRollbackLeavesEveryAuthnOwnedRowUntouched pins the invariant the
// existing atomicity test leaves implicit: when a later stage fails, the
// security state this transaction revoked must come back too. Sessions and both
// credential-token tables belong to authn, so whichever module ends up writing
// them, it has to write them inside the caller's transaction -- a capability
// that opened its own connection would leave these rows revoked after the
// rollback and no other test would notice.
func TestClosureRollbackLeavesEveryAuthnOwnedRowUntouched(t *testing.T) {
	h, p := closureHarness(t)
	configureClosureProcessing(h)
	_, err := h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.NoError(t, err)

	require.NoError(t, h.db.Table(h.table("iam_sessions")).Create(map[string]any{
		"id": 9001, "principal_id": p.ID,
	}).Error)
	for index, table := range []string{"iam_password_recovery_tokens", "iam_email_verification_tokens"} {
		require.NoError(t, h.db.Table(h.table(table)).Create(map[string]any{
			"id": 9100 + index, "principal_id": p.ID,
		}).Error)
	}

	// Fail after the security state has already been revoked in this transaction.
	h.service.closeOAuth = func(context.Context, *gorm.DB, guid.ID) error {
		return errors.New("processing dependency unavailable")
	}
	_, err = h.service.ProcessClosureRequest(t.Context(), p.ID, 1)
	require.Error(t, err)

	var revoked int64
	require.NoError(t, h.db.Table(h.table("iam_sessions")).
		Where("principal_id = ? AND revoked_at IS NOT NULL", p.ID).Count(&revoked).Error)
	assert.Zero(t, revoked, "a rolled back closure must not leave a session revoked")
	for _, table := range []string{"iam_password_recovery_tokens", "iam_email_verification_tokens"} {
		var consumed int64
		require.NoError(t, h.db.Table(h.table(table)).
			Where("principal_id = ? AND consumed_at IS NOT NULL", p.ID).Count(&consumed).Error)
		assert.Zero(t, consumed, "a rolled back closure must not leave %s consumed", table)
	}
	assert.Equal(t, "active", h.principal(t, p.ID).Status)
}
