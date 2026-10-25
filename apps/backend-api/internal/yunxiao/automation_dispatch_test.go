package yunxiao

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/yunxiao/model"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestAutomationDispatchAndLinkedDraftAdaptersRecheckRegistrationReplayAndRollback(t *testing.T) {
	for _, kind := range []string{"DISPATCH_TO_AGENT", "CREATE_LINKED_ITEM"} {
		for _, mode := range []string{"success", "unavailable", "unregistered", "denied", "later_failure", "extra_parameter"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				m, db, ctx, rule, envelope := automationDispatchHarness(t)
				in := automationDefinitionFixture()
				params := map[string]string{"role": "EXECUTOR"}
				if kind == "CREATE_LINKED_ITEM" {
					params = map[string]string{"type": "TASK", "title_template": "Follow up {title}"}
				}
				if mode == "extra_parameter" {
					params["unsafe"] = "true"
				}
				in.Definition.Actions = []AutomationAction{{Kind: kind, Parameters: params}}
				if mode == "later_failure" {
					in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "NOTIFY", Parameters: map[string]string{"recipients_rule": "OWNER"}})
					m.automationNotify = func(context.Context, *gorm.DB, AutomationNotice) error { return ErrForbidden }
				}
				if mode == "unregistered" {
					m.automationSystemAllowed = func(string, string) bool { return false }
				}
				calls := 0
				apply := func(c context.Context, tx *gorm.DB, change delivery.AutomationTransition) error {
					calls++
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, rule.ID, change.RuleID)
					require.Positive(t, change.RunID)
					require.Equal(t, "SYSTEM:automation:"+rule.ID.String(), change.RunAs)
					if kind == "DISPATCH_TO_AGENT" {
						require.Equal(t, []guid.ID{11, 12, 21}, change.LockedPrincipals)
					}
					if err := tx.Exec("INSERT INTO test_effects(kind) VALUES ('new_action')").Error; err != nil {
						return err
					}
					if mode == "denied" {
						return ErrForbidden
					}
					return nil
				}
				if mode != "unavailable" {
					if kind == "DISPATCH_TO_AGENT" {
						m.automationDispatch = &delivery.AutomationDispatchCommands{Discover: func(context.Context, *gorm.DB, guid.ID) ([]guid.ID, error) { return []guid.ID{21, 12}, nil }, Run: func(c context.Context, tx *gorm.DB, change delivery.AutomationDispatch) error {
							require.Equal(t, "EXECUTOR", change.Role)
							return apply(c, tx, change.AutomationTransition)
						}}
					} else {
						m.automationLinkedItem = func(c context.Context, tx *gorm.DB, change delivery.AutomationLinkedItem) error {
							require.Equal(t, "TASK", change.Type)
							require.Equal(t, "Follow up {title}", change.TitleTemplate)
							return apply(c, tx, change.AutomationTransition)
						}
					}
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
					return err
				}))
				for range 2 {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
				}
				var runs []model.AutomationRun
				require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&runs).Error)
				require.Len(t, runs, 1)
				var effects int64
				require.NoError(t, db.Table("test_effects").Where("kind=?", "new_action").Count(&effects).Error)
				if mode == "success" {
					require.Equal(t, "SUCCEEDED", runs[0].Outcome)
					require.EqualValues(t, 1, effects)
					require.Equal(t, 1, calls)
				} else {
					require.Equal(t, "FAILED", runs[0].Outcome)
					require.Zero(t, effects)
					if mode == "unavailable" || mode == "unregistered" || mode == "extra_parameter" {
						require.Zero(t, calls)
					}
				}
			})
		}
	}
}

func TestAutomationTransitionAdapterMapsStateKeyReplayAndRollback(t *testing.T) {
	for _, mode := range []string{"success", "domain_denied", "later_failure", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, envelope := automationDispatchHarness(t)
			m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
				return delivery.PlanningWorkIdentity{PrincipalID: 12, Type: "TASK", State: "DRAFT"}, nil
			}
			in := automationDefinitionFixture()
			in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "TRANSITION", Parameters: map[string]string{"to_state_key": "ready"}})
			if mode == "later_failure" {
				in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "ADD_REPORT_ENTRY", Parameters: map[string]string{"section": "uncompleted"}})
				m.automationReport = func(context.Context, *gorm.DB, AutomationReportEntry) error { return ErrForbidden }
			}
			calls := 0
			if mode != "unavailable" {
				m.automationTransition = func(c context.Context, tx *gorm.DB, change delivery.AutomationTransition) error {
					calls++
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, "READY", change.ToState)
					require.Equal(t, guid.ID(88), change.WorkItemID)
					require.Equal(t, guid.ID(11), change.CreatorID)
					require.Equal(t, guid.ID(12), change.OwnerID)
					require.Equal(t, rule.ID, change.RuleID)
					require.Positive(t, change.RunID)
					require.Equal(t, "SYSTEM:automation:"+rule.ID.String(), change.RunAs)
					if mode == "domain_denied" {
						return ErrForbidden
					}
					return tx.Exec("INSERT INTO test_effects(kind) VALUES ('actual_transition')").Error
				}
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
				return err
			}))
			for range 2 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
			}
			var runs []model.AutomationRun
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&runs).Error)
			require.Len(t, runs, 1)
			var count int64
			require.NoError(t, db.Table("test_effects").Where("kind IN ?", []string{"actual_transition", "actual_notification"}).Count(&count).Error)
			if mode == "success" {
				require.Equal(t, "SUCCEEDED", runs[0].Outcome)
				require.EqualValues(t, 2, count)
			} else {
				require.Equal(t, "FAILED", runs[0].Outcome)
				require.Zero(t, count)
				require.NotContains(t, runs[0].ActionsResult, `"ok":true`)
			}
			if mode == "unavailable" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestAutomationPreparationFieldAdapterReplaysAndRollsBack(t *testing.T) {
	for _, field := range []string{"risk", "autonomy_level", "estimate_minutes"} {
		for _, mode := range []string{"success", "domain_denied", "later_failure", "unavailable", "extra_parameter", "missing_value"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				m, db, ctx, rule, envelope := automationDispatchHarness(t)
				in := automationDefinitionFixture()
				value := "HIGH"
				if field == "estimate_minutes" {
					value = "60"
				}
				action := AutomationAction{Kind: "SET_FIELD", Parameters: map[string]string{"field": field, "value": value}}
				if mode == "extra_parameter" {
					action.Parameters["unsafe"] = "true"
				}
				if mode == "missing_value" {
					delete(action.Parameters, "value")
				}
				in.Definition.Actions = []AutomationAction{action}
				if mode == "later_failure" {
					in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "NOTIFY", Parameters: map[string]string{"recipients_rule": "OWNER"}})
					m.automationNotify = func(context.Context, *gorm.DB, AutomationNotice) error { return errors.New("later action failed") }
				}
				calls := 0
				if mode != "unavailable" {
					m.automationWorkParticipants = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
						return []guid.ID{11, 12}, nil
					}
					m.automationPreparationField = func(c context.Context, tx *gorm.DB, change delivery.AutomationPreparationField) error {
						calls++
						require.Zero(t, authn.PrincipalIDFromContext(c))
						require.Equal(t, field, change.Field)
						require.Equal(t, value, change.Value)
						require.Equal(t, rule.ID, change.RuleID)
						require.Positive(t, change.RunID)
						require.Equal(t, "SYSTEM:automation:"+rule.ID.String(), change.RunAs)
						require.Equal(t, []guid.ID{11, 12}, change.LockedPrincipals)
						if err := tx.Exec("INSERT INTO test_effects(kind) VALUES ('preparation_field')").Error; err != nil {
							return err
						}
						if mode == "domain_denied" {
							return delivery.ErrWorkPreparationState
						}
						return nil
					}
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
					return err
				}))
				for range 2 {
					require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
				}
				var runs []model.AutomationRun
				require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&runs).Error)
				require.Len(t, runs, 1)
				var effects int64
				require.NoError(t, db.Table("test_effects").Where("kind = ?", "preparation_field").Count(&effects).Error)
				if mode == "success" {
					require.Equal(t, "SUCCEEDED", runs[0].Outcome)
					require.EqualValues(t, 1, effects)
				} else {
					require.Equal(t, "FAILED", runs[0].Outcome)
					require.Zero(t, effects)
				}
				if mode == "unavailable" || mode == "extra_parameter" || mode == "missing_value" {
					require.Zero(t, calls)
				} else {
					require.Equal(t, 1, calls)
				}
			})
		}
	}
}

func TestAutomationNonWorkSourceResolutionKeepsDurableIdentityAndPreviewParity(t *testing.T) {
	for _, trigger := range []string{"review_sheet.closed", "test_run.recorded", "clientwork.test_run_recorded"} {
		for _, mode := range []string{"work_review", "no_work", "missing_provider", "forged_envelope", "foreign_source"} {
			t.Run(trigger+"/"+mode, func(t *testing.T) {
				m, db, ctx, rule, envelope := automationDispatchHarness(t)
				m.workItemOn = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) error {
					require.Equal(t, guid.ID(1), tenant)
					require.Equal(t, guid.ID(99), project)
					require.Equal(t, guid.ID(88), work)
					return nil
				}
				in := automationDefinitionFixture()
				in.Definition.Trigger.EventType = trigger
				sourceType, sourceKind := trigger, "review_sheet"
				if trigger != "review_sheet.closed" {
					sourceType, sourceKind = "clientwork.test_run_recorded", "clientwork"
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
					return err
				}))
				envelope.EventType, envelope.Subject.Kind, envelope.Subject.ID = sourceType, sourceKind, 789
				m.automationEvents = func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) {
					source := events.RecordedEvent{ID: 700, TenantID: 1, ProjectID: 99, SubjectKind: sourceKind, SubjectID: 789, Type: sourceType, Data: json.RawMessage(`{"state":"NEEDS_HUMAN_REVIEW","work_item_id":"999"}`)}
					if mode == "foreign_source" {
						source.ProjectID++
					}
					return []events.RecordedEvent{source}, nil
				}
				if mode != "missing_provider" {
					m.automationTriggerWork = func(_ context.Context, _ *gorm.DB, source events.RecordedEvent) (guid.ID, error) {
						require.Equal(t, guid.ID(789), source.SubjectID)
						if mode == "no_work" {
							return 0, nil
						}
						return 88, nil
					}
				}
				if mode == "forged_envelope" {
					envelope.Subject.ID++
				}
				err := db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) })
				if mode == "missing_provider" {
					require.ErrorIs(t, err, ErrUnavailable)
				} else if mode == "forged_envelope" || mode == "foreign_source" {
					require.ErrorIs(t, err, ErrInvalid)
				} else {
					require.NoError(t, err)
				}
				var runs []model.AutomationRun
				require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&runs).Error)
				if mode != "work_review" {
					require.Empty(t, runs)
					return
				}
				require.Len(t, runs, 1)
				require.Equal(t, guid.ID(88), runs[0].WorkItemID)
				require.Equal(t, guid.ID(700), runs[0].TriggerEventID)
				require.Equal(t, "SUCCEEDED", runs[0].Outcome)
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					preview, err := m.previewAutomationOn(ctx, tx, 1, 99, rule.ID, 700)
					require.NoError(t, err)
					require.True(t, preview.Matches)
					require.Equal(t, guid.ID(88), preview.WorkItemID)
					return m.consumeAutomation(ctx, tx, envelope)
				}))
				var count int64
				require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&count).Error)
				require.EqualValues(t, 1, count)
			})
		}
	}
}

func TestAutomationPlanningFieldsRequireExplicitValueAndExactParameters(t *testing.T) {
	for _, field := range []string{"iteration", "version", "milestone"} {
		for _, value := range []string{"", "9007199254740993"} {
			require.True(t, automationPlanningField(AutomationAction{Kind: "SET_FIELD", Parameters: map[string]string{"field": field, "value": value}}))
		}
		for _, parameters := range []map[string]string{
			{"field": field, "unexpected": ""},
			{"field": field, "value": "-1"},
			{"field": field, "value": "invalid"},
			{"field": field, "value": "0"},
			{"field": field, "value": "1", "unexpected": ""},
		} {
			require.False(t, automationPlanningField(AutomationAction{Kind: "SET_FIELD", Parameters: parameters}))
		}
	}
	require.False(t, automationPlanningField(AutomationAction{Kind: "SET_FIELD", Parameters: map[string]string{"field": "state", "value": "ACCEPTED"}}))
}

func TestAutomationVerificationAdapterLocksParticipantsBeforeProjectAndReplays(t *testing.T) {
	for _, mode := range []string{"success", "denied", "later_failure", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, envelope := automationDispatchHarness(t)
			in := automationDefinitionFixture()
			in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "REQUEST_VERIFICATION", Parameters: map[string]string{"stage": "AUTOMATED"}})
			if mode == "later_failure" {
				in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "ADD_REPORT_ENTRY", Parameters: map[string]string{"section": "uncompleted"}})
				m.automationReport = func(context.Context, *gorm.DB, AutomationReportEntry) error { return ErrForbidden }
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
				return err
			}))
			locked := []guid.ID{}
			m.principals.Lock = func(_ context.Context, _ *gorm.DB, id guid.ID) error { locked = append(locked, id); return nil }
			m.automationWorkParticipants = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
				require.Empty(t, locked)
				return []guid.ID{30, 5}, nil
			}
			m.projects.AuthorizeOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string) error {
				require.Equal(t, "workflow_manage", permission)
				if mode != "unavailable" {
					require.Equal(t, []guid.ID{5, 11, 12, 30}, locked[:4])
				}
				return nil
			}
			calls := 0
			if mode != "unavailable" {
				m.automationVerification = func(c context.Context, tx *gorm.DB, request delivery.AutomationVerification) error {
					calls++
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, []guid.ID{5, 11, 12, 30}, request.LockedPrincipals)
					require.Equal(t, guid.ID(88), request.WorkItemID)
					require.Equal(t, guid.ID(11), request.CreatorID)
					require.Equal(t, "AUTOMATED", request.Stage)
					require.Equal(t, rule.ID, request.RuleID)
					require.Positive(t, request.RunID)
					if mode == "denied" {
						return ErrForbidden
					}
					return tx.Exec("INSERT INTO test_effects(kind) VALUES ('actual_verification_task')").Error
				}
			}
			for range 2 {
				locked = nil
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
			}
			var runs []model.AutomationRun
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&runs).Error)
			require.Len(t, runs, 1)
			var count int64
			require.NoError(t, db.Table("test_effects").Where("kind IN ?", []string{"actual_verification_task", "actual_notification"}).Count(&count).Error)
			if mode == "success" {
				require.Equal(t, "SUCCEEDED", runs[0].Outcome)
				require.EqualValues(t, 2, count)
			} else {
				require.Equal(t, "FAILED", runs[0].Outcome)
				require.Zero(t, count)
			}
			if mode == "unavailable" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestAutomationPlanningFieldUsesDomainLocksAuthorityAndSystemAudit(t *testing.T) {
	for _, mode := range []string{"set", "clear", "locked", "revoked", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, envelope := automationDispatchHarness(t)
			var target PlanningEntity
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				target, err = m.savePlanningEntityOn(ctx, tx, 1, 99, 0, "versions", 0, PlanningEntityInput{Name: "Actual release"})
				return err
			}))
			value := target.ID.String()
			if mode == "clear" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.assignPlanningOn(ctx, tx, 1, 99, 88, 0, PlanningAssignmentInput{VersionID: &target.ID})
					return err
				}))
				value = ""
			}
			if mode == "locked" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, err := m.changePlanningStateOn(ctx, tx, 1, 99, target.ID, "versions", target.Version, "LOCKED", "", nil)
					return err
				}))
			}
			in := automationDefinitionFixture()
			in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "SET_FIELD", Parameters: map[string]string{"field": "version", "value": value}})
			if mode == "rollback" {
				in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "ADD_REPORT_ENTRY", Parameters: map[string]string{"section": "uncompleted"}})
				m.automationReport = func(context.Context, *gorm.DB, AutomationReportEntry) error { return ErrForbidden }
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
				return err
			}))
			if mode == "revoked" {
				m.projects.AuthorizeOn = func(_ context.Context, _ *gorm.DB, _, _ guid.ID, permission string) error {
					if permission == "work_item_write" {
						return ErrForbidden
					}
					return nil
				}
			}
			originalAudit := m.audit
			var effectsBefore int64
			require.NoError(t, db.Table("test_effects").Where("kind = ?", "planning.assignment_changed").Count(&effectsBefore).Error)
			m.audit = func(c context.Context, tx *gorm.DB, event audit.DomainEvent) error {
				if event.EventType == "planning.assignment_changed" {
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, "SYSTEM", event.Detail["actor_kind"])
					require.Equal(t, "SYSTEM:automation:"+rule.ID.String(), event.Detail["authority_source"])
					require.Positive(t, int64(event.Detail["run_id"].(guid.ID)))
				}
				return originalAudit(c, tx, event)
			}
			for range 2 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
			}
			var run model.AutomationRun
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).First(&run).Error)
			var effectsAfter int64
			require.NoError(t, db.Table("test_effects").Where("kind = ?", "planning.assignment_changed").Count(&effectsAfter).Error)
			var assignment *model.WorkPlanning
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				var err error
				assignment, err = m.assignmentOn(ctx, tx, 1, 99, 88)
				return err
			}))
			if mode == "set" || mode == "clear" {
				require.EqualValues(t, 2, effectsAfter-effectsBefore, "exactly one audit and one outbox; replay adds neither")
				require.Equal(t, "SUCCEEDED", run.Outcome)
				require.NotNil(t, assignment)
				require.Zero(t, assignment.UpdatedBy, "the rule creator is authority, never the written actor")
				if mode == "set" {
					require.Equal(t, &target.ID, assignment.VersionID)
				} else {
					require.Nil(t, assignment.VersionID)
				}
			} else {
				require.Equal(t, effectsBefore, effectsAfter, "failed action rolls back audit and outbox")
				require.Equal(t, "FAILED", run.Outcome)
				require.Nil(t, assignment)
				var count int64
				require.NoError(t, db.Table("test_effects").Where("kind = ?", "actual_notification").Count(&count).Error)
				require.Zero(t, count, "all prior actions roll back")
			}
		})
	}
}

func automationDispatchHarness(t *testing.T) (*Module, *gorm.DB, context.Context, AutomationRuleView, events.Envelope) {
	m, db, ctx := workHarness(t)
	m.automationSystemAllowed = func(job, code string) bool { return job == "automation" }
	m.automationWorkParticipants = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
		return []guid.ID{12}, nil
	}
	m.automationOwner = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) { return 12, nil }
	m.planningWork = func(c context.Context, _ *gorm.DB, tenant, project, work guid.ID) (delivery.PlanningWorkIdentity, error) {
		require.Equal(t, guid.ID(11), authn.PrincipalIDFromContext(c))
		require.Equal(t, guid.ID(1), tenant)
		require.Equal(t, guid.ID(99), project)
		require.Equal(t, guid.ID(88), work)
		return delivery.PlanningWorkIdentity{PrincipalID: 12}, nil
	}
	m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "work_item.state_changed", Data: json.RawMessage(`{"state":"NEEDS_HUMAN_REVIEW"}`)}}, nil
	}
	m.automationNotify = func(c context.Context, tx *gorm.DB, n AutomationNotice) error {
		require.Zero(t, authn.PrincipalIDFromContext(c))
		require.Equal(t, "SYSTEM:automation:"+n.RuleID.String(), n.RunAs)
		require.Equal(t, guid.ID(12), n.RecipientID)
		return tx.Exec("INSERT INTO test_effects(kind) VALUES ('actual_notification')").Error
	}
	var rule AutomationRuleView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var e error
		rule, e = m.saveAutomationDefinitionOn(ctx, tx, 1, 99, 0, 0, automationDefinitionFixture())
		return e
	}))
	return m, db, ctx, rule, events.Envelope{EventID: 700, TenantID: 1, ProjectID: 99, EventType: "work_item.state_changed", Subject: events.Subject{Kind: "work_item", ID: 88}}
}
func TestAutomationDispatcherRealActionReplayAndSystemIdentity(t *testing.T) {
	m, db, ctx, rule, envelope := automationDispatchHarness(t)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
	}
	var rows []model.AutomationRun
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "SUCCEEDED", rows[0].Outcome)
	require.Equal(t, "SYSTEM:automation:"+rule.ID.String(), rows[0].RunAs)
	require.JSONEq(t, `[{"action":"NOTIFY","ok":true}]`, rows[0].ActionsResult)
	var count int64
	require.NoError(t, db.Table("test_effects").Where("kind = ?", "actual_notification").Count(&count).Error)
	require.EqualValues(t, 1, count)
	envelope.EventID++
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
	var last model.AutomationRun
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Order("id DESC").First(&last).Error)
	require.Equal(t, "SKIPPED", last.Outcome)
	require.Equal(t, "HOURLY_LIMIT", last.Error)
	// A forged event envelope cannot select a different work item.
	envelope.Subject.ID = 89
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }), ErrInvalid)
}

func TestAutomationRuleOccurrenceFenceUsesDurableSourceClock(t *testing.T) {
	for _, sourceBeforeRule := range []bool{false, true} {
		t.Run(map[bool]string{true: "old_source_forged_new_envelope", false: "new_source_old_envelope"}[sourceBeforeRule], func(t *testing.T) {
			m, db, ctx, _, envelope := automationDispatchHarness(t)
			read := m.automationEvents
			m.automationEvents = func(c context.Context, tx *gorm.DB, after guid.ID, limit int) ([]events.RecordedEvent, error) {
				rows, err := read(c, tx, after, limit)
				if sourceBeforeRule {
					rows[0].OccurredAt = m.now().Add(-time.Hour)
				} else {
					rows[0].OccurredAt = m.now().Add(time.Hour)
				}
				return rows, err
			}
			if sourceBeforeRule {
				envelope.OccurredAt = m.now().Add(time.Hour)
			} else {
				envelope.OccurredAt = m.now().Add(-time.Hour)
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
			var count int64
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&count).Error)
			if sourceBeforeRule {
				require.Zero(t, count)
			} else {
				require.EqualValues(t, 1, count)
			}
		})
	}
}

func TestAutomationReportAdapterUsesResolvedIdentityAndAtomicActionBoundary(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			m, db, ctx, rule, envelope := automationDispatchHarness(t)
			in := automationDefinitionFixture()
			in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "ADD_REPORT_ENTRY", Parameters: map[string]string{"section": "uncompleted"}})
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, 1, in)
				return err
			}))
			m.automationReport = func(c context.Context, tx *gorm.DB, entry AutomationReportEntry) error {
				require.Zero(t, authn.PrincipalIDFromContext(c))
				require.Equal(t, guid.ID(1), authn.TenantIDFromContext(c))
				require.Equal(t, rule.ID, entry.RuleID)
				require.Equal(t, guid.ID(11), entry.CreatorID)
				require.Equal(t, guid.ID(12), entry.PrincipalID)
				require.Equal(t, guid.ID(88), entry.WorkItemID)
				require.Equal(t, "uncompleted", entry.Section)
				require.Equal(t, "SYSTEM:automation:"+rule.ID.String(), entry.RunAs)
				require.Positive(t, entry.RunID)
				if err := tx.Exec("INSERT INTO test_effects(kind) VALUES ('actual_report')").Error; err != nil {
					return err
				}
				if fail {
					return ErrForbidden
				}
				return nil
			}
			for range 2 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
			}
			var rows []model.AutomationRun
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&rows).Error)
			require.Len(t, rows, 1)
			var count int64
			require.NoError(t, db.Table("test_effects").Where("kind IN ?", []string{"actual_report", "actual_notification"}).Count(&count).Error)
			if fail {
				require.Equal(t, "FAILED", rows[0].Outcome)
				require.Zero(t, count)
				require.NotContains(t, rows[0].ActionsResult, `"ok":true`)
			} else {
				require.Equal(t, "SUCCEEDED", rows[0].Outcome)
				require.EqualValues(t, 2, count)
			}
		})
	}
}
func TestAutomationDispatcherUnsupportedRollbackAndCurrentRevocation(t *testing.T) {
	t.Run("unsupported action preflight", func(t *testing.T) {
		m, db, ctx, rule, envelope := automationDispatchHarness(t)
		in := automationDefinitionFixture()
		in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "TRANSITION", Parameters: map[string]string{"to_state_key": "ACCEPTED"}})
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, 1, in)
			return e
		}))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
		var row model.AutomationRun
		require.NoError(t, db.Table(m.names.AutomationRuns.String()).First(&row).Error)
		require.Equal(t, "FAILED", row.Outcome)
		require.Equal(t, "ACTION_UNSUPPORTED", row.Error)
		var count int64
		require.NoError(t, db.Table("test_effects").Where("kind = ?", "actual_notification").Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("action write rollback", func(t *testing.T) {
		m, db, ctx, _, envelope := automationDispatchHarness(t)
		m.automationNotify = func(_ context.Context, tx *gorm.DB, _ AutomationNotice) error {
			require.NoError(t, tx.Exec("INSERT INTO test_effects(kind) VALUES ('actual_notification')").Error)
			return errors.New("private credentials")
		}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
		var row model.AutomationRun
		require.NoError(t, db.Table(m.names.AutomationRuns.String()).First(&row).Error)
		require.Equal(t, "FAILED", row.Outcome)
		require.Equal(t, "ACTION_FAILED", row.Error)
		require.NotContains(t, row.ActionsResult, "private")
		var count int64
		require.NoError(t, db.Table("test_effects").Where("kind = ?", "actual_notification").Count(&count).Error)
		require.Zero(t, count)
	})
	t.Run("revoked manager", func(t *testing.T) {
		m, db, ctx, _, envelope := automationDispatchHarness(t)
		m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return delivery.ErrWorkForbidden }}
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
		var row model.AutomationRun
		require.NoError(t, db.Table(m.names.AutomationRuns.String()).First(&row).Error)
		require.Equal(t, "SKIPPED", row.Outcome)
		require.Equal(t, "CURRENT_AUTHORITY_REVOKED", row.Error)
	})
	t.Run("outbox atomicity", func(t *testing.T) {
		m, db, ctx, _, envelope := automationDispatchHarness(t)
		m.automationNotify = func(context.Context, *gorm.DB, AutomationNotice) error { return ErrUnavailable }
		m.events = testPublisher{err: errors.New("outbox unavailable")}
		require.Error(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
		var count int64
		require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&count).Error)
		require.Zero(t, count)
	})
}

type automationSchedulePublisher struct{ next guid.ID }

func (p *automationSchedulePublisher) Publish(_ context.Context, tx *gorm.DB, e events.Event) (guid.ID, error) {
	p.next++
	return p.next, tx.Exec("INSERT INTO test_effects(kind) VALUES (?)", e.Type).Error
}
func TestAutomationCronTimezoneSlotReplayAndValidation(t *testing.T) {
	m, db, ctx, rule, _ := automationDispatchHarness(t)
	in := automationDefinitionFixture()
	in.Definition.Trigger.EventType = "schedule"
	in.Definition.Trigger.Cron = "0 8 * * *"
	in.Definition.Trigger.Timezone = "Asia/Singapore"
	in.Definition.Trigger.WorkItemID = 88
	in.Definition.Trigger.Filters = nil
	in.Definition.Condition = nil
	in.Definition.MaxRunsPerHour = 60
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, 1, in)
		return e
	}))
	m.events = &automationSchedulePublisher{next: 800}
	for range 2 {
		require.NoError(t, m.automationScheduleJob(ctx, jobs.Job{TenantID: 1}))
	}
	var rows []model.AutomationRun
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "SUCCEEDED", rows[0].Outcome)
	require.EqualValues(t, 801, rows[0].TriggerEventID)
	m.now = func() time.Time { return time.Date(2026, 10, 20, 0, 1, 0, 0, time.UTC) }
	require.NoError(t, m.automationScheduleJob(ctx, jobs.Job{TenantID: 1}))
	var count int64
	require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&count).Error)
	require.EqualValues(t, 1, count)
	for _, cron := range []string{"@hourly", "60 * * * *", "* * * * * *"} {
		in.Definition.Trigger.Cron = cron
		_, e := validateAutomationDefinition(in.Definition)
		require.ErrorIs(t, e, ErrInvalid)
	}
	in.Definition.Trigger.Cron = "* * * * *"
	in.Definition.Trigger.Timezone = "Local"
	_, e := validateAutomationDefinition(in.Definition)
	require.ErrorIs(t, e, ErrInvalid)
}

func TestAutomationAssignAdapterReplayAuthorityAndRollback(t *testing.T) {
	for _, mode := range []string{"success", "hidden_participants", "transition_before", "domain_denied", "later_failure", "unavailable", "invalid", "extra"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, envelope := automationDispatchHarness(t)
			in := automationDefinitionFixture()
			action := AutomationAction{Kind: "ASSIGN", Parameters: map[string]string{"principal_id": "21"}}
			if mode == "invalid" {
				action.Parameters["principal_id"] = "0"
			}
			if mode == "extra" {
				action.Parameters["unsafe"] = "true"
			}
			in.Definition.Actions = []AutomationAction{action}
			if mode == "transition_before" {
				in.Definition.Actions = append([]AutomationAction{{Kind: "TRANSITION", Parameters: map[string]string{"to_state_key": "READY"}}}, in.Definition.Actions...)
				m.automationTransition = func(context.Context, *gorm.DB, delivery.AutomationTransition) error { return nil }
			}
			if mode == "later_failure" {
				in.Definition.Actions = append(in.Definition.Actions, AutomationAction{Kind: "NOTIFY", Parameters: map[string]string{"recipients_rule": "OWNER"}})
				m.automationNotify = func(context.Context, *gorm.DB, AutomationNotice) error { return ErrForbidden }
			}
			if mode == "hidden_participants" {
				m.automationWorkParticipants = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) ([]guid.ID, error) {
					return []guid.ID{31, 12, 31}, nil
				}
			}
			calls := 0
			if mode != "unavailable" {
				m.automationAssignment = func(c context.Context, tx *gorm.DB, a delivery.AutomationAssignment) error {
					calls++
					require.Zero(t, authn.PrincipalIDFromContext(c))
					require.Equal(t, guid.ID(21), a.AssigneePrincipalID)
					if mode == "hidden_participants" {
						require.Equal(t, []guid.ID{11, 12, 21, 31}, a.LockedPrincipals)
					} else {
						require.Equal(t, []guid.ID{11, 12, 21}, a.LockedPrincipals)
					}
					require.Equal(t, rule.ID, a.RuleID)
					require.Positive(t, a.RunID)
					require.Equal(t, "SYSTEM:automation:"+rule.ID.String(), a.RunAs)
					if e := tx.Exec("INSERT INTO test_effects(kind) VALUES ('assignment')").Error; e != nil {
						return e
					}
					if mode == "domain_denied" {
						return ErrForbidden
					}
					return nil
				}
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.saveAutomationDefinitionOn(ctx, tx, 1, 99, rule.ID, rule.Version, in)
				return e
			}))
			for range 2 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) }))
			}
			var runs []model.AutomationRun
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&runs).Error)
			require.Len(t, runs, 1)
			var n int64
			require.NoError(t, db.Table("test_effects").Where("kind=?", "assignment").Count(&n).Error)
			if mode == "success" || mode == "hidden_participants" || mode == "transition_before" {
				require.Equal(t, "SUCCEEDED", runs[0].Outcome)
				require.EqualValues(t, 1, n)
			} else {
				require.Equal(t, "FAILED", runs[0].Outcome)
				require.Zero(t, n)
			}
			if mode == "unavailable" || mode == "invalid" || mode == "extra" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestAutomationDispatcherSourceStorageAndCurrentOwnerFailuresNeverCommitActions(t *testing.T) {
	for _, mode := range []string{"unbound", "source", "owner_missing", "owner_error", "owner_changed", "rule_corrupt", "rule_version", "lock", "authority", "work_missing", "work_error", "id", "run_read", "run_write", "audit"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, envelope := automationDispatchHarness(t)
			switch mode {
			case "unbound":
				m.automationEvents = nil
			case "source":
				m.automationEvents = func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) {
					return nil, ErrUnavailable
				}
			case "owner_missing":
				m.automationOwner = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) {
					return 0, gorm.ErrRecordNotFound
				}
			case "owner_error":
				m.automationOwner = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) { return 0, ErrUnavailable }
			case "owner_changed":
				m.automationOwner = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) { return 13, nil }
			case "rule_corrupt":
				require.NoError(t, db.Table(m.names.AutomationRules.String()).Where("id=?", rule.ID).Update("definition", "[").Error)
			case "rule_version":
				m.automationOwner = func(_ context.Context, tx *gorm.DB, _, _, _ guid.ID) (guid.ID, error) {
					return 12, tx.Table(m.names.AutomationRules.String()).Where("id=?", rule.ID).Update("version", 2).Error
				}
			case "lock":
				m.principals.Lock = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "authority":
				m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrUnavailable }
			case "work_missing":
				m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
					return delivery.PlanningWorkIdentity{}, delivery.ErrProjectNotFound
				}
			case "work_error":
				m.planningWork = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (delivery.PlanningWorkIdentity, error) {
					return delivery.PlanningWorkIdentity{}, ErrUnavailable
				}
			case "id":
				m.ids = &testIDs{err: ErrUnavailable}
			case "run_read":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("automation_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRuns.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("automation_storage")
			case "run_write":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("automation_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRuns.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Create().Remove("automation_storage")
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			}
			err := db.Transaction(func(tx *gorm.DB) error { return m.consumeAutomation(ctx, tx, envelope) })
			if mode == "owner_missing" || mode == "rule_version" || mode == "work_missing" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			var effects int64
			require.NoError(t, db.Table("test_effects").Where("kind='actual_notification'").Count(&effects).Error)
			require.Zero(t, effects)
			if mode == "run_read" {
				require.NoError(t, db.Callback().Query().Remove("automation_storage"))
			}
			var runs []model.AutomationRun
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Find(&runs).Error)
			if mode == "work_missing" {
				require.Len(t, runs, 1)
				require.Equal(t, "SKIPPED", runs[0].Outcome)
				require.Equal(t, "WORK_UNAVAILABLE", runs[0].Error)
			} else {
				require.Empty(t, runs)
			}
		})
	}
}

func TestAutomationActionConnectorsBindExactlyOnceAndExposeRunnableSchedules(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideAutomationDispatcher(s))
	require.NoError(t, s.Seal())
	report, e := module.Resolve[AutomationReportWriterConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, report.Bind(nil), ErrInvalid)
	write := func(context.Context, *gorm.DB, AutomationReportEntry) error { return nil }
	require.NoError(t, report.Bind(write))
	require.ErrorIs(t, report.Bind(write), ErrInvalid)
	notify, e := module.Resolve[AutomationNotifierConnector](s)
	require.NoError(t, e)
	require.ErrorIs(t, notify.Bind(nil), ErrInvalid)
	send := func(context.Context, *gorm.DB, AutomationNotice) error { return nil }
	require.NoError(t, notify.Bind(send))
	require.ErrorIs(t, notify.Bind(send), ErrInvalid)
	schedules := m.JobSchedules()
	require.Len(t, schedules, 1)
	require.Equal(t, time.Minute, schedules[0].Interval)
	handlers := m.JobHandlers()
	require.Len(t, handlers, 2)
	require.NotNil(t, handlers[0].Run)
	require.NotNil(t, handlers[1].Run)
	require.ErrorIs(t, handlers[1].Run(t.Context(), jobs.Job{}), ErrInvalid)
	require.ErrorIs(t, handlers[1].Run(t.Context(), jobs.Job{TenantID: 1}), ErrUnavailable)
}

func TestAutomationDispatcherReportsDuplicateCapabilityPublication(t *testing.T) {
	m := &Module{}
	s := module.NewServices()
	require.NoError(t, m.provideAutomationDispatcher(s))
	require.Error(t, m.provideAutomationDispatcher(s))
	s = module.NewServices()
	require.NoError(t, module.Provide[AutomationCommands](s, AutomationCommands{}))
	require.Error(t, m.provideAutomation(s))
	s = module.NewServices()
	require.NoError(t, module.Provide[AutomationNotifierConnector](s, AutomationNotifierConnector{}))
	require.Error(t, m.provideAutomationDispatcher(s))
}
