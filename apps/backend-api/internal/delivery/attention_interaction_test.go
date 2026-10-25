package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAttentionInteractionInboxRechecksAndAtomicRollback(t *testing.T) {
	h, r, at := attentionFixture(t)
	calls := 0
	closed := false
	h.m.attentionInteraction = func(ctx context.Context, tx *gorm.DB, tenant, project, work, interaction guid.ID) error {
		calls++
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, h.work, work)
		require.Equal(t, guid.ID(900), interaction)
		if closed {
			return ErrAttentionClosed
		}
		return nil
	}
	var out attentionView
	create := func(body attentionCreateBody) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			out, e = h.m.createAttentionOn(h.actor(12), tx, 99, h.work, body, at)
			return e
		})
	}
	body := attentionCreateBody{InteractionID: 900, Surface: "INBOX"}
	require.NoError(t, create(body))
	require.Equal(t, 1, calls)
	saved := out
	q := query.BindAttentionSessions(h.db, h.m.service.names)
	row, e := q.WithContext(h.ctx).Where(q.ID.Eq(out.ID)).Take()
	require.NoError(t, e)
	require.Nil(t, row.ClaimID)
	require.Equal(t, guid.ID(900), *row.InteractionID)
	_, e = attentionBeat(h, saved.ID, 12, 1, 15000, false, at.Add(15*time.Second))
	require.NoError(t, e)
	require.Equal(t, 2, calls)
	closed = true
	_, e = attentionBeat(h, saved.ID, 12, 2, 15000, false, at.Add(30*time.Second))
	require.ErrorIs(t, e, ErrAttentionClosed)
	row, e = q.WithContext(h.ctx).Where(q.ID.Eq(saved.ID)).Take()
	require.NoError(t, e)
	require.EqualValues(t, 1, row.LastSequence)
	require.ErrorIs(t, create(body), ErrAttentionClosed)
	closed = false
	h.fail = "outbox"
	require.Error(t, create(body))
	h.fail = ""
	n, e := q.WithContext(h.ctx).Count()
	require.NoError(t, e)
	require.EqualValues(t, 1, n)
	require.ErrorIs(t, create(attentionCreateBody{InteractionID: 900, ClaimID: r.ID, Surface: "INBOX"}), ErrInvalidArgument)
	require.ErrorIs(t, create(attentionCreateBody{InteractionID: 900, Surface: "REVIEW_DESK"}), ErrInvalidArgument)
	h.m.attentionInteraction = nil
	require.ErrorIs(t, create(body), ErrUnavailable)
	require.NoError(t, create(attentionCreateBody{ClaimID: r.ID, Surface: "INBOX"}))
}
func TestAttentionInteractionConnectorRejectsNilAndDuplicate(t *testing.T) {
	services := module.NewServices()
	m := new(Module)
	require.NoError(t, m.provideAttentionInteraction(services))
	require.NoError(t, services.Seal())
	connector, e := module.Resolve[AttentionInteractionConnector](services)
	require.NoError(t, e)
	require.ErrorIs(t, connector.Bind(nil), ErrInvalidArgument)
	f := func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, guid.ID) error { return nil }
	require.NoError(t, connector.Bind(f))
	require.ErrorIs(t, connector.Bind(f), ErrInvalidArgument)
}
