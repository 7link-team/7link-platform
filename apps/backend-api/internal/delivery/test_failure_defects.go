package delivery

import (
	"context"
	"encoding/json"
	"fmt"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

// TestFailureDefects creates ordinary unconfirmed BUG work from a frozen failed
// test occurrence. The caller owns the immutable occurrence and transaction.
type TestFailureDefects interface {
	CreateOn(context.Context, *gorm.DB, guid.ID, guid.ID, TestFailureDefect) (guid.ID, error)
}

type TestFailureDefect struct {
	Title                  string  `json:"title" minLength:"1" maxLength:"200"`
	Severity               string  `json:"severity" enum:"FATAL,CRITICAL,NORMAL,MINOR"`
	Priority               string  `json:"priority" enum:"URGENT,HIGH,MEDIUM,LOW"`
	Reproducibility        string  `json:"reproducibility" enum:"ALWAYS,HIGH,MEDIUM,LOW,UNABLE"`
	AccountablePrincipalID guid.ID `json:"accountable_principal_id"`
	PlanID                 guid.ID `json:"-"`
	PlanVersion            int     `json:"-"`
	RunID                  guid.ID `json:"-"`
	CaseID                 guid.ID `json:"-"`
	CaseVersion            int     `json:"-"`
	Steps                  string  `json:"-"`
	Expected               string  `json:"-"`
	Observed               string  `json:"-"`
}

func (a clientWork) CreateOn(ctx context.Context, tx *gorm.DB, tenant, project guid.ID, in TestFailureDefect) (guid.ID, error) {
	if in.PlanID <= 0 || in.PlanVersion < 1 || in.RunID <= 0 || in.CaseID <= 0 || in.CaseVersion < 1 || !validFailureText(in.Steps) || !validFailureText(in.Expected) || !validFailureText(in.Observed) {
		return 0, ErrInvalidArgument
	}
	provenance, err := json.Marshal(map[string]any{"source": "TEST_PLAN", "plan_id": in.PlanID, "plan_version": in.PlanVersion, "run_id": in.RunID, "case_id": in.CaseID, "case_version": in.CaseVersion})
	if err != nil {
		return 0, err
	}
	description := fmt.Sprintf("%s\n\n%s", provenance, in.Observed)
	row, err := a.m.createWorkOn(ctx, tx, tenant, project, authn.PrincipalIDFromContext(ctx), workCreateBody{
		Type: "BUG", Title: in.Title, Description: description, Objective: in.Expected,
		Severity: &in.Severity, Priority: &in.Priority, Reproducibility: &in.Reproducibility, AccountablePrincipalID: in.AccountablePrincipalID,
		DefectDetails: &DefectDescription{Steps: in.Steps, Expected: in.Expected, Actual: in.Observed},
		Criteria:      []workCriterionInput{{Text: in.Expected, VerificationMethod: "Re-run the referenced frozen test case and record independent evidence"}},
	})
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

func validFailureText(s string) bool { return len(s) > 0 && len(s) <= 32000 }
