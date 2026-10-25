package notification

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/governance"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/realtime"
	"7link.com/platform/internal/notification/query"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectedAssignmentRequirementAndBudgetRetainExactIdentity(t *testing.T) {
	for _, typ := range []string{"work_item.assigned", "requirement.versioned", "work_item.state_changed", "budget.threshold_reached"} {
		t.Run(typ, func(t *testing.T) {
			f := newFixture(t)
			f.m.workFacts.ReviewEligibleOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return true, nil }
			data := []byte(`{"assignee":{"principal_id":"11"},"assignment_id":"92","version_no":2,"to":"NEEDS_HUMAN_REVIEW","threshold_pct":100,"cap_version":3}`)
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 91, SubjectKind: "work_item", Type: typ, Data: data}
			for range 2 {
				require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
			}
			rows, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Find()
			require.NoError(t, err)
			require.NotEmpty(t, rows)
			for _, row := range rows {
				expectedMerge := 1
				if typ == "work_item.state_changed" {
					expectedMerge = 2
				}
				require.Equal(t, expectedMerge, row.MergeCount)
				require.Equal(t, guid.ID(81), row.ProjectID)
				if typ == "work_item.assigned" {
					require.NotNil(t, row.DedupeKey)
					require.Equal(t, "assigned:92", *row.DedupeKey)
				}
				if typ == "budget.threshold_reached" {
					require.Equal(t, string(Urgent), row.Severity)
					require.Contains(t, row.Params, `"threshold_pct":"100"`)
				}
			}
		})
	}
}

func TestProjectEventRejectsInvalidAndUnavailableCurrentFacts(t *testing.T) {
	f := newFixture(t)
	require.ErrorIs(t, f.m.projectEventOn(t.Context(), f.db, events.RecordedEvent{}), ErrInvalid)
	for _, mode := range []string{"stage_payload", "stage_missing", "stage_source", "members", "mentions", "deadline", "delegation", "incident", "attention", "attention_execution", "stage_health"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			e := events.RecordedEvent{ID: 1001, TenantID: 7, ProjectID: 81, SubjectID: 81, Data: []byte(`{}`)}
			switch mode {
			case "stage_payload":
				e.Type = "project.stage_ready"
			case "stage_missing":
				e.Type = "project.stage_ready"
				e.Data = []byte(`{"target":"BUILD"}`)
			case "stage_source":
				e.Type = "project.stage_ready"
				e.Data = []byte(`{"target":"BUILD"}`)
				f.m.decisionFacts.StageSuggestionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (string, error) { return "", ErrUnavailable }
			case "members":
				e.Type = "project.created"
				e.Data = []byte(`{"member_ids":"invalid"}`)
			case "mentions":
				e.Type = "collab.message_posted"
				f.m.mentions.RecipientsOn = nil
			case "deadline":
				e.Type = "project.stage_changed"
				f.m.decisionFacts.DeadlineOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (delivery.DeadlineNotificationFact, bool, error) {
					return delivery.DeadlineNotificationFact{}, false, ErrUnavailable
				}
			case "delegation":
				e.Type = "delegation.granted"
				f.m.delegationFacts = nil
			case "incident":
				e.Type = "governance.incident_changed"
				f.m.incidentFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (governance.IncidentNotificationFact, bool, error) {
					return governance.IncidentNotificationFact{}, false, ErrUnavailable
				}
			case "stage_health":
				e.Type = "work_item.created"
				f.m.decisionFacts.PublishStageReadyOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
				f.m.queue = nil
			case "attention_execution":
				e.Type = "execution.state_changed"
				e.Data = []byte(`{"state":"INTERRUPTED"}`)
				f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
					return execution.NotificationFacts{}, ErrUnavailable
				}
				f.m.decisionFacts.AttentionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ExecutionAttentionFact, bool, error) {
					return delivery.ExecutionAttentionFact{}, false, ErrUnavailable
				}
			case "attention":
				e.Type = "work_item.state_changed"
				e.Data = []byte(`{"to":"EXECUTION_FAILED"}`)
				f.m.decisionFacts.AttentionOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (delivery.ExecutionAttentionFact, bool, error) {
					return delivery.ExecutionAttentionFact{}, false, ErrUnavailable
				}
			}
			require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestMachineRevocationRetainsRouteAndUsesCanonicalOwnerPermission(t *testing.T) {
	r, ok := eventRoute(events.RecordedEvent{Type: "machine.state_changed"}, map[string]json.RawMessage{"state": json.RawMessage(`"REVOKED"`)})
	require.True(t, ok)
	require.Equal(t, "machine.revoked", r.typ)
	require.Equal(t, "project_takeover", r.permission)
	require.True(t, r.holders)
}

func TestGatewayRotationProjectionRequiresCurrentSourceAndAuthority(t *testing.T) {
	f := newFixture(t)
	source := true
	f.m.gatewayRotations = func(_ context.Context, _ *gorm.DB, tenant, key guid.ID) (bool, error) {
		return source && tenant == 7 && key == 91, nil
	}
	e := events.RecordedEvent{ID: 9101, TenantID: 7, SubjectID: 91, SubjectKind: "gateway_key", Type: "gateway.key_rotated", Data: json.RawMessage(`{"old_key_id":"90","new_key_id":"91","config_version":2}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	q := query.BindNotifications(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		require.Equal(t, "gateway.key_rotated", row.Type)
		require.Equal(t, 1, row.MergeCount, "rotation receipt is permanently deduplicated")
		require.Equal(t, "/admin/7/gateway", row.LinkPath)
		require.NotContains(t, row.Params, "old_key_id")
		visible, err := f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.True(t, visible)
		f.allow = false
		visible, err = f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.False(t, visible)
		f.allow = true
		source = false
		visible, err = f.m.visibleOn(t.Context(), f.db, row, row.RecipientPrincipalID)
		require.NoError(t, err)
		require.False(t, visible)
		source = true
	}
}

func projectionHarness(t *testing.T, f *fixture) *events.Module {
	t.Helper()
	e := events.NewModule(f.m.runtime)
	locks := dlock.NewModule(f.m.runtime)
	raw, err := f.db.DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{e.MigrationSource(), locks.MigrationSource()} {
		source.Prefix = "n_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	services := module.NewServices()
	require.NoError(t, locks.ProvideServices(services))
	require.NoError(t, module.Provide[guid.Source](services, f.ids))
	require.NoError(t, services.Seal())
	require.NoError(t, e.InjectServices(services))
	require.NoError(t, e.AddTypes("fleet", events.TypeSpec{Name: "machine.state_changed", SchemaVersion: 1, SubjectKind: "machine", Realtime: events.RealtimeNone}))
	require.NoError(t, e.AddSubscribers(Name, f.m.subscriber()))
	require.NoError(t, e.Initialize(t.Context()))
	return e
}
func publishMachine(t *testing.T, f *fixture, e *events.Module, id guid.ID) {
	t.Helper()
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error {
		_, err := e.Publish(t.Context(), tx, events.Event{TenantID: 7, Type: "machine.state_changed", Actor: events.Actor{Kind: "SYSTEM"}, Subject: events.Subject{Kind: "machine", ID: id}, Data: map[string]any{"state": "REVOKED"}})
		return err
	}))
	_, err := e.Sequence(t.Context())
	require.NoError(t, err)
}
func TestProjectionSharedDispatcherReplayAndRollback(t *testing.T) {
	f := newFixture(t)
	e := projectionHarness(t, f)
	publishMachine(t, f, e, 91)
	for range 2 {
		_, err := e.DispatchPartition(t.Context(), Name+".projector", 0)
		require.NoError(t, err)
	}
	q := query.BindNotifications(f.db, f.m.names)
	count, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	publishMachine(t, f, e, 92)
	f.err = errors.New("current IAM unavailable")
	_, err = e.DispatchPartition(t.Context(), Name+".projector", 0)
	require.NoError(t, err) // Shared dispatcher parks the failing event after bounded retries.
	count, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	f.err = nil
	publishMachine(t, f, e, 93)
	_, err = e.DispatchPartition(t.Context(), Name+".projector", 0)
	require.NoError(t, err)
	count, err = q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 4, count)
	require.False(t, f.db.Migrator().HasTable("n_notification_receipts"))
}

func TestEventRoutes(t *testing.T) {
	for _, test := range []struct {
		event, data string
		severity    Severity
	}{
		{"machine.credential_changed", `{"state":"FAILED"}`, Action},
		{"execution.state_changed", `{"state":"COST_PAUSED"}`, Action},
		{"knowledge.entry_written", `{"state":"PENDING_REVIEW"}`, Info},
		{"budget.threshold_reached", `{"threshold_pct":80}`, Action},
		{"budget.threshold_reached", `{"threshold_pct":100}`, Urgent},
	} {
		var data map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(test.data), &data))
		r, ok := eventRoute(events.RecordedEvent{Type: test.event}, data)
		require.True(t, ok)
		require.Equal(t, test.severity, r.severity, test.event)
	}
	for _, test := range []struct{ event, stateKey, state string }{
		{"machine.credential_changed", "state", "FAILED"}, {"machine.state_changed", "state", "REVOKED"}, {"work_item.assigned", "", ""}, {"work_item.state_changed", "to", "NEEDS_HUMAN_REVIEW"}, {"work_item.state_changed", "to", "REWORK"}, {"work_item.state_changed", "to", "ACCEPTED"}, {"execution.state_changed", "state", "INTERRUPTED"}, {"execution.state_changed", "state", "COST_PAUSED"}, {"project.stage_changed", "", ""}, {"requirement.versioned", "", ""}, {"knowledge.entry_written", "state", "PENDING_REVIEW"}, {"budget.threshold_reached", "", ""},
	} {
		t.Run(test.event+test.state, func(t *testing.T) {
			data := map[string]json.RawMessage{}
			data[test.stateKey], _ = json.Marshal(test.state)
			r, ok := eventRoute(events.RecordedEvent{Type: test.event}, data)
			require.True(t, ok)
			require.NotEmpty(t, r.permission)
			require.NotEmpty(t, r.typ)
		})
	}
	_, failed := eventRoute(events.RecordedEvent{Type: "execution.state_changed"}, map[string]json.RawMessage{"state": json.RawMessage(`"FAILED"`)})
	require.False(t, failed, "terminal runtime failure does not prove retry exhaustion")
	_, ok := eventRoute(events.RecordedEvent{Type: "unknown.event"}, nil)
	require.False(t, ok)
	_, ok = eventRoute(events.RecordedEvent{Type: "agents.changed"}, map[string]json.RawMessage{"state": json.RawMessage(`"ERROR"`)})
	require.False(t, ok, "runtime activity uses the delayed observer, never agents.changed")
	for _, raw := range []string{`"42"`, `42`} {
		require.Equal(t, guid.ID(42), rawID(json.RawMessage(raw)))
	}
	require.Zero(t, rawID(json.RawMessage(`"invalid"`)))
}

func TestProjectionRollback(t *testing.T) {
	f := newFixture(t)
	for _, event := range []events.RecordedEvent{{ID: 1, TenantID: 7, Type: "machine.state_changed", Data: json.RawMessage(`invalid`)}, {ID: 2, TenantID: 7, SubjectID: 91, Type: "machine.credential_changed", Data: json.RawMessage(`{"state":"FAILED","rotation_id":"77"}`)}} {
		if event.ID == 2 {
			f.m.rotationRequester = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (guid.ID, error) {
				return 0, errors.New("rotation lookup")
			}
		}
		require.Error(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, event) }))
	}
	r := query.BindNotifications(f.db, f.m.names)
	count, err := r.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestProjectionDependencyFailuresDoNotConsumeReceipt(t *testing.T) {
	for _, failure := range []string{"candidates", "permission", "notify"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			sentinel := errors.New("owner unavailable")
			switch failure {
			case "candidates":
				f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) { return nil, sentinel }
			case "permission":
				f.err = sentinel
			case "notify":
				f.ids.err = sentinel
			}
			e := events.RecordedEvent{ID: 41, TenantID: 7, SubjectID: 91, Type: "machine.state_changed", Data: json.RawMessage(`{"state":"REVOKED"}`)}
			require.ErrorIs(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }), sentinel)
			r := query.BindNotifications(f.db, f.m.names)
			count, err := r.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}

}

func TestProjectionBroadcastsAfterCommitWithoutPersistingNotificationEvents(t *testing.T) {
	f := newFixture(t)
	seen := map[string]bool{}
	f.m.broadcast = func(_ context.Context, topic string, data any) error {
		q := query.BindNotifications(f.db, f.m.names)
		count, err := q.WithContext(t.Context()).Count()
		require.NoError(t, err)
		require.EqualValues(t, 2, count, "broadcast must observe committed recipients")
		message, ok := data.(realtime.Message)
		require.True(t, ok)
		require.Equal(t, "notification.created", message.Type)
		require.Empty(t, message.Payload)
		seen[topic] = true
		return errors.New("hint transport unavailable")
	}
	e := projectionHarness(t, f)
	publishMachine(t, f, e, 91)
	_, err := e.DispatchPartition(t.Context(), Name+".projector", 0)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"tenant/7/inbox/11": true, "tenant/7/inbox/12": true}, seen)
	var count int64
	require.NoError(t, f.db.Table("test_notification_events").Where("type = ?", "notification.created").Count(&count).Error)
	require.Zero(t, count)
}
