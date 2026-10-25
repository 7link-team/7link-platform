package clientwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"7link.com/platform/internal/clientwork/model"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// The existing actor fence requires an active Human; no Agent, including a
// reviewer Agent, receives hidden content. Runtime verification needs a separate
// platform security identity and is deliberately not exposed by this module.
func (m *Module) hiddenAcceptanceAllowedOn(ctx context.Context, tx *gorm.DB, project guid.ID) (bool, error) {
	_, err := m.authorize(ctx, tx, project, "acceptance_suite_manage", false)
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || errors.Is(err, delivery.ErrWorkForbidden) || errors.Is(err, delivery.ErrProjectNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (m *Module) requireHiddenAcceptanceOn(ctx context.Context, tx *gorm.DB, project guid.ID) error {
	allowed, err := m.hiddenAcceptanceAllowedOn(ctx, tx, project)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

// Hash the complete versioned source record, but export no hidden title, steps,
// expected output, or defect link into requirements, contexts or runner payloads.
func requirementCaseSnapshot(row *model.TestCase) delivery.RequirementCaseSnapshot {
	snapshot := delivery.RequirementCaseSnapshot{ID: row.ID, Version: row.Version, WorkItemID: row.WorkItemID, Title: row.Title, Steps: row.Steps, Expected: row.Expected}
	if !row.IsHiddenAcceptance {
		snapshot.RequirementID, snapshot.CriterionID, snapshot.CriterionSource, snapshot.Preconditions, snapshot.Priority = row.RequirementID, row.CriterionID, row.CriterionSource, row.Preconditions, row.Priority
	}
	if row.IsHiddenAcceptance {
		data, _ := hiddenCaseContent(row)
		sum := sha256.Sum256(data)
		snapshot.Title, snapshot.Steps, snapshot.Expected = "", "", ""
		snapshot.IsHiddenAcceptance, snapshot.ContentHash = true, hex.EncodeToString(sum[:])
	}
	return snapshot
}

// Defaults introduced by the CSV migration must not change the exact bytes or
// hash of an already frozen legacy case version.
func hiddenCaseContent(row *model.TestCase) ([]byte, error) {
	view := caseView(row)
	if row.RequirementID == nil && row.CriterionID == nil && row.CriterionSource == "" && row.Preconditions == "" && !row.Unclassified && (row.Priority == "" || row.Priority == "P2") {
		view.Priority = ""
	}
	return json.Marshal(view)
}
