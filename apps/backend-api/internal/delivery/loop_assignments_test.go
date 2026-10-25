package delivery

import (
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestLoopAssignmentsUseHistoricalScopedAssignee(t *testing.T) {
	m, db, ctx, work := assignmentHarness(t, true)
	claim := assignmentClaim(t, m, db, ctx, work, false)
	id := claim.Assignment.ID
	require.NoError(t, db.Table(m.service.names.Assignments.String()).Where("id=?", id).Updates(map[string]any{"assignee_kind": "AGENT", "assignee_principal_id": 70, "agent_id": 70}).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		facts, e := m.loopAssignmentAgentsOn(ctx, tx, 1, 99, []guid.ID{id, id, 999})
		require.NoError(t, e)
		require.Equal(t, map[guid.ID]guid.ID{id: 70}, facts)
		other, e := m.loopAssignmentAgentsOn(ctx, tx, 2, 99, []guid.ID{id})
		require.Empty(t, other)
		return e
	}))
	_, e := m.loopAssignmentAgentsOn(ctx, db, 1, 99, []guid.ID{id})
	require.ErrorIs(t, e, ErrInvalidArgument)
}
