package clientwork

import (
	"7link.com/platform/internal/clientwork/query"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"database/sql/driver"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) bindRequirementSuites(s *module.Services) error {
	connector, err := module.Resolve[delivery.RequirementSuiteConnector](s)
	if err != nil {
		return err
	}
	connector.Bind(m.requirementCasesOn)
	return nil
}

// Delivery holds the actor/project fence before this read; case edits take the
// same project fence. Only the case owner reads its physical storage.
func (m *Module) requirementCasesOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, workIDs []guid.ID) ([]delivery.RequirementCaseSnapshot, error) {
	out := []delivery.RequirementCaseSnapshot{}
	if len(workIDs) == 0 {
		return out, nil
	}
	ids := make([]driver.Valuer, len(workIDs))
	for i, id := range workIDs {
		ids[i] = id
	}
	q := query.BindTestCase(tx, m.names)
	rows, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.TenantID.Eq(tenant), q.ProjectID.Eq(project), q.WorkItemID.In(ids...)).Order(q.ID).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		// Unresolved imported criteria never contribute to a frozen acceptance suite.
		if row.Unclassified {
			continue
		}
		if row.IsHiddenAcceptance {
			if err := m.archiveHiddenCaseOn(ctx, tx, row); err != nil {
				return nil, err
			}
		}
		out = append(out, requirementCaseSnapshot(row))
	}
	return out, nil
}
