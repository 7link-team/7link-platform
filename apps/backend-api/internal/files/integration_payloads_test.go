package files

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIntegrationPayloadTracksRawBytesScopeRollbackAndRetention(t *testing.T) {
	h, adapter, _, _ := delegatedArtifactFixture(t)
	p := &IntegrationPayloads{module: adapter.module}
	allowed := true
	require.NoError(t, p.Bind(func(_ context.Context, tx *gorm.DB, tenant, connection, owner guid.ID) error {
		if !allowed || tenant != 90 || connection != 100 || owner != 70 {
			return ErrPinnedForbidden
		}
		_, ok := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, ok)
		return nil
	}))
	require.Error(t, p.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }))
	raw := []byte(`{"signed":"exact bytes","note":"private original"}`)
	prepared, err := p.Stage(h.ctx, 90, 100, 70, raw)
	require.NoError(t, err)
	q := query.Bind(h.db, h.tree.prefix)
	intent, err := q.AccountPreparation.WithContext(h.ctx).Where(q.AccountPreparation.ID.Eq(prepared.object.object.ID)).First()
	require.NoError(t, err)
	require.Equal(t, "ready", intent.State)
	rollback := errors.New("late audit failed")
	var object guid.ID
	err = h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		object, e = p.AttachOn(h.ctx, tx, prepared)
		require.NoError(t, e)
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	n, err := q.AccountReference.WithContext(h.ctx).Where(q.AccountReference.Purpose.Eq("integration_ingress")).Count()
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; object, e = p.AttachOn(h.ctx, tx, prepared); return e }))
	ref, err := q.AccountReference.WithContext(h.ctx).Where(q.AccountReference.ObjectID.Eq(object), q.AccountReference.Purpose.Eq("integration_ingress")).First()
	require.NoError(t, err)
	require.NotNil(t, ref.ExpiresAt)
	require.InDelta(t, (90 * 24 * time.Hour).Milliseconds(), *ref.ExpiresAt-ref.CreatedAt, 2)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		r, e := p.OpenOn(h.ctx, tx, 90, 100, 70, object)
		require.NoError(t, e)
		got, e := io.ReadAll(r)
		require.NoError(t, e)
		require.NoError(t, r.Close())
		require.Equal(t, raw, got)
		return nil
	}))
	for _, scope := range [][3]guid.ID{{91, 100, 70}, {90, 101, 70}, {90, 100, 71}} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.OpenOn(h.ctx, tx, scope[0], scope[1], scope[2], object); return e }))
	}
	allowed = false
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.OpenOn(h.ctx, tx, 90, 100, 70, object); return e }))
	_, err = p.Stage(h.ctx, 90, 100, 70, raw)
	require.Error(t, err)
	allowed = true
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		past := time.Now().Add(-time.Second).UnixMilli()
		_, e := q.AccountReference.WithContext(h.ctx).Where(q.AccountReference.ID.Eq(ref.ID)).Update(q.AccountReference.ExpiresAt, past)
		return e
	}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.OpenOn(h.ctx, tx, 90, 100, 70, object); return e }))
	_, err = p.module.accounts.CollectAccountObjects(h.ctx, h.db, time.Now().Add(91*24*time.Hour), 0, 100)
	require.NoError(t, err)
	_, err = p.OpenOn(h.ctx, h.db, 90, 100, 70, object)
	require.ErrorIs(t, err, ErrAccountTransaction)
}

func TestIntegrationPayloadUnboundAndInvalidNeverStages(t *testing.T) {
	p := &IntegrationPayloads{}
	_, err := p.Stage(t.Context(), 1, 2, 3, []byte(`{}`))
	require.Error(t, err)
	_, err = p.AttachOn(t.Context(), nil, nil)
	require.Error(t, err)
	_, err = p.OpenOn(t.Context(), nil, 1, 2, 3, 4)
	require.Error(t, err)
	h, adapter, _, _ := delegatedArtifactFixture(t)
	p = &IntegrationPayloads{module: adapter.module}
	require.NoError(t, p.Bind(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }))
	for _, raw := range [][]byte{nil, make([]byte, (1<<20)+1)} {
		_, err = p.Stage(h.ctx, 90, 100, 70, raw)
		require.Error(t, err)
	}
}
