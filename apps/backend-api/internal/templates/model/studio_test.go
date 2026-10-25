package model

import "testing"

func TestStudioRecordTableName(t *testing.T) {
	if (StudioRecord{}).TableName() != "templates_studio_records" {
		t.Fatal("studio record table drift")
	}
}
