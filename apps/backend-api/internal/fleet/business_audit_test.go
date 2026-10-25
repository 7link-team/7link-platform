package fleet

import (
	"context"
	"testing"

	"7link.com/platform/internal/iam/audit"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSpawnAuditFreezesActualOriginGrantAndTargetLabel(t *testing.T) {
	m, db, ctx, _, in := agentSpawnHarness(t)
	appendEvent := m.audit
	var recorded audit.DomainEvent
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		recorded = event
		return appendEvent(ctx, tx, event)
	}
	m.bindAudit()
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.SpawnAgentOn(ctx, tx, 7, in); return err }))
	require.Equal(t, "fleet.agent_spawned", recorded.EventType)
	require.Equal(t, "AGENT_SPAWNED", recorded.Detail["origin"])
	require.Equal(t, "901", recorded.Detail["spawn_grant_id"])
	require.Equal(t, "900", recorded.Detail["supervisor_agent_id"])
	require.Equal(t, recorded.TargetID.String(), recorded.Detail["agent_id"])
	require.Equal(t, in.Registration.Name, recorded.Detail["target_label"])
}
