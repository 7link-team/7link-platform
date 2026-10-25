package intake

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/intake/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestOwnSubmissionChangeWhitelistUsesOnlyCanonicalSafeFacts(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"submitted_by_principal_id": 2, "kind": "CHANGE", "change_of_intake_id": 9, "target_requirement_id": 20, "title": "Public change", "latest_card_revision": 0, "decline_reason": "PRIVATE rationale", "client_message": "Public reply"}).Error)
	for status, label := range map[string]string{"RECEIVED": "ASSESSING", "TRIAGED": "ASSESSING", "CLARIFYING": "ASSESSING", "CONVERTED": "ACCEPTED", "DECLINED": "NOT_ACCEPTED", "MERGED": "MERGED"} {
		t.Run(status, func(t *testing.T) {
			// Status mapping is independently verified without bypassing DB terminal
			// state constraints to synthesize incomplete domain transitions.
			row := model.Record{Title: "Public change", RefNo: 7, Status: status}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				view, err := m.submissionChangeOn(ctx, tx, &row)
				require.NoError(t, err)
				require.Equal(t, label, view.Status)
				return nil
			}))
		})
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, err := m.submissionChangeOn(ctx, tx, &model.Record{Status: "CLIENT_APPROVAL"})
		require.NoError(t, err)
		require.Equal(t, "AWAITING_YOUR_APPROVAL", view.Status)
		return nil
	}))
	proposal := `{"tolerance_delta":{"schedule_delta_days":4,"cost_delta_micros":999},"change":{"stale_work_ids":[998],"criteria":{"add":[{"text":"New plain criterion"}],"supersede":[{"criterion_id":997,"text":"Changed plain criterion"}],"retire":[996]}},"internal_cost_delta_micros":999,"affected_work_items":[995],"tolerance_check":{"PRIVATE":true},"agent":"PRIVATE"}`
	card := model.TriageCard{ID: 701, TenantID: 7, IntakeID: 10, Revision: 1, ProposalHash: strings.Repeat("a", 64), ProposalJSON: proposal, ImpactJSON: `{"PRIVATE":"internal impact"}`, ActorID: 2, CreatedAt: time.Now().UTC()}
	require.NoError(t, db.Table(m.names.TriageCards.String()).Create(&card).Error)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("latest_card_revision", 1).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		view, err := m.ownSubmissionOn(ctx, tx, 7, 10)
		require.NoError(t, err)
		require.NotNil(t, view.ChangeView)
		require.Equal(t, 4, *view.ChangeView.ScheduleDeltaDays)
		require.Len(t, view.ChangeView.CriteriaDiff, 2)
		require.Equal(t, "New plain criterion", view.ChangeView.CriteriaDiff[0].Text)
		raw, err := json.Marshal(view.ChangeView)
		require.NoError(t, err)
		for _, forbidden := range []string{"PRIVATE", "internal_cost", "affected_work", "tolerance_check", "agent", "stale_work", "criterion_id", "999", "998", "997", "996", "995", "proposal", "impact"} {
			require.NotContains(t, string(raw), forbidden)
		}
		return nil
	}))
	require.NoError(t, db.Table(m.names.TriageCards.String()).Where("id=701").Update("proposal_json", "broken").Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ownSubmissionOn(ctx, tx, 7, 10); return err }))
	require.NoError(t, db.Table(m.names.TriageCards.String()).Where("id=701").Delete(&model.TriageCard{}).Error)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.ownSubmissionOn(ctx, tx, 7, 10); return err }))
}
