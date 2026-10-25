package delivery

import (
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestAndonHTTPReplayRechecksCurrentAuthority(t *testing.T) {
	m, db, ctx, work, _ := readinessHarness(t)
	allowed := true
	m.deliveryAuthority = func(_ context.Context, _ *gorm.DB, r governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		require.Contains(t, []string{"andon_pull", "andon_clear"}, r.Code)
		return governance.AuthorityDecision{Allowed: allowed, AuthoritySource: "ROLE:owner"}, nil
	}
	m.workPolicy = func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	}
	in := &andonInput{EntityID: "99", WorkItemID: work.ID.String(), Key: "andon-pull-once", Body: andonBody{Version: work.Version, Reason: "Investigate conflicting acceptance evidence"}}
	first, e := m.pullAndonHTTP(ctx, in)
	require.NoError(t, e)
	replay, e := m.pullAndonHTTP(ctx, in)
	require.NoError(t, e)
	require.Equal(t, "true", replay.Replayed)
	require.Equal(t, first.Body.Data.Work.Version, replay.Body.Data.Work.Version)
	allowed = false
	_, e = m.pullAndonHTTP(ctx, in)
	require.Error(t, e)
	allowed = true
	in.Key = "andon-clear-once"
	in.Body.Version = first.Body.Data.Work.Version
	in.Body.Reason = "Human reviewed and resolved the source conflict"
	cleared, e := m.clearAndonHTTP(ctx, in)
	require.NoError(t, e)
	require.Nil(t, cleared.Body.Data.Work.PausedAt)
	var n int64
	require.NoError(t, db.Table(m.service.names.WorkTransitions.String()).Where("reason_code IN ?", []string{"ANDON_PULLED", "ANDON_CLEARED"}).Count(&n).Error)
	require.EqualValues(t, 2, n)
}

func TestAndonHTTPBoundaryFailuresNeverCreateHoldReceipts(t *testing.T) {
	h := newResultFixture(t)
	for _, in := range []andonInput{{EntityID: "9999999999999999999", WorkItemID: "1"}, {EntityID: "99", WorkItemID: "9999999999999999999"}, {EntityID: "99", WorkItemID: "999"}} {
		_, e := h.m.pullAndonHTTP(h.ctx, &in)
		require.Error(t, e)
	}
	h.m.runtime = nil
	_, e := h.m.pullAndonHTTP(h.ctx, &andonInput{EntityID: "99", WorkItemID: h.work.String()})
	assertWorkStatus(t, e, 503)
}
