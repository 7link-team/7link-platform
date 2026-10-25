package delivery

import (
	"7link.com/platform/internal/delivery/model"
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestQualityEscapeUsesOnlyExactResultInternalAcceptanceReceipts(t *testing.T) {
	h := newResultFixture(t)
	result, e := h.submit()
	require.NoError(t, e)
	var emitted []events.Event
	h.m.service.events = projectPublisher(func(_ context.Context, _ *gorm.DB, event events.Event) (guid.ID, error) {
		emitted = append(emitted, event)
		return 1, nil
	})
	work := h.stored()
	row := &model.ResultVersion{ID: result.ID}
	publish := func() error {
		return h.db.Transaction(func(tx *gorm.DB) error {
			return h.m.publishQualityEscapeOn(h.ctx, tx, work, row, 1000, 13, "Client found a rounding regression")
		})
	}
	require.NoError(t, publish())
	require.Empty(t, emitted)
	now := time.Now().UTC().Truncate(time.Millisecond)
	reviewer := guid.ID(12)
	run := model.VerificationRun{TenantID: 1, ID: 990, ProjectID: 99, WorkItemID: h.work, ResultID: result.ID, CriterionID: *h.input.Evidence[0].CriterionID, Method: "AGENT_JUDGE", Stage: "AGENT_ACCEPTANCE", Round: 1, State: "DONE", Verdict: "PASSED", ReviewerID: &reviewer, ReviewerFamily: "family-a", ReceiptHash: strings.Repeat("a", 64), CreatedAt: now, CompletedAt: &now}
	require.NoError(t, query.BindVerificationRuns(h.db, h.m.service.names).WithContext(h.ctx).Create(&run))
	require.NoError(t, publish())
	require.Len(t, emitted, 1)
	require.Equal(t, "quality.escaped", emitted[0].Type)
	data := emitted[0].Data.(map[string]any)
	require.Equal(t, "CLIENT_ACCEPTANCE", data["stage"])
	require.Equal(t, h.work, data["work_item_id"])
	require.Len(t, data["passed_by"], 1)
}
