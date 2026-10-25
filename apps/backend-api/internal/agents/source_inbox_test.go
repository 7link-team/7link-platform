package agents

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSourceInboxBindingPersistsReferencesAndSharesSourceRollback(t *testing.T) {
	m, db, _ := agentHarness(t)
	s := module.NewServices()
	var appendSource func(context.Context, *gorm.DB, delivery.AgentInboxReference) error
	require.NoError(t, module.Provide(s, delivery.AgentInboxConnector{Bind: func(fn func(context.Context, *gorm.DB, delivery.AgentInboxReference) error) error {
		appendSource = fn
		return nil
	}}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.injectSourceInbox(s))
	require.NotNil(t, appendSource)
	ctx := agentContext(t, 2)
	ref := delivery.AgentInboxReference{TenantID: 1, PrincipalID: 7, SubjectID: 5, SourceEventID: "source-assignment-1", Kind: "DISPATCH", StreamKey: "work_item:5", StreamSeq: 12}
	rollback := errors.New("source command failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, appendSource(ctx, tx, ref))
		return rollback
	}), rollback)
	q := query.BindInboxEvent(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := appendSource(ctx, tx, ref); err != nil {
			return err
		}
		return appendSource(ctx, tx, ref)
	}))
	rows, err := q.WithContext(ctx).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, ref.Kind, rows[0].Kind)
	require.Equal(t, ref.SubjectID, rows[0].SubjectID)
	require.Equal(t, ref.SourceEventID, rows[0].SourceEventID)
	require.Equal(t, ref.StreamKey, rows[0].StreamKey)
	require.Equal(t, ref.StreamSeq, rows[0].StreamSeq)
	ref.PrincipalID = 8
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return appendSource(ctx, tx, ref) }), ErrNotFound)
}

func TestSourceInboxBindingRejectsInvalidConnector(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, module.Provide(s, delivery.AgentInboxConnector{}))
	require.ErrorIs(t, m.injectSourceInbox(s), module.ErrServicesProviding)
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.injectSourceInbox(s), ErrUnavailable)
	want := errors.New("source binding failed")
	s = module.NewServices()
	require.NoError(t, module.Provide(s, delivery.AgentInboxConnector{Bind: func(func(context.Context, *gorm.DB, delivery.AgentInboxReference) error) error { return want }}))
	require.NoError(t, s.Seal())
	require.ErrorIs(t, m.injectSourceInbox(s), want)
}
