package fleet

import (
	"context"

	"7link.com/platform/internal/fleet/query"
	"7link.com/platform/internal/iam/audit"
	"gorm.io/gorm"
)

// bindAudit freezes target provenance at the Fleet producer, inside the
// same transaction as registration/spawn. Readers never infer missing old
// origin facts from current agent state.
func (m *Module) bindAudit() {
	appendEvent := m.audit
	m.audit = func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		if event.EventType == "fleet.agent_registered" || event.EventType == "fleet.agent_spawned" {
			q := query.BindAgents(tx, m.names)
			row, err := q.WithContext(ctx).Where(q.TenantID.Eq(event.TenantID), q.ID.Eq(event.TargetID)).First()
			if err != nil {
				return err
			}
			if event.Detail == nil {
				event.Detail = map[string]any{}
			}
			event.Detail["agent_id"], event.Detail["origin"], event.Detail["target_label"] = row.ID.String(), row.Origin, row.Name
			if row.SpawnGrantID != nil {
				event.Detail["spawn_grant_id"] = row.SpawnGrantID.String()
			}
			if row.ParentAgentID != nil {
				event.Detail["supervisor_agent_id"] = row.ParentAgentID.String()
			}
		}
		return appendEvent(ctx, tx, event)
	}
}
