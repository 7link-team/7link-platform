package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestExecutionProgressHoldPersistsAndAuditFailureRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "audit_failure"}[fail], func(t *testing.T) {
			h, l := executionWorkFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return h.m.authorizeExecutionWorkOn(h.ctx, tx, l, "CREATE") }))
			if fail {
				h.m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			}
			e := h.db.Transaction(func(tx *gorm.DB) error { return h.m.progressStopOn(h.ctx, tx, l, "AUTONOMY_NO_PROGRESS") })
			q := query.BindWorkItems(h.db, h.m.service.names)
			work, readErr := q.WithContext(h.ctx).Where(q.ID.Eq(h.work)).Take()
			require.NoError(t, readErr)
			if fail {
				require.Error(t, e)
				require.Nil(t, work.PausedAt)
			} else {
				require.NoError(t, e)
				require.NotNil(t, work.PausedAt)
				require.Equal(t, "ESCALATION_RULE", *work.PauseReason)
				require.Equal(t, "MANDATORY_HUMAN_GATE", *work.EscalationReason)
				require.True(t, work.RequireHumanDecision)
			}
		})
	}
}
