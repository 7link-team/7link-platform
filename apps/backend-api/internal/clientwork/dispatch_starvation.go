package clientwork

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"context"
	"gorm.io/gorm"
	"time"
)

const dispatchStarvationJob = "clientwork.dispatch_starvation"

func (m *Module) scanDispatchStarvation(ctx context.Context, tenant guid.ID, at time.Time) error {
	if tenant <= 0 || at.IsZero() {
		return jobs.Permanent(ErrInvalid)
	}
	db := m.writer()
	if db == nil || m.dispatchStarvation.ListOn == nil {
		return ErrUnavailable
	}
	var after guid.ID
	for {
		count := 0
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			rows, err := m.dispatchStarvation.ListOn(ctx, tx, tenant, after, at)
			if err != nil {
				return err
			}
			count = len(rows)
			for _, row := range rows {
				if row.WorkItemID <= after {
					return ErrConflict
				}
				after = row.WorkItemID
				if err = m.expiryEventOn(ctx, tx, tenant, row.ProjectID, row.WorkItemID, "dispatch_starved", row.Version, at); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if count < 100 {
			return nil
		}
	}
}
