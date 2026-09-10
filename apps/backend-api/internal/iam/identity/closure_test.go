package identity

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/iam/identity/model"
	"7link.com/platform/internal/infra/guid"
	authnext "7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func closureHarness(t *testing.T) (*identityHarness, Principal) {
	t.Helper()
	h := newIdentityHarness(t, "close_")
	p, err := h.service.Create(t.Context(), 7, "closure-person", testPassword, "Person", "person@example.test")
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("CREATE TABLE proof_uses (count INTEGER NOT NULL)").Error)
	require.NoError(t, h.db.Exec("INSERT INTO proof_uses VALUES (0)").Error)
	h.service.reauthenticate = func(ctx context.Context, tx *gorm.DB, id guid.ID, cookie, proof, purpose, target string) error {
		if id != p.ID || cookie != "browser" || proof != "verified" || purpose != "principal_close" || target != p.ID.String() {
			return authnext.ErrInvalidCredentials
		}
		return tx.WithContext(ctx).Exec("UPDATE proof_uses SET count = count + 1").Error
	}
	return h, p
}

func TestClosureRequestLifecycleAndNoAutomaticErasure(t *testing.T) {
	h, p := closureHarness(t)
	ctx := t.Context()
	// A global request must also work after the last membership is gone.
	require.NoError(t, h.db.Table(h.table("iam_tenant_members")).Where("principal_id = ?", p.ID).Delete(map[string]any{}).Error)
	h.seedSession(t, 1, p.ID)
	initial, err := h.service.GetClosureRequest(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "none", initial.Status)
	require.Zero(t, initial.Version)
	pending, err := h.service.ChangeClosureRequest(ctx, p.ID, 0, "pending", "browser", "verified")
	require.NoError(t, err)
	require.EqualValues(t, 1, pending.Version)
	require.Equal(t, "active", h.principal(t, p.ID).Status)
	policy, err := h.service.GetPrincipalLifecycle(ctx, p.ID)
	require.NoError(t, err)
	require.Zero(t, policy.Version, "request must not establish retention")
	var sessions int64
	require.NoError(t, h.db.Table(h.table("iam_sessions")).Where("revoked_at IS NULL").Count(&sessions).Error)
	require.EqualValues(t, 1, sessions)
	require.Empty(t, h.guard.calls, "request does not change administrator authority")
	_, err = h.service.ChangeClosureRequest(ctx, p.ID, 0, "canceled", "browser", "")
	require.ErrorIs(t, err, ErrClosureConflict)
	canceled, err := h.service.ChangeClosureRequest(ctx, p.ID, 1, "canceled", "browser", "")
	require.NoError(t, err)
	require.Equal(t, "canceled", canceled.Status)
	pending, err = h.service.ChangeClosureRequest(ctx, p.ID, 2, "pending", "browser", "verified")
	require.NoError(t, err)
	rejected, err := h.service.ChangeClosureRequest(ctx, p.ID, pending.Version, "rejected", "", "")
	require.NoError(t, err)
	require.Equal(t, "rejected", rejected.Status)
	_, err = h.service.ChangeClosureRequest(ctx, p.ID, rejected.Version, "canceled", "browser", "")
	require.ErrorIs(t, err, ErrClosureConflict)
	items, total, err := h.service.ListClosureRequests(ctx, "rejected", 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, p.ID, items[0].PrincipalID)
	_, total, err = h.service.ListClosureRequests(ctx, "pending", 0, 10)
	require.NoError(t, err)
	require.Zero(t, total)
}

func TestClosureRequestProofAndAuditRollback(t *testing.T) {
	h, p := closureHarness(t)
	_, err := h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "wrong")
	require.ErrorIs(t, err, authnext.ErrInvalidCredentials)
	h.auditErr = errors.New("audit unavailable")
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.ErrorIs(t, err, h.auditErr)
	var used int
	require.NoError(t, h.db.Raw("SELECT count FROM proof_uses").Scan(&used).Error)
	require.Zero(t, used, "proof and request roll back together")
	state, err := h.service.GetClosureRequest(t.Context(), p.ID)
	require.NoError(t, err)
	require.Equal(t, "none", state.Status)
	h.auditErr = nil
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.NoError(t, err)
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 1, "pending", "browser", "verified")
	require.ErrorIs(t, err, ErrClosureConflict)
	require.NoError(t, h.db.Raw("SELECT count FROM proof_uses").Scan(&used).Error)
	require.Equal(t, 1, used)
}

func TestClosureRequestCompletionUsesLifecycleAndFencesTombstone(t *testing.T) {
	h, p := closureHarness(t)
	ctx := t.Context()
	pending, err := h.service.ChangeClosureRequest(ctx, p.ID, 0, "pending", "browser", "verified")
	require.NoError(t, err)
	h.service.anonymizer = func(context.Context, *gorm.DB, Principal, time.Time) error { return nil }
	policy, err := h.service.SetPrincipalRetention(ctx, p.ID, 0, time.Now().Add(-time.Hour), true, "case")
	require.NoError(t, err)
	_, err = h.service.SetStatus(ctx, 7, p.ID, "disabled")
	require.NoError(t, err)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.ErrorIs(t, err, ErrPrincipalRetained)
	policy, err = h.service.SetPrincipalRetention(ctx, p.ID, policy.Version, time.Now().Add(-time.Hour), false, "case")
	require.NoError(t, err)
	_, err = h.service.AnonymizePrincipal(ctx, p.ID, policy.Version, p.LoginName)
	require.NoError(t, err)
	completed, err := h.service.GetClosureRequest(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
	items, total, err := h.service.ListClosureRequests(ctx, "completed", 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	_, total, err = h.service.ListClosureRequests(ctx, "pending", 0, 10)
	require.NoError(t, err)
	require.Zero(t, total)
	_, err = h.service.ChangeClosureRequest(ctx, p.ID, pending.Version, "rejected", "", "")
	require.ErrorIs(t, err, ErrPrincipalAnonymized)
	require.Error(t, h.db.Table(h.table("iam_closure_requests")).Where("principal_id = ?", p.ID).Update("status", "pending").Error)
	// Even deleting a request cannot permit a fresh applicant row on a tombstone.
	require.NoError(t, h.db.Table(h.table("iam_closure_requests")).Where("principal_id = ?", p.ID).Delete(map[string]any{}).Error)
	require.Error(t, h.db.Table(h.table("iam_closure_requests")).Create(&model.ClosureRequest{PrincipalID: p.ID, Status: "pending", Version: 1, RequestedAt: time.Now(), UpdatedAt: time.Now()}).Error)
}

func TestClosureRequestConcurrentSubmissionsHaveOneWinner(t *testing.T) {
	h, p := closureHarness(t)
	var winners atomic.Int32
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if _, err := h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified"); err == nil {
				winners.Add(1)
			}
		})
	}
	group.Wait()
	require.EqualValues(t, 1, winners.Load())
	var used int
	require.NoError(t, h.db.Raw("SELECT count FROM proof_uses").Scan(&used).Error)
	require.Equal(t, 1, used)
}

func TestClosureRequestRejectsInvalidOrUnavailableOperations(t *testing.T) {
	unavailable := &Service{repo: &Repository{}}
	_, err := unavailable.GetClosureRequest(t.Context(), 11)
	require.ErrorIs(t, err, errRepositoryUnavailable)
	_, _, err = unavailable.ListClosureRequests(t.Context(), "pending", 0, 50)
	require.ErrorIs(t, err, errRepositoryUnavailable)
	h, p := closureHarness(t)
	_, err = h.service.GetClosureRequest(t.Context(), 0)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.GetClosureRequest(t.Context(), 999)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = h.service.ChangeClosureRequest(t.Context(), 999, 1, "rejected", "", "")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, h.db.Table(h.table("iam_principals")).Where("id = ?", p.ID).Update("status", "disabled").Error)
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.ErrorIs(t, err, ErrPrincipalInactive)
	require.NoError(t, h.db.Table(h.table("iam_principals")).Where("id = ?", p.ID).Update("status", "active").Error)
	for _, status := range []string{"", "completed", "processing"} {
		_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 0, status, "browser", "verified")
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, -1, "pending", "browser", "verified")
	require.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "canceled", "browser", "")
	require.ErrorIs(t, err, ErrClosureConflict)
	h.service.reauthenticate = nil
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = h.service.ProcessClosureRequest(t.Context(), p.ID, 0)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = h.service.ProcessClosureRequest(t.Context(), p.ID, 1)
	require.ErrorIs(t, err, ErrUnavailable)
	configureClosureProcessing(h)
	_, err = h.service.ProcessClosureRequest(t.Context(), 999, 1)
	require.ErrorIs(t, err, ErrNotFound)
	for _, args := range []struct {
		status        string
		offset, limit int
	}{{"unknown", 0, 50}, {"", -1, 50}, {"", 0, 0}, {"", 0, 201}} {
		_, _, err = h.service.ListClosureRequests(t.Context(), args.status, args.offset, args.limit)
		require.ErrorIs(t, err, ErrInvalid)
	}
	require.NoError(t, h.db.Exec("DROP TABLE close_iam_closure_requests").Error)
	_, err = h.service.GetClosureRequest(t.Context(), p.ID)
	require.Error(t, err)
	_, _, err = h.service.ListClosureRequests(t.Context(), "", 0, 50)
	require.Error(t, err)
	_, err = h.service.ProcessClosureRequest(t.Context(), p.ID, 1)
	require.Error(t, err)
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 1, "rejected", "", "")
	require.Error(t, err)
}

func TestClosureRequestStorageMutationFailuresRollbackProofAndState(t *testing.T) {
	h, p := closureHarness(t)
	require.NoError(t, h.db.Exec("CREATE TRIGGER reject_closure_insert BEFORE INSERT ON close_iam_closure_requests BEGIN SELECT RAISE(ABORT, 'storage rejected'); END").Error)
	_, err := h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.Error(t, err)
	var used int
	require.NoError(t, h.db.Raw("SELECT count FROM proof_uses").Scan(&used).Error)
	require.Zero(t, used)
	require.NoError(t, h.db.Exec("DROP TRIGGER reject_closure_insert").Error)
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 0, "pending", "browser", "verified")
	require.NoError(t, err)
	require.NoError(t, h.db.Exec("CREATE TRIGGER reject_closure_update BEFORE UPDATE ON close_iam_closure_requests BEGIN SELECT RAISE(ABORT, 'storage rejected'); END").Error)
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 1, "canceled", "browser", "")
	require.Error(t, err)
	state, err := h.service.GetClosureRequest(t.Context(), p.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", state.Status)
	require.EqualValues(t, 1, state.Version)
	require.NoError(t, h.db.Exec("DROP TRIGGER reject_closure_update").Error)
	require.NoError(t, h.db.Exec("CREATE TRIGGER suppress_closure_cancel BEFORE UPDATE ON close_iam_closure_requests BEGIN SELECT RAISE(IGNORE); END").Error)
	_, err = h.service.ChangeClosureRequest(t.Context(), p.ID, 1, "canceled", "browser", "")
	require.ErrorIs(t, err, ErrClosureConflict, "an unpersisted cancellation must not be reported as successful")
	state, err = h.service.GetClosureRequest(t.Context(), p.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", state.Status)
	require.EqualValues(t, 1, state.Version)
	require.NoError(t, h.db.Exec("DROP TABLE close_iam_principal_lifecycles").Error)
	_, err = h.service.GetClosureRequest(t.Context(), p.ID)
	require.Error(t, err, "missing lifecycle storage cannot silently become a non-completed request")
}
