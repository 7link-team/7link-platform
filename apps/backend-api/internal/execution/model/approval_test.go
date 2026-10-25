package model

import "testing"

func TestApprovalTableName(t *testing.T) {
	if (Approval{}).TableName() != "execution_approvals" {
		t.Fatal("approval table identity changed")
	}
}
