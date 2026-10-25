package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAndonPullClearPreservesStateAndRecordsActualRuntimeLimit(t *testing.T) {
	h, link := executionWorkFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CREATE") }))
	originalPolicy := h.m.workPolicy
	h.m.workPolicy = func(ctx context.Context, tx *gorm.DB, t, p guid.ID, code string, a guid.ID) (authz.DataConstraint, error) {
		if code == "execution_stop" && t == 1 && p == 99 && a == 11 {
			return authz.DataConstraint{AllowAll: true}, nil
		}
		return originalPolicy(ctx, tx, t, p, code, a)
	}
	before := h.stored()
	var pulled andonView
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		pulled, e = h.m.changeAndonOn(h.ctx, tx, 1, 99, h.work, 11, 11, andonBody{Version: before.Version, Reason: "Investigate inconsistent source evidence"}, false)
		return e
	}))
	require.Equal(t, before.State, pulled.Work.State)
	require.Equal(t, "UNAVAILABLE", pulled.RunnerPause)
	require.Equal(t, "MANUAL", *h.stored().PauseReason)
	require.True(t, pulled.Work.AndonActive)
	for _, op := range []string{"DISPATCH", "RECOVER", "START"} {
		require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, _, e := h.m.executionWorkOn(h.ctx, tx, link, op); return e }), op)
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, link, "CANCEL") }))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		cleared, e := h.m.changeAndonOn(h.ctx, tx, 1, 99, h.work, 11, 11, andonBody{Version: pulled.Work.Version, Reason: "Human confirms source inconsistency resolved"}, true)
		require.Equal(t, before.State, cleared.Work.State)
		return e
	}))
	require.Nil(t, h.stored().PausedAt)
	require.Equal(t, before.CurrentExecutionID, h.stored().CurrentExecutionID, "clear does not launch or resume a runner")
	q := query.BindWorkTransitions(h.db, h.m.service.names)
	n, e := q.WithContext(h.ctx).Where(q.ReasonCode.In("ANDON_PULLED", "ANDON_CLEARED")).Count()
	require.NoError(t, e)
	require.EqualValues(t, 2, n)
}

func TestAndonTransitiveAndOverlappingHolds(t *testing.T) {
	m, db, ctx, work, upstream := dependencyHarness(t)
	m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
		return governance.AuthorityDecision{Allowed: true, AuthoritySource: "ROLE:owner"}, nil
	}
	// A -> B -> C: only C is pulled, so the guard must walk beyond one edge.
	_, _, _, body := workHTTPHarness(t)
	var middle workDetailView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		middle, e = m.createWorkOn(ctx, tx, 1, 99, 11, body)
		return e
	}))
	q := query.BindDependencies(db, m.service.names)
	require.NoError(t, q.WithContext(ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: work.ID, DependsOnID: middle.ID}))
	require.NoError(t, q.WithContext(ctx).Create(&model.WorkItemDependency{TenantID: 1, ProjectID: 99, WorkItemID: middle.ID, DependsOnID: upstream.ID}))
	change := func(id guid.ID, version int, release bool) error {
		return db.Transaction(func(tx *gorm.DB) error {
			_, e := m.changeAndonOn(ctx, tx, 1, 99, id, 11, 11, andonBody{Version: version, Reason: "Human investigates quality anomaly"}, release)
			return e
		})
	}
	require.NoError(t, change(upstream.ID, upstream.Version, false))
	require.NoError(t, change(work.ID, work.Version, false))
	check := func() error {
		return db.Transaction(func(tx *gorm.DB) error {
			w := query.BindWorkItems(tx, m.service.names)
			r, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
			if e != nil {
				return e
			}
			return m.requireNoAndonOn(ctx, tx, r)
		})
	}
	require.ErrorIs(t, check(), ErrAndonBlocked)
	require.NoError(t, change(work.ID, work.Version+1, true))
	require.ErrorIs(t, check(), ErrAndonBlocked, "clearing a downstream pull cannot clear its upstream pull")
	require.NoError(t, change(upstream.ID, upstream.Version+1, true))
	require.NoError(t, check())
	// A stale computed block must disappear on the next readiness evaluation.
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		w := query.BindWorkItems(tx, m.service.names)
		r, e := w.WithContext(ctx).Where(w.ID.Eq(work.ID)).First()
		if e != nil {
			return e
		}
		r.BlockReasons = `["ANDON_UPSTREAM"]`
		_, blocks, e := m.evaluatePreparationOn(ctx, tx, r, true)
		require.NotContains(t, blocks, "ANDON_UPSTREAM")
		return e
	}))
}

func TestAndonFailuresAreAtomic(t *testing.T) {
	for _, scenario := range []string{"authority", "audit", "outbox", "reason", "version", "other_pause", "denied", "reason_audit"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			in := andonBody{Version: h.stored().Version, Reason: "Investigate a verified quality anomaly"}
			switch scenario {
			case "authority", "audit", "outbox":
				h.fail = scenario
			case "reason":
				in.Reason = "  "
			case "version":
				in.Version++
			case "denied":
				h.deniedAuthority = true
			case "other_pause":
				now := time.Now().UTC()
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id = ?", h.work).Updates(map[string]any{"paused_at": now, "pause_reason": "MANUAL"}).Error)
			case "reason_audit":
				h.m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			before := h.snapshot()
			require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.changeAndonOn(h.ctx, tx, 1, 99, h.work, 11, 11, in, false)
				return e
			}))
			require.Equal(t, before, h.snapshot())
		})
	}
}
