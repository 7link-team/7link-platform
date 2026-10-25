package yunxiao

import (
	"context"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/yunxiao/query"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutomationFailureIsImmutableIdempotentAtomicAndCurrentAudience(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.workItemOn = func(_ context.Context, _ *gorm.DB, tenant, project, work guid.ID) error {
		if tenant != 1 || project != 99 || work != 88 {
			return ErrNotFound
		}
		return nil
	}
	m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, limit int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "work_item.state_changed"}}, nil
	}
	var rule, run guid.ID
	transact := func(fn func(*gorm.DB) error) { require.NoError(t, db.Transaction(fn)) }
	transact(func(tx *gorm.DB) error {
		var err error
		rule, err = m.registerAutomationRuleOn(ctx, tx, 1, 99, 0, 0, "Check transition", true)
		return err
	})
	calls := 0
	fail := func(_ context.Context, tx *gorm.DB) error {
		calls++
		require.NoError(t, tx.Exec("INSERT INTO test_effects(kind) VALUES ('action_write')").Error)
		return errors.New("private token")
	}
	execute := func(trigger guid.ID) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var err error
			run, err = m.executeAutomationOn(ctx, tx, 1, rule, trigger, 88, fail)
			return err
		})
	}
	require.NoError(t, execute(700))
	first := run
	require.NoError(t, execute(700))
	require.Equal(t, first, run)
	require.Equal(t, 1, calls)
	var actionWrites int64
	require.NoError(t, db.Table("test_effects").Where("kind = ?", "action_write").Count(&actionWrites).Error)
	require.Zero(t, actionWrites)
	fact, found, err := m.automationFailureOn(ctx, db, 1, run)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "ACTION_FAILED", fact.Error)
	require.Equal(t, guid.ID(11), fact.CreatorID)
	// Editing a rule cannot rewrite the creator/name/version captured by a run.
	editor := authn.WithClaims(ctx, &authn.Claims{TenantID: 1, PrincipalID: 12, SubjectType: authn.SubjectTypePrincipal})
	transact(func(tx *gorm.DB) error {
		_, err := m.registerAutomationRuleOn(editor, tx, 1, 99, rule, 1, "Updated", true)
		return err
	})
	fact, found, err = m.automationFailureOn(ctx, db, 1, first)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "Check transition", fact.RuleName)
	require.Equal(t, guid.ID(11), fact.CreatorID)
	revoked := false
	m.projects = delivery.ProjectScope{AuthorizeOn: func(c context.Context, _ *gorm.DB, tenant, project guid.ID, permission string) error {
		if tenant != 1 || project != 99 || revoked {
			return delivery.ErrWorkForbidden
		}
		if permission == "workflow_manage" && authn.PrincipalIDFromContext(c) != 12 {
			return delivery.ErrWorkForbidden
		}
		return nil
	}}
	for _, tc := range []struct {
		id  guid.ID
		yes bool
	}{{11, true}, {12, true}, {13, false}} {
		transact(func(tx *gorm.DB) error {
			yes, err := m.automationEligibleOn(ctx, tx, 1, first, tc.id)
			require.Equal(t, tc.yes, yes)
			return err
		})
	}
	revoked = true
	transact(func(tx *gorm.DB) error {
		yes, err := m.automationEligibleOn(ctx, tx, 1, first, 11)
		require.False(t, yes)
		return err
	})
	revoked = false
	// Restore manager authority for execution; an outbox failure rolls back receipt.
	m.projects = delivery.ProjectScope{AuthorizeOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil }}
	m.events = testPublisher{err: errors.New("outbox failed")}
	require.Error(t, execute(701))
	q := query.BindAutomationRuns(db, m.names)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	m.events = testPublisher{}
	// A foreign/missing source event never executes the action.
	m.automationEvents = func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: 702, TenantID: 2, ProjectID: 99, SubjectID: 88, SubjectKind: "work_item"}}, nil
	}
	require.ErrorIs(t, execute(702), ErrInvalid)
	require.Equal(t, 2, calls)
}

func TestAutomationSuccessSkipAuthorizationAndRollback(t *testing.T) {
	m, db, ctx := workHarness(t)
	m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
	m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
		return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectID: 88, SubjectKind: "work_item", Type: "work_item.created"}}, nil
	}
	var rule guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		rule, err = m.registerAutomationRuleOn(ctx, tx, 1, 99, 0, 0, "Rule", true)
		return err
	}))
	called := 0
	success := func(context.Context, *gorm.DB) error { called++; return nil }
	var id guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = m.executeAutomationOn(ctx, tx, 1, rule, 100, 88, success)
		return err
	}))
	_, found, err := m.automationFailureOn(ctx, db, 1, id)
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.registerAutomationRuleOn(ctx, tx, 1, 99, rule, 1, "Rule", false)
		return err
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.executeAutomationOn(ctx, tx, 1, rule, 101, 88, success)
		return err
	}))
	require.Equal(t, 1, called)
	q := query.BindAutomationRuns(db, m.names)
	rows, err := q.WithContext(ctx).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "SUCCEEDED", rows[0].Outcome)
	require.Equal(t, "SKIPPED", rows[1].Outcome)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.registerAutomationRuleOn(ctx, tx, 1, 99, rule, 1, "Stale", true)
		return err
	}), ErrConflict)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.executeAutomationOn(ctx, tx, 2, rule, 102, 88, success)
		return err
	}), ErrForbidden)
	abort := errors.New("abort")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.executeAutomationOn(ctx, tx, 1, rule, 103, 88, success)
		require.NoError(t, err)
		return abort
	}), abort)
	count, err := q.WithContext(ctx).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
}

func TestAutomationRuleAndExecutionFailuresLeaveNoActionOrReceipt(t *testing.T) {
	for _, mode := range []string{"rule_missing", "rule_query", "authority", "unbound", "work", "source", "event_type", "id", "run_query", "run_insert", "audit"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx, rule, _ := automationDispatchHarness(t)
			m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return nil }
			ruleID := rule.ID
			switch mode {
			case "rule_missing":
				ruleID = 999
			case "rule_query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("executor_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRules.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("executor_storage")
			case "authority":
				m.projects.AuthorizeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return ErrForbidden }
			case "unbound":
				m.workItemOn = nil
			case "work":
				m.workItemOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) error { return ErrNotFound }
			case "source":
				m.automationEvents = func(context.Context, *gorm.DB, guid.ID, int) ([]events.RecordedEvent, error) {
					return nil, ErrUnavailable
				}
			case "event_type":
				m.automationEvents = func(_ context.Context, _ *gorm.DB, after guid.ID, _ int) ([]events.RecordedEvent, error) {
					return []events.RecordedEvent{{ID: after + 1, TenantID: 1, ProjectID: 99, SubjectKind: "work_item", SubjectID: 88, Type: "claim.submitted"}}, nil
				}
			case "id":
				m.ids = &testIDs{err: ErrUnavailable}
			case "run_query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("executor_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRuns.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("executor_storage")
			case "run_insert":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("executor_storage", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.AutomationRuns.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Create().Remove("executor_storage")
			case "audit":
				m.audit = func(context.Context, *gorm.DB, audit.DomainEvent) error { return ErrUnavailable }
			}
			calls := 0
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.executeAutomationOn(ctx, tx, 1, ruleID, 700, 88, func(_ context.Context, tx *gorm.DB) error {
					calls++
					return tx.Exec("INSERT INTO test_effects(kind) VALUES ('executor_action')").Error
				})
				return e
			}))
			if mode == "run_query" {
				require.NoError(t, db.Callback().Query().Remove("executor_storage"))
			}
			var count int64
			require.NoError(t, db.Table(m.names.AutomationRuns.String()).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Table("test_effects").Where("kind='executor_action'").Count(&count).Error)
			require.Zero(t, count)
			if mode != "run_insert" && mode != "audit" {
				require.Zero(t, calls)
			}
		})
	}
	m, db, ctx := workHarness(t)
	action := func(context.Context, *gorm.DB) error { return nil }
	_, e := m.executeAutomationOn(ctx, nil, 1, 1, 1, 1, action)
	require.ErrorIs(t, e, ErrInvalid)
	_, e = m.executeAutomationOn(ctx, db, 1, 1, 1, 1, action)
	require.ErrorIs(t, e, ErrInvalid)
	for _, tc := range []struct {
		name    string
		version int
	}{{"", 0}, {"Valid", 1}} {
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.registerAutomationRuleOn(ctx, tx, 1, 99, 0, tc.version, tc.name, true)
			return e
		}))
	}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.registerAutomationRuleOn(ctx, tx, 1, 99, 999, 1, "Missing", true)
		return e
	}), ErrNotFound)
}
