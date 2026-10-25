package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"7link.com/platform/internal/delivery"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVerificationBridgeMapsWrappedDomainErrors(t *testing.T) {
	for _, tc := range []struct{ source, target error }{
		{ErrConflict, delivery.ErrResultState},
		{ErrQueueFull, delivery.ErrResultState},
		{ErrInvalidArgument, delivery.ErrInvalidArgument},
		{ErrForbidden, delivery.ErrWorkForbidden},
		{ErrNotFound, delivery.ErrProjectNotFound},
		{ErrUnavailable, delivery.ErrUnavailable},
		{ErrCapabilityUnavailable, delivery.ErrUnavailable},
	} {
		t.Run(tc.source.Error(), func(t *testing.T) {
			require.ErrorIs(t, verificationBridgeError(tc.source), tc.target)
			require.ErrorIs(t, verificationBridgeError(fmt.Errorf("provider: %w", tc.source)), tc.target)
		})
	}
	require.NoError(t, verificationBridgeError(nil))
	unknown := errors.New("unmapped provider error")
	require.Same(t, unknown, verificationBridgeError(unknown))
}

func TestVerificationAdapterPreservesTransactionAndExactResultBinding(t *testing.T) {
	s := module.NewServices()
	tx := &gorm.DB{}
	var bound delivery.VerificationExecutor
	require.NoError(t, module.Provide[QualityLauncher](s, QualityLauncher{
		PrincipalsOn: func(_ context.Context, db *gorm.DB, tenant, parent guid.ID) ([]guid.ID, error) {
			require.Same(t, tx, db)
			require.Equal(t, []guid.ID{1, 5}, []guid.ID{tenant, parent})
			return []guid.ID{8, 9}, nil
		},
		LaunchOn: func(_ context.Context, db *gorm.DB, in QualityLaunchRequest) (guid.ID, error) {
			require.Same(t, tx, db)
			require.Equal(t, guid.ID(5), in.ParentExecutionID)
			require.Equal(t, guid.ID(12), in.SourceDecisionID)
			require.Equal(t, "snapshot", in.Feedback)
			return 13, nil
		},
	}))
	require.NoError(t, module.Provide[delivery.VerificationExecutorConnector](s, delivery.VerificationExecutorConnector{Bind: func(e delivery.VerificationExecutor) error { bound = e; return nil }}))
	require.NoError(t, module.Provide[VerificationExecutor](s, VerificationExecutor{
		ReviewersOn: func(_ context.Context, db *gorm.DB, tenant, id guid.ID, stage string) ([]guid.ID, error) {
			require.Same(t, tx, db)
			require.Equal(t, []guid.ID{1, 5}, []guid.ID{tenant, id})
			require.Equal(t, "AGENT_TEST", stage)
			return []guid.ID{9}, nil
		},
		SourcePrincipalOn: func(_ context.Context, db *gorm.DB, tenant, id guid.ID) (guid.ID, error) {
			require.Same(t, tx, db)
			return 8, nil
		},
		QueueOn: func(_ context.Context, db *gorm.DB, r VerificationRequest) (guid.ID, error) {
			require.Same(t, tx, db)
			require.Equal(t, guid.ID(6), r.CriterionID)
			require.Equal(t, 2, r.ResultVersion)
			require.Equal(t, guid.ID(9), r.ReviewerPrincipalID)
			require.Equal(t, "AGENT_TEST", r.Stage)
			require.True(t, r.AllowFamilyFallback)
			return 7, nil
		},
		ReceiptOn: func(_ context.Context, db *gorm.DB, tenant, project, work, result, task guid.ID) (VerificationReceipt, error) {
			require.Same(t, tx, db)
			require.Equal(t, guid.ID(4), result)
			require.Equal(t, guid.ID(7), task)
			return VerificationReceipt{TaskID: task, ResultID: result, ExecutionID: 5, CriterionID: 6, ResultVersion: 2, Verdict: "FAILED", ReceiptHash: "immutable", ReviewerID: 9, Model: "judge", Family: "review-family", Stage: "AGENT_TEST"}, nil
		},
	}))
	require.NoError(t, s.Seal())
	a := &verificationAdapter{enabled: true}
	require.NoError(t, a.InjectServices(s))
	participants, err := bound.QualityPrincipalsOn(t.Context(), tx, 1, 5)
	require.NoError(t, err)
	require.Equal(t, []guid.ID{8, 9}, participants)
	child, err := bound.LaunchQualityOn(t.Context(), tx, delivery.QualityLaunchRequest{ParentExecutionID: 5, SourceDecisionID: 12, Feedback: "snapshot"})
	require.NoError(t, err)
	require.Equal(t, guid.ID(13), child)
	reviewers, err := bound.ReviewersOn(t.Context(), tx, 1, 5, "AGENT_TEST")
	require.NoError(t, err)
	require.Equal(t, []guid.ID{9}, reviewers)
	principal, err := bound.SourcePrincipalOn(t.Context(), tx, 1, 5)
	require.NoError(t, err)
	require.Equal(t, guid.ID(8), principal)
	id, err := bound.QueueOn(t.Context(), tx, delivery.VerificationRequest{AllowFamilyFallback: true, CriterionID: 6, ResultVersion: 2, ReviewerPrincipalID: 9, Stage: "AGENT_TEST"})
	require.NoError(t, err)
	require.Equal(t, guid.ID(7), id)
	r, err := bound.ReceiptOn(t.Context(), tx, 1, 2, 3, 4, 7)
	require.NoError(t, err)
	require.Equal(t, "FAILED", r.Verdict)
	require.Equal(t, "immutable", r.ReceiptHash)
	require.Equal(t, 2, r.ResultVersion)
	require.Equal(t, guid.ID(9), r.ReviewerID)
	require.Equal(t, "review-family", r.Family)
	require.Equal(t, "AGENT_TEST", r.Stage)
}
