package model

import "testing"

func TestAcceptanceReminderTableName(t *testing.T) {
	if (AcceptanceReminder{}).TableName() != "clientwork_acceptance_reminders" {
		t.Fatal("unexpected acceptance reminder table")
	}
}
