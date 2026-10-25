package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestClarificationReceiptExactReplyCurrentAuthorityAndRollback(t *testing.T) {
	m, db, ctx, _, _ := dependencyHarness(t)
	m.workPolicy = func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string, actor guid.ID) (authz.DataConstraint, error) {
		require.Equal(t, "intake_read", permission)
		return authz.DataConstraint{AllowAll: true}, nil
	}
	reply := ClarificationReplyFact{ProjectID: 99, RequesterID: 11, ReplyVersion: 1}
	m.clarificationReplySource = func(context.Context, *gorm.DB, guid.ID, guid.ID, bool) (ClarificationReplyFact, bool, error) {
		return reply, true, nil
	}
	call := func(version int) (clarificationReceiptView, error) {
		var out clarificationReceiptView
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = m.handleClarificationOn(ctx, tx, 1, 99, 81, 11, version)
			return err
		})
		return out, err
	}
	first, err := call(1)
	require.NoError(t, err)
	require.Positive(t, int64(first.ID))
	require.Equal(t, guid.ID(11), first.HandledBy)
	again, err := call(1)
	require.NoError(t, err)
	require.Equal(t, first, again)
	handled, err := m.clarificationHandledOn(ctx, db, 1, 99, 81, 1)
	require.NoError(t, err)
	require.True(t, handled)
	for _, scope := range []struct {
		tenant, project, id guid.ID
		version             int
	}{{2, 99, 81, 1}, {1, 98, 81, 1}, {1, 99, 82, 1}, {1, 99, 81, 2}} {
		handled, err = m.clarificationHandledOn(ctx, db, scope.tenant, scope.project, scope.id, scope.version)
		require.NoError(t, err)
		require.False(t, handled)
	}
	reply.RequesterID = 12
	_, err = call(1)
	require.ErrorIs(t, err, ErrWorkForbidden)
	reply.RequesterID = 11
	reply.ReplyVersion = 2
	_, err = call(1)
	require.ErrorIs(t, err, ErrWorkVersion)
	original := m.service.audit
	m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
	_, err = call(2)
	require.Error(t, err)
	q := query.BindClarificationReceipts(db, m.service.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	m.service.audit = original
	second, err := call(2)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
}
