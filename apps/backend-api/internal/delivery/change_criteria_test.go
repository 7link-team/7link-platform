package delivery

import (
	"7link.com/platform/internal/delivery/query"
	"7link.com/platform/internal/infra/guid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestScopeCriteriaReplacementPreservesHistoryAndStalesAllLinkedWork(t *testing.T) {
	m, db, ctx, row, body := scopeHarness(t)
	q := query.BindCriteria(db, m.service.names)
	original, err := q.WithContext(ctx).Where(q.RequirementID.Eq(row.ID)).Order(q.Position).Find()
	require.NoError(t, err)
	require.Len(t, original, 2)
	version := 1
	workBody := workCreateBody{Title: "Linked task", Type: "TASK", Objective: "Deliver requirement", AccountablePrincipalID: 11, RequirementID: &row.ID, BaselineVersionNo: &version, Criteria: []workCriterionInput{{Text: "Task-owned criterion", VerificationMethod: "Manual check"}}}
	created, err := m.createWorkHTTP(ctx, &workCreateInput{EntityID: "99", Body: workBody})
	require.NoError(t, err)
	body.Title = nil
	body.Criteria = scopeCriteriaBody{Add: []requirementCriterionInput{{Text: "Additional requirement", Required: true}}, Retire: []guid.ID{original[1].ID}, Supersede: []scopeCriterionReplacement{{CriterionID: original[0].ID, Text: "Revised required clause", Required: true}}}
	var view scopeChangeView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		view, e = m.scopeChangeOn(ctx, tx, 1, 99, row.ID, 11, row.Version, "", body)
		return e
	}))
	all, err := q.WithContext(ctx).Where(q.RequirementID.Eq(row.ID)).Order(q.Position, q.ID).Find()
	require.NoError(t, err)
	require.Len(t, all, 4)
	for _, old := range original {
		now, err := q.WithContext(ctx).Where(q.ID.Eq(old.ID)).First()
		require.NoError(t, err)
		require.Equal(t, old.Text, now.Text)
		require.Equal(t, old.Code, now.Code)
		require.Equal(t, 2, *now.RetiredInVersion)
	}
	w := query.BindWorkItems(db, m.service.names)
	work, err := w.WithContext(ctx).Where(w.ID.Eq(created.Body.Data.ID)).First()
	require.NoError(t, err)
	require.True(t, work.BaselineStale)
	require.Equal(t, 2, *work.BaselineStaleSince)
	require.Equal(t, 1, work.BaselineVersionNo)
	require.Equal(t, created.Body.Data.Version+1, work.Version)
	impacts := query.BindChangeImpacts(db, m.service.names)
	record, err := impacts.WithContext(ctx).Where(impacts.ChangeRecordID.Eq(view.ID)).First()
	require.NoError(t, err)
	require.Equal(t, work.ID, record.WorkItemID)
	require.Equal(t, created.Body.Data.Version, record.VersionBefore)
	versions := query.BindRequirementVersions(db, m.service.names)
	old, err := versions.WithContext(ctx).Where(versions.RequirementID.Eq(row.ID), versions.VersionNo.Eq(1)).First()
	require.NoError(t, err)
	require.Contains(t, old.Criteria, "Acceptance")
	require.NotContains(t, old.Criteria, "Revised required clause")
}

func TestScopeCriteriaInvalidRetirementRollsBackAllChanges(t *testing.T) {
	for _, scenario := range []string{"all_required", "duplicate_retirement", "unknown", "invalid_replacement", "limit"} {
		t.Run(scenario, func(t *testing.T) {
			m, db, ctx, row, body := scopeHarness(t)
			q := query.BindCriteria(db, m.service.names)
			original, err := q.WithContext(ctx).Where(q.RequirementID.Eq(row.ID)).Order(q.Position).Find()
			require.NoError(t, err)
			body.Title = nil
			switch scenario {
			case "all_required":
				body.Criteria.Retire = []guid.ID{original[0].ID}
			case "duplicate_retirement":
				body.Criteria.Retire = []guid.ID{original[1].ID, original[1].ID}
			case "unknown":
				body.Criteria.Retire = []guid.ID{999}
			case "invalid_replacement":
				body.Criteria.Supersede = []scopeCriterionReplacement{{CriterionID: original[0].ID, Text: " "}}
			case "limit":
				for range 50 {
					body.Criteria.Add = append(body.Criteria.Add, requirementCriterionInput{Text: "New clause", Required: true})
				}
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				_, e := m.scopeChangeOn(ctx, tx, 1, 99, row.ID, 11, row.Version, "", body)
				return e
			})
			require.Error(t, err)
			current, err := q.WithContext(ctx).Where(q.RequirementID.Eq(row.ID), q.RetiredInVersion.IsNull()).Find()
			require.NoError(t, err)
			require.Len(t, current, 2)
			var count int64
			require.NoError(t, db.Table("d_delivery_change_records").Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
