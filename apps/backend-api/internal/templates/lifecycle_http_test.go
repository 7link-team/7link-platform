package templates

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLifecycleHTTPProviderCommandsReplayAndRejectHealthClaims(t *testing.T) {
	m, _, ctx, s := lifecycleHarness(t)
	in := &providerStateInput{ID: s.TemplateVersionID.String(), IdempotencyKey: "provider-enable"}
	in.Body.Reference = "generator:v1"
	in.Body.State = "ENABLED"
	first, e := m.providerStateHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.providerStateHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, first.Body.Data, replay.Body.Data)
	in.Body.State = "UNAVAILABLE"
	_, e = m.providerStateHTTP(ctx, in)
	require.Error(t, e, "HTTP cannot claim a health-check result")
	in.Body.State = "DISABLED"
	in.Body.PriorID = first.Body.Data.ID
	in.IdempotencyKey = "provider-disable"
	_, e = m.providerStateHTTP(ctx, in)
	require.NoError(t, e)
	page, e := m.lifecycleListHTTP(ctx, &lifecycleListInput{ID: in.ID, Limit: 1, Sort: "id"})
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	require.True(t, page.Body.Data.HasMore)
	next, e := m.lifecycleListHTTP(ctx, &lifecycleListInput{ID: in.ID, Limit: 1, Sort: "id", Cursor: page.Body.Data.NextCursor})
	require.NoError(t, e)
	require.Len(t, next.Body.Data.Items, 1)
	require.False(t, next.Body.Data.HasMore)
	m.policy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{}, nil
	}
	_, e = m.providerStateHTTP(ctx, in)
	require.Error(t, e, "idempotent replay must reauthorize")
	_, e = m.lifecycleListHTTP(ctx, &lifecycleListInput{ID: in.ID, Limit: 50, Sort: "id"})
	require.Error(t, e)
}

func TestLifecycleHTTPCurrentProjectScopeAndMalformedReads(t *testing.T) {
	m, db, ctx, s := lifecycleHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.instantiateOn(ctx, tx, s.TemplateVersionID, 99, 11); return e }))
	s.ProjectID = 99
	var row LifecycleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		row, e = m.recordToolOn(ctx, tx, ToolObservation{Source: s, EligibleMachines: 0})
		return e
	}))
	input := &lifecycleListInput{ID: s.TemplateVersionID.String(), Limit: 1, Sort: "id"}
	page, e := m.lifecycleListHTTP(ctx, input)
	require.NoError(t, e)
	require.Len(t, page.Body.Data.Items, 1)
	m.scope = delivery.ProjectScope{}
	_, e = m.lifecycleListHTTP(ctx, input)
	assertStatus(t, e, 503)
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }}
	_, e = m.lifecycleListHTTP(ctx, input)
	assertStatus(t, e, 403)
	m.scope = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }}
	for _, in := range []lifecycleListInput{{ID: "bad", Limit: 1, Sort: "id"}, {ID: input.ID, Limit: 1, Sort: "id", Cursor: "bad"}} {
		_, e := m.lifecycleListHTTP(ctx, &in)
		assertStatus(t, e, 422)
	}
	require.NoError(t, db.Table(m.names.LifecycleReceipts.String()).Where("id=?", row.ID).Update("payload", "invalid").Error)
	_, e = m.lifecycleListHTTP(ctx, input)
	assertStatus(t, e, 500)
}
