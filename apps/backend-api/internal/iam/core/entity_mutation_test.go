package core

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestEntityStateFenceRestrictsCurrentIAMAndSharesTransaction(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	m := NewModule(h.runtime, enabledModuleConfig())
	*m.checker = *h.authorizer
	s := module.NewServices()
	require.NoError(t, m.provideEntityConstraint(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[EntityStateFenceConnector](s)
	require.NoError(t, err)
	require.ErrorIs(t, c.Bind(nil), ErrInvalidArgument)
	blocked := errors.New("owner: archived")
	var calls int
	require.NoError(t, c.Bind(func(_ context.Context, tx *gorm.DB, tenant, id guid.ID, permission string) error {
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, entity.ID, id)
		require.Equal(t, "user_view", permission)
		calls++
		return blocked
	}))
	require.ErrorIs(t, c.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }), ErrInvalidArgument)
	evaluate, err := module.Resolve[EntityConstraintOn](s)
	require.NoError(t, err)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, e := evaluate(t.Context(), tx, 1, entity.ID, "user_view", 11)
		return e
	})
	require.ErrorIs(t, err, blocked)
	require.Equal(t, 1, calls)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		_, e := evaluate(t.Context(), tx, 1, entity.ID, "not_registered", 11)
		return e
	})
	require.Error(t, err)
	require.Equal(t, 1, calls, "owner state cannot turn denied IAM into a grant")
}

func TestMarkedMutationFencesRelationshipPolicyWithoutGrantingIAM(t *testing.T) {
	h, entity, _ := transactionAuthorizationFixture(t)
	h.seedAdditionalMember(t, 1, 12, MemberActive)
	m := NewModule(h.runtime, enabledModuleConfig())
	*m.checker = *h.authorizer
	s := module.NewServices()
	require.NoError(t, m.provideEntityConstraint(s))
	require.NoError(t, s.Seal())
	connector, err := module.Resolve[EntityStateFenceConnector](s)
	require.NoError(t, err)
	blocked := errors.New("owner: archived")
	fenceErr := blocked
	calls := 0
	require.NoError(t, connector.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error {
		calls++
		return fenceErr
	}))
	evaluate, err := module.Resolve[EntityConstraintOn](s)
	require.NoError(t, err)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		c, e := evaluate(t.Context(), tx, 1, entity.ID, "user_view", 12)
		require.NoError(t, e)
		require.False(t, c.AllowsEntityRows(entity.ID))
		require.Zero(t, calls)
		ctx := WithEntityMutation(t.Context())
		_, e = evaluate(ctx, tx, 1, entity.ID, "user_view", 12)
		require.ErrorIs(t, e, blocked)
		require.Equal(t, 1, calls)
		fenceErr = nil
		c, e = evaluate(ctx, tx, 1, entity.ID, "user_view", 12)
		require.NoError(t, e)
		require.False(t, c.AllowsEntityRows(entity.ID), "owner fence cannot grant IAM")
		require.Equal(t, 2, calls)
		_, e = evaluate(ctx, tx, 1, entity.ID, "not_registered", 12)
		require.Error(t, e)
		require.Equal(t, 2, calls, "checker errors precede owner fence")
		return nil
	}))
}
