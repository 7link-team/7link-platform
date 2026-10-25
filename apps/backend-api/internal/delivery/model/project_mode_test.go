package model

import "testing"

func TestProjectModeChangeTableName(t *testing.T) {
	if (ProjectModeChange{}).TableName() != "delivery_project_mode_changes" {
		t.Fatal("unexpected mode history table")
	}
}
