package clientwork

import (
	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTestCaseImportDefaultsPreserveFrozenHiddenCaseBytes(t *testing.T) {
	row := &model.TestCase{IsHiddenAcceptance: true, ID: 10, Version: 1, Title: "Private", Steps: "Secret", Expected: "Done", WorkItemID: 11, Priority: "P2"}
	raw, err := hiddenCaseContent(row)
	require.NoError(t, err)
	require.Equal(t, `{"is_hidden_acceptance":true,"id":"10","version":1,"title":"Private","steps":"Secret","expected":"Done","work_item_id":"11"}`, string(raw)) //nolint:testifylint // Serialized bytes are frozen by this regression contract.
	legacyHash := requirementCaseSnapshot(row).ContentHash
	row.Priority = ""
	require.Equal(t, legacyHash, requirementCaseSnapshot(row).ContentHash)
	row.Preconditions = "Secret precondition"
	require.NotEqual(t, legacyHash, requirementCaseSnapshot(row).ContentHash)
	snapshot := requirementCaseSnapshot(row)
	require.Empty(t, snapshot.Preconditions)
	require.Empty(t, snapshot.Priority)
	require.Empty(t, snapshot.CriterionSource)
}

func (p *testProjects) TestCaseScopeOn(ctx context.Context, tx *gorm.DB, tenant, project, work, requirement guid.ID) (delivery.ClientRequirement, error) {
	if err := p.ValidateWorkOn(ctx, tx, tenant, project, work, false); err != nil {
		return delivery.ClientRequirement{}, err
	}
	if err := p.LockRequirementReadOn(ctx, tx, tenant, project, requirement); err != nil {
		return delivery.ClientRequirement{}, err
	}
	rows, err := p.RequirementsOn(ctx, tx, tenant, project, []guid.ID{requirement})
	if err != nil {
		return delivery.ClientRequirement{}, err
	}
	return rows[0], nil
}
func TestTestCaseClassificationRejectsForeignCriteriaAndRetainsMetadata(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	req, criterion := guid.ID(10), guid.ID(20)
	body := TestCaseBody{Title: "Imported", Steps: "Action", Expected: "Result", WorkItemID: 11, RequirementID: &req, CriterionID: &criterion, CriterionSource: "C1", Preconditions: "Signed in", Priority: "P1"}
	var r Receipt
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var err error; r, err = m.writeCaseOn(ctx, tx, 3, 0, 0, body); return err }))
	body.CriterionID = nil
	body.RequirementID = nil
	body.CriterionSource = ""
	body.Preconditions = ""
	body.Priority = ""
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(ctx, tx, 3, r.ID, r.Version, body); return err }))
	view, err := m.get_test_caseHTTP(ctx, &ObjectInput{EntityID: "3", ID: r.ID.String()})
	require.NoError(t, err)
	require.Equal(t, "Signed in", view.Body.Data.Preconditions)
	require.Equal(t, "P1", view.Body.Data.Priority)
	require.Equal(t, &criterion, view.Body.Data.CriterionID)
	wrong := guid.ID(999)
	body.RequirementID = &req
	body.CriterionID = &wrong
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.writeCaseOn(ctx, tx, 3, 0, 0, body); return err }), ErrInvalid)
}
