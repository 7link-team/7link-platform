package knowledge

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestExecutionKnowledgeViewRechecksSourceDecisionAndHidesSystemSentinel(t *testing.T) {
	m, db, _ := injectionHarness(t)
	s := module.NewServices()
	require.NoError(t, m.provideExecutionView(s))
	s.Seal()
	c, err := module.Resolve[ExecutionKnowledgeViewConnector](s)
	require.NoError(t, err)
	require.NoError(t, c.BindExecution(func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionKnowledgeSnapshot, error) {
		return ExecutionKnowledgeSnapshot{ProjectID: 99, WorkItemID: 100, Feedback: []FeedbackContextItem{{Label: "[F1]", SourceDecisionID: 900, Category: "FUNCTIONAL_DEFECT", Expected: "PASSED"}}}, nil
	}))
	_, err = m.executionKnowledgeViewOn(knowledgeContext(t, 2), db, 1, 200)
	require.ErrorIs(t, err, ErrUnavailable)
	at := time.Date(2026, 10, 22, 1, 2, 3, 0, time.UTC)
	require.NoError(t, c.BindDecision(func(_ context.Context, _ *gorm.DB, tenant, project, work, decision guid.ID) (FeedbackDecisionMetadata, error) {
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, guid.ID(100), work)
		require.Equal(t, guid.ID(900), decision)
		return FeedbackDecisionMetadata{CreatedAt: at}, nil
	}))
	out, err := m.executionKnowledgeViewOn(knowledgeContext(t, 2), db, 1, 200)
	require.NoError(t, err)
	require.Nil(t, out.Feedback[0].ActorID)
	require.Equal(t, "SYSTEM", out.Feedback[0].ActorKind)
	require.Equal(t, &at, out.Feedback[0].DecidedAt)
	m.view.decision = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (FeedbackDecisionMetadata, error) {
		return FeedbackDecisionMetadata{}, ErrForbidden
	}
	_, err = m.executionKnowledgeViewOn(knowledgeContext(t, 2), db, 1, 200)
	require.ErrorIs(t, err, ErrForbidden)
}
func TestUsedKnowledgeViewFallsBackToAuthorizedFrozenExecutionWithoutScores(t *testing.T) {
	m, db, _ := injectionHarness(t)
	m.view.read = func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionKnowledgeSnapshot, error) {
		return ExecutionKnowledgeSnapshot{ProjectID: 99, WorkItemID: 100, Budget: 12000, Feedback: []FeedbackContextItem{{Label: "[F1]", SourceDecisionID: 900}}}, nil
	}
	m.view.decision = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (FeedbackDecisionMetadata, error) {
		return FeedbackDecisionMetadata{CreatedAt: time.Now()}, nil
	}
	var out UsedKnowledgeView
	err := db.Transaction(func(tx *gorm.DB) error {
		var e error
		out, e = m.usedKnowledgeOn(knowledgeContext(t, 2), tx, 1, 200)
		return e
	})
	require.NoError(t, err)
	require.True(t, out.Available)
	require.False(t, out.ScoresAvailable)
	require.Equal(t, "SNAPSHOT_ONLY", out.Retrieval.Mode)
	require.Len(t, out.Feedback, 1)
	require.EqualValues(t, 12000, out.ExecutionBudget)
}

func TestExecutionViewConnectorRejectsInvalidAndCompetingReadersAndPropagatesErrors(t *testing.T) {
	m, db, _ := knowledgeHarness(t)
	services := module.NewServices()
	require.NoError(t, m.provideExecutionView(services))
	require.NoError(t, services.Seal())
	connector, err := module.Resolve[ExecutionKnowledgeViewConnector](services)
	require.NoError(t, err)
	require.ErrorIs(t, connector.BindExecution(nil), ErrInvalid)
	require.ErrorIs(t, connector.BindDecision(nil), ErrInvalid)
	reader := func(context.Context, *gorm.DB, guid.ID, guid.ID) (ExecutionKnowledgeSnapshot, error) {
		return ExecutionKnowledgeSnapshot{}, ErrUnavailable
	}
	decision := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) (FeedbackDecisionMetadata, error) {
		return FeedbackDecisionMetadata{}, ErrNotFound
	}
	require.NoError(t, connector.BindExecution(reader))
	require.NoError(t, connector.BindDecision(decision))
	require.ErrorIs(t, connector.BindExecution(reader), ErrConflict)
	require.ErrorIs(t, connector.BindDecision(decision), ErrConflict)
	_, err = m.executionKnowledgeViewOn(knowledgeContext(t, 2), db, 1, 200)
	require.ErrorIs(t, err, ErrUnavailable)
}
