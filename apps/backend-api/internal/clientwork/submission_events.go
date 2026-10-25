package clientwork

import (
	"context"
	"encoding/json"

	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

func (m *Module) publishSubmissionDeliveriesOn(ctx context.Context, tx *gorm.DB, project, id guid.ID) error {
	tenant := authn.TenantIDFromContext(ctx)
	q := query.BindDeliveryPackage(tx, m.names)
	r, err := q.WithContext(ctx).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.ID.Eq(id)).Take()
	if err != nil {
		return err
	}
	var manifest packageManifest
	if err := json.Unmarshal([]byte(r.Manifest), &manifest); err != nil {
		return err
	}
	if len(manifest.Refs) == 0 {
		return nil
	}
	if m.packageSources.ReadOn == nil {
		return ErrUnavailable
	}
	facts, err := m.packageSources.ReadOn(ctx, tx, tenant, project, manifest.Refs)
	if err != nil {
		return err
	}
	if len(facts) != len(manifest.Refs) {
		return ErrUnavailable
	}
	seen := map[guid.ID]bool{}
	for i, fact := range facts {
		if fact.WorkItemID != manifest.Refs[i].WorkItemID {
			return ErrConflict
		}
		if fact.IntakeID <= 0 || seen[fact.IntakeID] {
			continue
		}
		if m.submissionEvents.DeliveryPublishedOn == nil {
			return ErrUnavailable
		}
		seen[fact.IntakeID] = true
		if err := m.submissionEvents.DeliveryPublishedOn(ctx, tx, tenant, project, fact.IntakeID); err != nil {
			return err
		}
	}
	return nil
}
