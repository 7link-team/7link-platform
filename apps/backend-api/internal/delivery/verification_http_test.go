package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"context"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerificationHTTPRejectsInvalidResultIdentity(t *testing.T) {
	m := &Module{}
	_, err := m.startVerificationHTTP(context.Background(), &verificationStartInput{ResultVersionInput: ResultVersionInput{ResultID: "bad"}})
	require.Error(t, err)
	_, err = m.collectVerificationHTTP(context.Background(), &verificationCollectInput{ResultVersionInput: ResultVersionInput{ResultID: "bad"}})
	require.Error(t, err)
	_, err = m.overrideVerificationHTTP(context.Background(), &verificationOverrideInput{ResultVersionInput: ResultVersionInput{ResultID: "bad"}})
	require.Error(t, err)
}

func TestVerificationHTTPReplayRechecksCurrentAuthority(t *testing.T) {
	h, r, body := verificationFixture(t)
	queued := 0
	h.m.verificationExecutor.QueueOn = func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error) { queued++; return 900, nil }
	h.m.verificationExecutor.ReceiptOn = func(_ context.Context, _ *gorm.DB, tenant, project, work, result, task guid.ID) (VerificationReceipt, error) {
		zero := 0
		return VerificationReceipt{TaskID: task, ResultID: result, ExecutionID: 880, CriterionID: body.CriterionID, ResultVersion: r.Sequence, Method: "COMMAND", Verdict: "PASSED", ReceiptHash: strings.Repeat("b", 64), HeadCommit: strings.Repeat("a", 40), ExitCode: &zero}, nil
	}
	input := &verificationStartInput{ResultVersionInput: h.versionInput(r.ID, "verification-once"), Body: body}
	first, err := h.m.startVerificationHTTP(h.actor(12), input)
	require.NoError(t, err)
	replay, err := h.m.startVerificationHTTP(h.actor(12), input)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	require.Equal(t, 1, queued)
	collect := &verificationCollectInput{ResultVersionInput: h.versionInput(r.ID, "receipt-once")}
	_, err = h.m.collectVerificationHTTP(h.actor(12), collect)
	require.NoError(t, err)
	replay, err = h.m.collectVerificationHTTP(h.actor(12), collect)
	require.NoError(t, err)
	require.Equal(t, "true", replay.Replayed)
	h.m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, err = h.m.startVerificationHTTP(h.actor(12), input)
	require.Error(t, err, "revocation must defeat cached success")
	_, err = h.m.collectVerificationHTTP(h.actor(12), collect)
	require.Error(t, err)
}

func TestVerificationHTTPReplayRechecksReviewerOwnerScopeBeforeProjectLock(t *testing.T) {
	h, r, in := verificationFixture(t)
	now := time.Now().UTC()
	require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(&model.VerificationRun{TenantID: 1, ID: 890, ProjectID: 99, WorkItemID: h.work, ResultID: r.ID, CriterionID: in.CriterionID, Method: "COMMAND", Stage: "AUTOMATED", Round: 1, State: "DONE", Verdict: "PASSED", ActorID: 12, CreatedAt: now, CompletedAt: &now}))
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.verificationExecutor.SourcePrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (guid.ID, error) { return 30, nil }
	h.m.verificationExecutor.QueueOn = func(context.Context, *gorm.DB, VerificationRequest) (guid.ID, error) { return 901, nil }
	locks := []guid.ID{}
	h.m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locks = append(locks, id); return nil }
	scoped := false
	h.m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, principal guid.ID) (authz.DataConstraint, error) {
		if principal == 21 {
			require.GreaterOrEqual(t, len(locks), 4)
			require.Equal(t, []guid.ID{11, 12, 21, 30}, locks[:4])
			if scoped {
				return authz.DataConstraint{OwnerIDs: []string{"999"}}, nil
			}
		}
		return authz.DataConstraint{AllowAll: true}, nil
	}
	input := &verificationStartInput{ResultVersionInput: h.versionInput(r.ID, "agent-review-once"), Body: verificationStartBody{CriterionID: in.CriterionID, Method: "AGENT_JUDGE", Stage: "AGENT_TEST", ReviewerPrincipalID: 21, TimeoutSec: 30}}
	_, err := h.m.startVerificationHTTP(h.actor(12), input)
	require.NoError(t, err)
	scoped = true
	locks = nil
	_, err = h.m.startVerificationHTTP(h.actor(12), input)
	assertWorkStatus(t, err, 403)
}
