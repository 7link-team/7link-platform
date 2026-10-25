package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProgressResumeRequiresCurrentExecutionOwnerAndRetainsOtherBlocks(t *testing.T) {
	for _, fault := range []string{"ok", "missing", "source", "version", "corrupt", "fence"} {
		t.Run(fault, func(t *testing.T) {
			h := newResultFixture(t)
			at := time.Now().UTC()
			require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Updates(map[string]any{"paused_at": at, "pause_reason": "AUTONOMY_SPINNING", "block_reasons": "[\"AUTONOMY_SPINNING\",\"MANUAL\"]", "current_execution_id": 800}).Error)
			injected := errors.New("resume source unavailable")
			calls := 0
			want := error(nil)
			h.m.executionHolds = ExecutionHolds{PrincipalsOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
				if fault == "fence" {
					return nil, injected
				}
				return nil, nil
			}, ChangeOn: func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID, resume bool) ([]ExecutionHoldFact, error) {
				calls++
				require.True(t, resume)
				require.Equal(t, h.work, work)
				if fault == "source" {
					return nil, injected
				}
				return nil, nil
			}}
			before := h.stored()
			expected := before.Version
			switch fault {
			case "missing":
				h.m.executionHolds.ChangeOn = nil
				want = ErrUnavailable
			case "source", "fence":
				want = injected
			case "version":
				expected++
				want = ErrWorkVersion
			case "corrupt":
				require.NoError(t, h.db.Table(h.m.service.names.WorkItems.String()).Where("id=?", h.work).Update("block_reasons", "[{}]").Error)
				want = ErrUnavailable
			}
			e := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.resumeManualWorkOn(h.actor(13), tx, 1, 99, h.work, 13, 11, expected, workResumeBody{Reason: "Human authorizes current runtime resume"})
				return e
			})
			require.ErrorIs(t, e, want)
			after := h.stored()
			if fault == "ok" {
				require.Nil(t, after.PausedAt)
				require.Equal(t, "[\"MANUAL\"]", after.BlockReasons)
				require.Equal(t, 1, calls)
			} else {
				require.NotNil(t, after.PausedAt)
				require.Equal(t, before.Version, after.Version)
			}
		})
	}
}

func TestScopeRequestChangesGuardsAndRollback(t *testing.T) {
	for _, scenario := range []string{"not_stale", "accepted", "self", "feedback", "audit", "outbox", "feedback_invalid", "action_id", "feedback_id", "feedback_write"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			result, err := h.submit()
			require.NoError(t, err)
			require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET baseline_stale=1 WHERE id=?", h.work).Error)
			actor := h.actor(12)
			actorID := result.ActorID + 1
			body := scopeReworkBody{FailureCategory: "SCOPE_CHANGE", Reason: "Review revised business scope", Feedback: []resultFeedbackBody{{Kind: "FEEDBACK", Category: "SCOPE_CHANGE", Detail: "Update the scope coverage"}}}
			switch scenario {
			case "not_stale":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET baseline_stale=0 WHERE id=?", h.work).Error)
			case "accepted":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET state='ACCEPTED' WHERE id=?", h.work).Error)
			case "self":
				actor, actorID = h.actor(11), 11
			case "feedback":
				body.Feedback = nil
			case "audit", "outbox":
				h.fail = scenario
			case "feedback_invalid":
				body.Feedback[0].Kind = "COMMENT"
			case "action_id", "feedback_id":
				failAt := 1
				if scenario == "feedback_id" {
					failAt = 2
				}
				original, count := h.m.ids, 0
				h.m.ids = draftIDSource(func() (int64, error) {
					count++
					if count == failAt {
						return 0, errors.New("id unavailable")
					}
					return original.Next()
				})
			case "feedback_write":
				require.NoError(t, h.db.Exec("CREATE TRIGGER reject_scope_feedback BEFORE INSERT ON d_delivery_result_feedback BEGIN SELECT RAISE(ABORT,'write failed'); END").Error)
			}
			before := h.stored()
			err = h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.scopeReworkOn(actor, tx, 1, 99, h.work, result.ID, actorID, 11, before.Version, body)
				return e
			})
			require.Error(t, err)
			require.Equal(t, before.State, h.stored().State)
			require.Equal(t, before.Version, h.stored().Version)
			q := query.BindResultActions(h.db, h.m.service.names)
			count, err := q.WithContext(h.ctx).Where(q.ResultID.Eq(result.ID)).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

func TestManualResumeRejectsOtherPausesAndRollsBackFailures(t *testing.T) {
	for _, scenario := range []string{"not_paused", "other_pause", "audit", "outbox", "reason", "authority", "authority_missing", "authority_denied", "authority_delegated", "reason_audit"} {
		t.Run(scenario, func(t *testing.T) {
			h := newResultFixture(t)
			now := time.Now().UTC()
			require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET state='REWORK',paused_at=?,pause_reason='AUTONOMY_REWORK_LIMIT',require_human_decision=1 WHERE id=?", now, h.work).Error)
			body := workResumeBody{Reason: "Human approves bounded rework"}
			switch scenario {
			case "not_paused":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET paused_at=NULL,pause_reason=NULL WHERE id=?", h.work).Error)
			case "other_pause":
				require.NoError(t, h.db.Exec("UPDATE d_delivery_work_items SET pause_reason='MANUAL' WHERE id=?", h.work).Error)
			case "audit", "outbox":
				h.fail = scenario
			case "reason":
				body.Reason = "bad"
			case "authority":
				h.fail = "authority"
			case "authority_missing":
				h.m.deliveryAuthority = nil
			case "authority_denied":
				h.deniedAuthority = true
			case "authority_delegated":
				h.m.deliveryAuthority = func(context.Context, *gorm.DB, governance.AuthorityRequest) (governance.AuthorityDecision, error) {
					return governance.AuthorityDecision{Allowed: true, AuthoritySource: "DELEGATION:1", DelegationID: 1}, nil
				}
			case "reason_audit":
				original := h.m.service.audit
				h.m.service.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
					if event.EventType == "delivery.work_resume_authorized" {
						return errors.New("reason audit unavailable")
					}
					return original(ctx, tx, event)
				}
			}
			before := h.stored()
			err := h.db.Transaction(func(tx *gorm.DB) error {
				_, e := h.m.resumeManualWorkOn(h.actor(13), tx, 1, 99, h.work, 13, 11, before.Version, body)
				return e
			})
			require.Error(t, err)
			require.Equal(t, before.Version, h.stored().Version)
			require.Equal(t, before.PauseReason, h.stored().PauseReason)
		})
	}
}
