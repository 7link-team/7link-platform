package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func acceptedDefectFixture(t *testing.T) (*resultFixture, guid.ID) {
	h := newResultFixture(t)
	require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET type='BUG',severity='NORMAL',priority='MEDIUM',reproducibility='ALWAYS',found_version='v1' WHERE id=?", h.work).Error)
	result, err := h.submit()
	require.NoError(t, err)
	_, err = h.check(result.ID, 12, "PASSED")
	require.NoError(t, err)
	_, err = h.accept(result.ID, 13)
	require.NoError(t, err)
	return h, result.ID
}

func TestDefectStageFailsClosedWithoutActualFacts(t *testing.T) {
	for _, scenario := range []string{"merge", "deploy", "not_verifier", "not_accepted", "stale", "closed_without_verification", "audit", "outbox", "invalid_result", "not_defect", "short_reason", "invalid_stage", "failed_check", "already_verified", "write", "cas"} {
		t.Run(scenario, func(t *testing.T) {
			h, resultID := acceptedDefectFixture(t)
			before := h.stored()
			actor := guid.ID(12)
			body := defectStageBody{ResultID: resultID, Stage: "VERIFIED", Reason: "Verify only actual evidence"}
			switch scenario {
			case "merge":
				body.Stage = "MERGED"
			case "deploy":
				body.Stage = "DEPLOYED"
			case "not_verifier":
				actor = 13
			case "not_accepted":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET state='NEEDS_HUMAN_REVIEW',accepted_at=NULL WHERE id=?", h.work).Error)
			case "stale":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET baseline_stale=1 WHERE id=?", h.work).Error)
			case "closed_without_verification":
				body.Stage = "CLOSED"
			case "audit", "outbox":
				h.fail = scenario
			case "invalid_result":
				body.ResultID = 0
			case "not_defect":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET type='TASK',severity=NULL,priority=NULL,reproducibility=NULL,found_version=NULL WHERE id=?", h.work).Error)
			case "short_reason":
				body.Reason = "bad"
			case "invalid_stage":
				body.Stage = "INVALID"
			case "failed_check":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_result_actions SET verdict='FAILED' WHERE result_id=? AND kind='CHECK'", resultID).Error)
			case "already_verified":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET defect_stage='VERIFIED' WHERE id=?", h.work).Error)
			case "write":
				require.NoError(t, h.db.Exec("CREATE TRIGGER reject_defect BEFORE UPDATE ON d_delivery_work_items BEGIN SELECT RAISE(ABORT,'write failed'); END").Error)
			case "cas":
				require.NoError(t, h.db.Exec("CREATE TRIGGER reject_defect BEFORE UPDATE ON d_delivery_work_items BEGIN SELECT RAISE(IGNORE); END").Error)
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.defectStageOn(h.actor(actor), tx, 1, 99, h.work, actor, 11, before.Version, body)
				return e
			})
			require.Error(t, err)
			after := h.stored()
			if scenario == "already_verified" {
				require.Equal(t, "VERIFIED", *after.DefectStage)
			} else {
				require.Nil(t, after.DefectStage)
			}
			require.Equal(t, before.Version, after.Version)
		})
	}
}
