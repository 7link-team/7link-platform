package yunxiao

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/model"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func automationDefinitionFixture() AutomationRuleInput {
	in := AutomationRuleInput{Name: "Notify review", Enabled: true}
	in.Definition.Trigger.EventType = "work_item.state_changed"
	in.Definition.Trigger.Filters = map[string]string{"state": "NEEDS_HUMAN_REVIEW"}
	in.Definition.Condition = json.RawMessage(`{"==":[{"var":"event.data.state"},"NEEDS_HUMAN_REVIEW"]}`)
	in.Definition.Actions = []AutomationAction{{Kind: "NOTIFY", Parameters: map[string]string{"recipients_rule": "OWNER"}}}
	in.Definition.MaxRunsPerHour = 1
	return in
}
func TestAutomationDefinitionValidationAndReadOnlyAuthoritativePreview(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.workItemOn = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) error {
		if tenant != 1 || project != 99 || work != 88 {
			return ErrNotFound
		}
		return nil
	}
	m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "work_item.state_changed", Data: json.RawMessage(`{"state":"NEEDS_HUMAN_REVIEW"}`)}}, nil
	}
	in := automationDefinitionFixture()
	var rule AutomationRuleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		rule, e = m.saveAutomationDefinitionOn(ctx, tx, 1, 99, 0, 0, in)
		return e
	}))
	var before int64
	require.NoError(t, db.Table("test_effects").Count(&before).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		preview, e := m.previewAutomationOn(ctx, tx, 1, 99, rule.ID, 700)
		require.NoError(t, e)
		require.True(t, preview.Matches)
		require.False(t, preview.Executed)
		require.Len(t, preview.Actions, 1)
		require.Equal(t, "NOTIFY_REPORT_DRAFT_PLANNING_TRANSITION_AND_VERIFICATION", preview.ExecutionMode)
		return e
	}))
	var after int64
	require.NoError(t, db.Table("test_effects").Count(&after).Error)
	require.Equal(t, before, after)
	var count int64
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&count).Error)
	require.Zero(t, count)
	in.Definition.Condition = json.RawMessage(`{"execute":["shell"]}`)
	_, e := validateAutomationDefinition(in.Definition)
	require.ErrorIs(t, e, ErrInvalid)
	in = automationDefinitionFixture()
	in.Definition.MaxRunsPerHour = 61
	_, e = validateAutomationDefinition(in.Definition)
	require.ErrorIs(t, e, ErrInvalid)
	m.automationEvents = func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: 700, TenantID: 2, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88}}, nil
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.previewAutomationOn(ctx, tx, 1, 99, rule.ID, 700); return e }), ErrNotFound)
}
func TestConfiguredAutomationHourlyCapAndImmutableDefinition(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "work_item.state_changed", Data: json.RawMessage(`{"state":"NEEDS_HUMAN_REVIEW"}`)}}, nil
	}
	in := automationDefinitionFixture()
	var rule AutomationRuleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		rule, e = m.saveAutomationDefinitionOn(ctx, tx, 1, 99, 0, 0, in)
		return e
	}))
	calls := 0
	for _, event := range []guid.ID{700, 701} {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.executeAutomationOn(ctx, tx, 1, rule.ID, event, 88, func(context.Context, *gorm.DB) error { calls++; return nil })
			return e
		}))
	}
	require.Equal(t, 1, calls)
	var rows []model.AutomationRun
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Order("id").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, "SUCCEEDED", rows[0].Outcome)
	require.Equal(t, "SKIPPED", rows[1].Outcome)
	require.Equal(t, "HOURLY_LIMIT", rows[1].Error)
	require.Contains(t, rows[0].DefinitionSnapshot, "NOTIFY")
	in.Name = "Edited rule"
	in.Definition.Actions[0].Parameters["recipients_rule"] = "MANAGERS"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
		return e
	}))
	var original model.AutomationRun
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Where("id = ?", rows[0].ID).First(&original).Error)
	require.Equal(t, rows[0].DefinitionSnapshot, original.DefinitionSnapshot)
}

func TestConfiguredAutomationLogicPersistsAndPreviewsExactBooleanSemantics(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "work_item.state_changed", Data: json.RawMessage(`{"state":"NEEDS_HUMAN_REVIEW","zero":0,"count":2,"empty":"","flag":false,"object":{}}`)}}, nil
	}
	for _, tc := range []struct {
		condition string
		matches   bool
	}{
		{`{"and":[{"!=":[{"var":"event.data.state"},"DRAFT"]},{"!":[{"var":"event.data.flag"}]}]}`, true},
		{`{"and":[true,false]}`, false},
		{`{"or":[false,{"var":"event.data.count"}]}`, true},
		{`{"or":[{"var":"event.data.zero"},{"var":"event.data.empty"},{"var":"event.data.missing"}]}`, false},
		{`{"!":[{"var":"event.data.state.child"}]}`, true},
		{`{"var":"event.data.object"}`, true},
	} {
		in := automationDefinitionFixture()
		in.Definition.Condition = json.RawMessage(tc.condition)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			rule, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, 0, 0, in)
			if e != nil {
				return e
			}
			preview, e := m.previewAutomationOn(ctx, tx, 1, 99, rule.ID, 700)
			require.NoError(t, e)
			require.Equal(t, tc.matches, preview.Matches)
			require.False(t, preview.Executed)
			return e
		}))
	}
	var runs int64
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&runs).Error)
	require.Zero(t, runs)
	for _, condition := range []string{`{}`, `{"var":1}`, `{"var":"ambient.secret"}`, `{"==":true}`, `{"==":[]}`, `{"!":[]}`, `{"and":[]}`, `{"or":[]}`, `{"and":[{"execute":[]}]}`, `{`} {
		in := automationDefinitionFixture()
		in.Definition.Condition = json.RawMessage(condition)
		require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, 0, 0, in); return e }), ErrInvalid, condition)
	}
}

func TestAutomationPreviewCurrentSourceAndStoredDefinitionFailuresAreExplicit(t *testing.T) {
	for _, mode := range []string{"unbound", "rule_missing", "source_error", "source_missing", "source_other_subject", "work_denied", "corrupt", "payload", "condition", "disabled", "filter", "trigger", "archived_update", "definition_update"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, _ := automationDispatchHarness(t)
			m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
			id := rule.ID
			switch mode {
			case "unbound":
				m.automationEvents = nil
			case "rule_missing":
				id = 99999
			case "source_error":
				m.automationEvents = func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) {
					return nil, ErrUnavailable
				}
			case "source_missing":
				m.automationEvents = func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) { return nil, nil }
			case "source_other_subject":
				m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
					return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "irrelevant", SubjectID: 88}}, nil
				}
			case "work_denied":
				m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrForbidden }
			case "corrupt":
				require.NoError(t, db.Table(m.names.AutomationRules.String()).Where("id=?", id).Update("definition", "[").Error)
			case "payload":
				m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
					return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "work_item.state_changed", Data: []byte("[")}}, nil
				}
			case "condition":
				in := automationDefinitionFixture()
				in.Definition.Condition = json.RawMessage(`{"execute":[]}`)
				raw, e := json.Marshal(in.Definition)
				require.NoError(t, e)
				require.NoError(t, db.Table(m.names.AutomationRules.String()).Where("id=?", id).Update("definition", string(raw)).Error)
			case "disabled":
				require.NoError(t, db.Table(m.names.AutomationRules.String()).Where("id=?", id).Update("enabled", false).Error)
			case "archived_update":
				require.NoError(t, db.Table(m.names.AutomationRules.String()).Where("id=?", id).Update("archived_at", m.now()).Error)
			case "definition_update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("definition_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRules.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Update().Remove("definition_storage")
			case "filter", "trigger":
				in := automationDefinitionFixture()
				if mode == "filter" {
					in.Definition.Trigger.Filters["state"] = "DRAFT"
				} else {
					in.Definition.Trigger.EventType = "work_item.created"
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, id, 1, in); return e }))
			}
			if mode == "archived_update" || mode == "definition_update" {
				require.Error(t, db.Transaction(func(tx *gorm.DB) error {
					_, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, id, 1, automationDefinitionFixture())
					return e
				}))
				return
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				preview, e := m.previewAutomationOn(ctx, tx, 1, 99, id, 700)
				if mode == "disabled" || mode == "filter" || mode == "trigger" {
					require.NoError(t, e)
					require.False(t, preview.Matches)
					require.NotEmpty(t, preview.Reason)
				}
				return e
			})
			if mode != "disabled" && mode != "filter" && mode != "trigger" {
				require.Error(t, err)
			}
			var count int64
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
