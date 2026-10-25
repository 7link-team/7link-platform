package delivery

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func staleBaselineFixture(t *testing.T) (*Module, *gorm.DB, context.Context, guid.ID, []guid.ID) {
	t.Helper()
	m, db, ctx, requirement, change := scopeHarness(t)
	version := 1
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: workCreateBody{Title: "Linked task", Type: "TASK", Objective: "Deliver requirement", AccountablePrincipalID: 11, RequirementID: &requirement.ID, BaselineVersionNo: &version, Criteria: []workCriterionInput{{Text: "Task-owned criterion", VerificationMethod: "Manual check"}}}})
	require.NoError(t, err)
	_, err = m.scopeChangeHTTP(ctx, &scopeChangeInput{EntityID: "99", RequirementID: requirement.ID.String(), IfMatch: fmtETag(requirement.Version), Body: change})
	require.NoError(t, err)
	q := query.BindCriteria(db, m.service.names)
	criteria, err := q.WithContext(ctx).Where(q.RequirementID.Eq(requirement.ID), q.RetiredInVersion.IsNull()).Order(q.ID).Find()
	require.NoError(t, err)
	ids := make([]guid.ID, 0, len(criteria))
	for _, c := range criteria {
		ids = append(ids, c.ID)
	}
	return m, db, ctx, created.Body.Data.ID, ids
}

func TestWorkBaselineGuardsAndAuditRollback(t *testing.T) {
	for _, scenario := range []string{"version", "requirement_version", "unknown_criterion", "duplicate", "accepted", "review", "verifying", "audit", "invalid_body", "missing_contract", "corrupt_contract", "ids", "contract_write", "work_write", "work_cas"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, id, ids := staleBaselineFixture(t)
			expected := 2
			body := workBaselineBody{RequirementVersionNo: 2, CriterionIDs: ids}
			switch scenario {
			case "version":
				expected = 1
			case "requirement_version":
				body.RequirementVersionNo = 1
			case "unknown_criterion":
				body.CriterionIDs = []guid.ID{333}
			case "duplicate":
				body.CriterionIDs = []guid.ID{ids[0], ids[0]}
			case "accepted":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET state='ACCEPTED' WHERE id=?", id).Error)
			case "review":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET state='NEEDS_HUMAN_REVIEW' WHERE id=?", id).Error)
			case "verifying":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_items SET state='VERIFYING' WHERE id=?", id).Error)
			case "audit":
				m.service.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return errors.New("audit unavailable") }
			case "invalid_body":
				body.RequirementVersionNo = 0
			case "missing_contract":
				require.NoError(t, db.Exec("DELETE FROM d_delivery_work_item_contracts WHERE work_item_id=?", id).Error)
			case "corrupt_contract":
				require.NoError(t, db.Exec("UPDATE d_delivery_work_item_contracts SET snapshot=? WHERE work_item_id=?", `{"requirement_criterion_ids":123}`, id).Error)
			case "ids":
				m.ids = draftIDSource(func() (int64, error) { return 0, errors.New("id unavailable") })
			case "contract_write":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_baseline BEFORE INSERT ON d_delivery_work_item_contracts BEGIN SELECT RAISE(ABORT,'write failed'); END").Error)
			case "work_write":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_baseline BEFORE UPDATE ON d_delivery_work_items BEGIN SELECT RAISE(ABORT,'write failed'); END").Error)
			case "work_cas":
				require.NoError(t, db.Exec("CREATE TRIGGER reject_baseline BEFORE UPDATE ON d_delivery_work_items BEGIN SELECT RAISE(IGNORE); END").Error)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, e := m.workBaselineOn(ctx, tx, 1, 99, id, 11, 11, expected, body)
				return e
			})
			require.Error(t, err)
			w := query.BindWorkItems(db, m.service.names)
			work, err := w.WithContext(ctx).Where(w.ID.Eq(id)).First()
			require.NoError(t, err)
			require.Equal(t, 1, work.BaselineVersionNo)
			require.Equal(t, 2, work.Version)
			require.True(t, work.BaselineStale)
			q := query.BindWorkContracts(db, m.service.names)
			count, err := q.WithContext(ctx).Where(q.WorkItemID.Eq(id)).Count()
			require.NoError(t, err)
			if scenario == "missing_contract" {
				require.Zero(t, count)
			} else {
				require.EqualValues(t, 1, count)
			}
		})
	}
}
