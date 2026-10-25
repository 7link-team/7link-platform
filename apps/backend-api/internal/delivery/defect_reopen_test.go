package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTesterAgentReopenRequiresCurrentRoleAndCursorWithoutHumanEscape(t *testing.T) {
	h, _ := acceptedDefectFixture(t)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE"}, true, nil
	}
	h.m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 50, nil }
	granted := true
	h.m.settingsMembers.ListOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) ([]core.EntityRoleBinding, error) {
		if !granted {
			return nil, nil
		}
		return []core.EntityRoleBinding{{PrincipalID: 14, RoleID: 50, Effect: "allow"}}, nil
	}
	before := h.stored()
	reopen := func(seen *int64) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.reopenDefectOn(h.actor(14), tx, 1, 99, h.work, 14, 11, before.Version, defectReopenBody{Reason: "Tester Agent reproduced the regression", SeenSeq: seen})
			return e
		})
	}
	require.ErrorIs(t, reopen(nil), ErrWorkSeenSeqRequired)
	var interrupted *WorkWriteInterrupted
	require.ErrorAs(t, reopen(new(int64(0))), &interrupted)
	require.NoError(t, reopen(new(before.ActivitySeq)))
	require.Equal(t, before, h.stored())
	in := &defectReopenInput{EntityID: "99", WorkItemID: h.work.String(), IfMatch: fmtETag(before.Version), IdempotencyKey: "tester-regression-reopen", Body: defectReopenBody{Reason: "Tester Agent reproduced the regression", SeenSeq: new(before.ActivitySeq)}}
	first, err := h.m.defectReopenHTTP(h.actor(14), in)
	require.NoError(t, err)
	again, err := h.m.defectReopenHTTP(h.actor(14), in)
	require.NoError(t, err)
	require.Equal(t, first.Body.Data.ID, again.Body.Data.ID)
	require.Equal(t, "true", again.Replayed)
	granted = false
	require.ErrorIs(t, reopen(new(before.ActivitySeq)), ErrWorkForbidden)
	_, err = h.m.defectReopenHTTP(h.actor(14), in)
	assertWorkStatus(t, err, 403)
	q := query.BindReportQualityReceipts(h.db, h.m.service.names)
	n, err := q.WithContext(h.ctx).Where(q.Kind.Eq("ESCAPE_HUMAN")).Count()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestDefectReopenCreatesNewBugPreservesSourceAndDeduplicatesEscape(t *testing.T) {
	h, _ := acceptedDefectFixture(t)
	before := h.stored()
	reopen := func() (WorkView, error) {
		var v WorkView
		err := h.db.Transaction(func(tx *gorm.DB) error {
			var e error
			v, e = h.m.reopenDefectOn(h.actor(12), tx, 1, 99, h.work, 12, 11, before.Version, defectReopenBody{Reason: "Regression reproduced after the accepted fix"})
			return e
		})
		return v, err
	}
	created, err := reopen()
	require.NoError(t, err)
	require.NotEqual(t, h.work, created.ID)
	require.Equal(t, "BUG", created.Type)
	require.Equal(t, h.work, *created.ReopenedFromWorkItemID)
	require.Nil(t, created.DefectStage)
	require.Equal(t, "HUMAN_REVIEW", *created.EscapedFromStage)
	after := h.stored()
	require.Equal(t, before, after, "reopening must not rewrite the original terminal aggregate")
	_, err = reopen()
	require.NoError(t, err)
	q := query.BindReportQualityReceipts(h.db, h.m.service.names)
	n, err := q.WithContext(h.ctx).Where(q.Kind.Eq("ESCAPE_HUMAN"), q.WorkItemID.Eq(h.work)).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}
func TestDefectReopenSourceAndAtomicGuards(t *testing.T) {
	for _, scenario := range []string{"processing", "cas", "audit", "outbox", "not_defect"} {
		t.Run(scenario, func(t *testing.T) {
			h, _ := acceptedDefectFixture(t)
			version := h.stored().Version
			switch scenario {
			case "processing":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "REWORK").Error)
			case "cas":
				version--
			case "audit", "outbox":
				h.fail = scenario
			case "not_defect":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"type": "TASK", "severity": nil, "priority": nil, "reproducibility": nil, "found_version": nil}).Error)
			}
			q := query.BindWorkItems(h.db, h.m.service.names)
			before, err := q.WithContext(h.ctx).Count()
			require.NoError(t, err)
			err = h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.reopenDefectOn(h.actor(12), tx, 1, 99, h.work, 12, 11, version, defectReopenBody{Reason: "Regression reproduced after the accepted fix"})
				return e
			})
			require.Error(t, err)
			after, e := q.WithContext(h.ctx).Count()
			require.NoError(t, e)
			require.Equal(t, before, after)
		})
	}
}

func TestCancelledDefectReopenIsHumanOnlyAndHasNoEscape(t *testing.T) {
	h, _ := acceptedDefectFixture(t)
	h.m.members = memberCheck(func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return true, nil })
	h.m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 114, TenantID: 1, PrincipalID: 14, State: "ACTIVE"}, true, nil
	}
	require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("state", "CANCELLED").Error) //nolint:misspell // Preserve the published domain or serialized contract spelling.
	before := h.stored()
	reopen := func(actor guid.ID) error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			_, e := h.m.reopenDefectOn(h.actor(actor), tx, 1, 99, h.work, actor, 11, before.Version, defectReopenBody{Reason: "The canceled defect is now reproducible"})
			return e
		})
	}
	require.ErrorIs(t, reopen(14), ErrWorkForbidden)
	require.NoError(t, reopen(12))
	require.Equal(t, before, h.stored())
	q := query.BindReportQualityReceipts(h.db, h.m.service.names)
	n, e := q.WithContext(h.ctx).Where(q.Kind.Eq("ESCAPE_HUMAN")).Count()
	require.NoError(t, e)
	require.Zero(t, n)
}
