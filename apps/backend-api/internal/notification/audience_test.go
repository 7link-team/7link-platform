package notification

import (
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestSourceAudienceProviderFailuresNeverGrantAccess(t *testing.T) {
	for _, typ := range []string{"execution.cost_paused", "execution.interrupted", "planning.loop_escalated", "governance.delegation_expiring", "governance.delegation_granted", "governance.delegation_ended", "memory.rule_pending", "work_item.review_ready", "work_item.rework", "gateway.key_rotated"} {
		t.Run(typ, func(t *testing.T) {
			f := newFixture(t)
			f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
				return execution.NotificationFacts{}, ErrUnavailable
			}
			f.m.planningFacts.LoopEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) {
				return false, ErrUnavailable
			}
			f.m.delegationFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.DelegationNotificationFact, bool, error) {
				return governance.DelegationNotificationFact{}, false, ErrUnavailable
			}
			f.m.ruleEligible = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, ErrUnavailable }
			f.m.workFacts.ReviewEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, ErrUnavailable }
			f.m.workFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.WorkNotificationFacts, bool, error) {
				return delivery.WorkNotificationFacts{}, false, ErrUnavailable
			}
			f.m.gatewayRotations = func(context.Context, *gorm.DB, guid.ID, guid.ID) (bool, error) { return false, ErrUnavailable }
			row := &model.Notification{TenantID: 7, ProjectID: 81, Type: typ, Params: `{"subject_id":"91","run_id":"91"}`}
			if typ == "gateway.key_rotated" {
				row.ProjectID = 0
			}
			yes, err := f.m.sourceAudienceOn(t.Context(), f.db, row, 11)
			require.False(t, yes)
			require.ErrorIs(t, err, ErrUnavailable)
			for _, raw := range []string{"[]", `{}`} {
				row.Params = raw
				yes, err = f.m.sourceAudienceOn(t.Context(), f.db, row, 11)
				require.False(t, yes)
				require.Error(t, err)
			}
		})
	}
}

func TestSourceAudienceUnavailableCapabilitiesAndMalformedIdentityFailClosed(t *testing.T) {
	for _, typ := range []string{"planning.loop_escalated", "execution.cost_paused", "work_item.lease_expired", "verification.human_requested", "gateway.key_rotated", "governance.delegation_expiring", "governance.delegation_granted", "governance.delegation_ended", "memory.rule_pending", "work_item.review_ready", "work_item.rework", "execution.interrupted"} {
		t.Run(typ, func(t *testing.T) {
			f := newFixture(t)
			row := &model.Notification{TenantID: 7, ProjectID: 81, Type: typ, Params: `{"subject_id":"91","run_id":"91","assignment_id":"91"}`}
			if typ == "gateway.key_rotated" {
				row.ProjectID = 0
			}
			f.m.planningFacts.LoopEligibleOn = nil
			f.m.executionFacts = nil
			f.m.decisionFacts.LeaseExpiredOn = nil
			f.m.decisionFacts.HumanVerificationOn = nil
			f.m.gatewayRotations = nil
			f.m.delegationFacts = nil
			f.m.ruleEligible = nil
			f.m.workFacts.ReviewEligibleOn = nil
			f.m.workFacts.FactsOn = nil
			yes, err := f.m.sourceAudienceOn(t.Context(), f.db, row, 11)
			require.False(t, yes)
			require.ErrorIs(t, err, ErrUnavailable)
		})
	}
	for _, typ := range []string{"planning.loop_escalated", "execution.cost_paused", "work_item.lease_expired"} {
		t.Run(typ+"_malformed", func(t *testing.T) {
			f := newFixture(t)
			f.m.planningFacts.LoopEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (bool, error) {
				return false, ErrUnavailable
			}
			f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
				return execution.NotificationFacts{}, ErrUnavailable
			}
			f.m.decisionFacts.LeaseExpiredOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.LeaseExpiredNotificationFact, bool, error) {
				return delivery.LeaseExpiredNotificationFact{}, false, ErrUnavailable
			}
			for _, raw := range []string{"[]", `{}`} {
				yes, err := f.m.sourceAudienceOn(t.Context(), f.db, &model.Notification{TenantID: 7, ProjectID: 81, Type: typ, Params: raw}, 11)
				require.False(t, yes)
				require.ErrorIs(t, err, ErrInvalid)
			}
		})
	}
}

func TestSourceAudienceCurrentExecutionRequirementBudgetAndKnowledge(t *testing.T) {
	f := newFixture(t)
	// No generic read role or owner grant. Only source facts may add recipients.
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.Permission != "project_takeover"}, nil
	}
	mode := "HYBRID"
	executor := guid.ID(12)
	initiator := guid.ID(11)
	f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
		return execution.NotificationFacts{ProjectID: 81, WorkItemID: 91, InitiatorID: initiator}, nil
	}
	f.m.workFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.WorkNotificationFacts, bool, error) {
		return delivery.WorkNotificationFacts{ProjectID: 81, ExecutorID: executor}, true, nil
	}
	f.m.workFacts.ProjectModeOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return mode, nil }
	r := &model.Notification{TenantID: 7, ProjectID: 81, RecipientPrincipalID: 11, Permission: "execution_read", Params: `{"subject_id":"91"}`, Type: "execution.interrupted"}
	allowed, err := f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	initiator = 13
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, allowed)
	executor = 11
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	executor = 12
	mode = "AUTONOMOUS"
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.Permission != "project_takeover", Matches: []authz.DecisionMatch{{Effect: "allow", RoleID: "10"}}}, nil
	}
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	f.m.explain = func(_ context.Context, _ *gorm.DB, r core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		return authz.Explanation{Allowed: r.Permission != "project_takeover"}, nil
	}
	f.m.workFacts.RequirementExecutorOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
	r.Type = "requirement.versioned"
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	f.m.workFacts.ActiveEngineerOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
	r.Type = "budget.threshold"
	r.Params = `{"subject_id":"81","threshold_pct":"80"}`
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, allowed)
	r.Params = `{"subject_id":"81","threshold_pct":"100"}`
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	r.Type = "memory.rule_pending"
	f.m.ruleEligible = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil }
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestInterruptedProjectionMergesByActualMachineAndRequiresSourceFacts(t *testing.T) {
	f := newFixture(t)
	machine := guid.ID(92)
	var sourceErr error
	f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
		return execution.NotificationFacts{ProjectID: 81, WorkItemID: 91, InitiatorID: 11, MachineID: machine}, sourceErr
	}
	project := func(id guid.ID) error {
		return f.db.Transaction(func(tx *gorm.DB) error {
			return f.m.projectEventOn(t.Context(), tx, events.RecordedEvent{TenantID: 7, ProjectID: 81, SubjectID: id, Type: "execution.state_changed", Data: []byte(`{"state":"INTERRUPTED"}`)})
		})
	}
	require.NoError(t, project(101))
	require.NoError(t, project(102))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Contains(t, rows[0].Params, "102")
	machine = 93
	require.NoError(t, project(103))
	rows, err = q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	machine = 0
	require.ErrorIs(t, project(104), ErrInvalid)
	sourceErr = execution.ErrNotFound
	require.NoError(t, project(105))
	sourceErr = errors.New("source unavailable")
	require.ErrorIs(t, project(106), sourceErr)
	f.m.executionFacts = nil
	require.ErrorIs(t, project(107), ErrUnavailable)
}

func TestExecutionAudienceDoesNotConvertMissingOrFailedSourcesToRecipients(t *testing.T) {
	f := newFixture(t)
	r := &model.Notification{TenantID: 7, ProjectID: 81, Type: "execution.interrupted", Params: `{"subject_id":"91"}`}
	sentinel := errors.New("source query failed")
	for _, sourceErr := range []error{execution.ErrNotFound, sentinel} {
		f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
			return execution.NotificationFacts{}, sourceErr
		}
		allowed, err := f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
		require.False(t, allowed)
		if errors.Is(sourceErr, execution.ErrNotFound) {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, sentinel)
		}
	}
	f.m.executionFacts = nil
	_, err := f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestEscalationAndIncidentRequireRealSource(t *testing.T) {
	f := newFixture(t)
	trigger := events.RecordedEvent{ID: 51, TenantID: 7, ProjectID: 81, SubjectID: 91, Type: "work_item.escalated", Data: []byte(`{"trigger":"REWORK_LIMIT"}`)}
	incident := events.RecordedEvent{ID: 52, TenantID: 7, ProjectID: 81, SubjectID: 101, Type: "governance.incident_changed", Data: []byte(`{"state":"OPEN","rule_kind":"REWORK_LIMIT","source_event_id":"51","subject_id":"91"}`)}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, trigger) }), ErrUnavailable)
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, incident) }), ErrUnavailable)
	f.m.incidentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.IncidentNotificationFact, bool, error) {
		return governance.IncidentNotificationFact{}, false, nil
	}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, incident) }))
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Empty(t, rows)

	malformed := events.RecordedEvent{TenantID: 7, ProjectID: 81, SubjectID: 91, ActorID: 11, Type: "work_item.assigned", Data: []byte(`{"assignment_id":"9"}`)}
	require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, malformed) }), ErrInvalid)
}

func TestBusinessAudiencesExcludeReadersAndRecheckCurrentRoles(t *testing.T) {
	f := newFixture(t)
	f.m.incidentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.IncidentNotificationFact, bool, error) {
		return governance.IncidentNotificationFact{ProjectID: 81, State: "OPEN"}, true, nil
	}
	r := &model.Notification{TenantID: 7, ProjectID: 81, RecipientPrincipalID: 11, Permission: "work_item_read", Params: `{"subject_id":"91"}`}
	role := "10"
	member := true
	owner := false
	f.m.explain = func(_ context.Context, _ *gorm.DB, request core.AuthorizationExplanationRequest) (authz.Explanation, error) {
		require.NotEqual(t, "takeover", request.Permission, "owner checks must use the registered canonical action")
		if request.Permission == "project_takeover" {
			return authz.Explanation{Allowed: owner}, nil
		}
		matches := []authz.DecisionMatch{{Effect: "allow", RoleID: role, SourceType: "tenant_role", ScopeID: "7"}}
		if member {
			matches = append(matches, authz.DecisionMatch{Effect: "allow", RoleID: role, SourceType: "entity_binding", ScopeID: "81"})
		}
		return authz.Explanation{Allowed: true, Matches: matches}, nil
	}
	for _, typ := range []string{"requirement.versioned", "project.stage_changed", "budget.threshold", "governance.escalation_triggered", "execution.cost_paused"} {
		r.Type = typ
		allowed, err := f.m.visibleOn(t.Context(), f.db, r, 11)
		require.NoError(t, err)
		require.True(t, allowed, typ)
		role = "99"
		member = false
		allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
		require.NoError(t, err)
		require.False(t, allowed, typ)
		role = "10"
		member = true
	}
	r.Type = "budget.threshold"
	role = "99"
	owner = true
	allowed, err := f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	f.m.workFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.WorkNotificationFacts, bool, error) {
		return delivery.WorkNotificationFacts{ProjectID: 81, ExecutorID: 12, AssignerID: 11}, true, nil
	}
	for _, typ := range []string{"work_item.rework", "work_item.accepted"} {
		r.Type = typ
		allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
		require.NoError(t, err)
		require.False(t, allowed)
	}
	f.m.workFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.WorkNotificationFacts, bool, error) {
		return delivery.WorkNotificationFacts{ProjectID: 81, AssignerID: 11}, true, nil
	}
	r.Type = "work_item.accepted"
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.True(t, allowed)
	f.m.workFacts.ReviewEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil }
	r.Type = "work_item.review_ready"
	allowed, err = f.m.visibleOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestBusinessAudiencesFailClosedWhenSourceFactsAreUnavailable(t *testing.T) {
	f := newFixture(t)
	r := &model.Notification{TenantID: 7, ProjectID: 81, RecipientPrincipalID: 11, Permission: "work_item_read", Params: `{"subject_id":"91"}`}
	for _, typ := range []string{"work_item.review_ready", "work_item.rework", "work_item.accepted"} {
		r.Type = typ
		for _, params := range []string{`bad`, `{}`} {
			r.Params = params
			_, err := f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
			require.Error(t, err)
		}
	}
	r.Params = `{"subject_id":"91"}`
	f.m.workFacts = delivery.WorkNotifications{}
	for _, typ := range []string{"work_item.review_ready", "work_item.rework"} {
		r.Type = typ
		_, err := f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
		require.ErrorIs(t, err, ErrUnavailable)
	}
	r.Type = "work_item.rework"
	f.m.workFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.WorkNotificationFacts, bool, error) {
		return delivery.WorkNotificationFacts{}, false, nil
	}
	allowed, err := f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, allowed)
	r.Type = "requirement.versioned"
	f.m.workFacts.RequirementExecutorOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, nil }
	f.m.roles.RoleIDOn = nil
	_, err = f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
	require.ErrorIs(t, err, ErrUnavailable)
	f.m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) {
		return 0, governance.ErrRoleNotSeeded
	}
	allowed, err = f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
	require.NoError(t, err)
	require.False(t, allowed)
	sentinel := errors.New("role lookup failed")
	f.m.roles.RoleIDOn = func(context.Context, *gorm.DB, guid.ID, string) (guid.ID, error) { return 0, sentinel }
	_, err = f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
	require.ErrorIs(t, err, sentinel)
	f.m.explain = nil
	for _, typ := range []string{"project.stage_changed", "budget.threshold"} {
		r.Type = typ
		_, err = f.m.sourceAudienceOn(t.Context(), f.db, r, 11)
		require.ErrorIs(t, err, ErrUnavailable)
	}
}

func TestIncidentProjectionUsesPersistedOpenStateAndCurrentLead(t *testing.T) {
	f := newFixture(t)
	state := "OPEN"
	f.m.incidentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.IncidentNotificationFact, bool, error) {
		return governance.IncidentNotificationFact{ProjectID: 81, State: state}, true, nil
	}
	e := events.RecordedEvent{ID: 99, TenantID: 7, ProjectID: 81, SubjectID: 101, Type: "governance.incident_changed", Data: []byte(`{"state":"OPEN","rule_kind":"REWORK_LIMIT","note":"private"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	require.NotContains(t, rows[0].Params, "private")
	require.Contains(t, rows[0].LinkPath, "/governance/incidents?incident_id=101")
	state = "ACKNOWLEDGED"
	yes, err := f.m.sourceAudienceOn(t.Context(), f.db, rows[0], rows[0].RecipientPrincipalID)
	require.NoError(t, err)
	require.False(t, yes)
}
