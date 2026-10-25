package model

import "testing"

func TestProjectBudgetEventTableName(t *testing.T) {
	if (ProjectBudgetEvent{}).TableName() != "delivery_project_budget_events" {
		t.Fatal("unexpected budget ledger table")
	}
}
