package notification

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/execution"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/jobs"
	"7link.com/platform/internal/notification/model"
	"7link.com/platform/internal/notification/query"
	"7link.com/platform/pkg/extension/authz"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMachineAudienceRejectsMalformedAndUnverifiedExecution(t *testing.T) {
	for _, mode := range []string{"missing", "params", "id", "source", "empty", "execution_missing", "execution", "work", "foreign", "actor", "readable"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			fact := fleet.MachineNoticeFact{MachineID: 81, SessionID: 91, Offline: true, OfflineAt: f.m.now().Add(-time.Hour), Executions: []guid.ID{92}}
			f.m.machineNoticeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.MachineNoticeFact, bool, error) {
				if mode == "source" {
					return fact, false, ErrUnavailable
				}
				return fact, true, nil
			}
			f.m.tenantPermission = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
				return authz.DataConstraint{}, nil
			}
			f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
				if mode == "execution" {
					return execution.NotificationFacts{}, ErrUnavailable
				}
				actor := guid.ID(11)
				if mode == "actor" {
					actor = 12
				}
				return execution.NotificationFacts{ProjectID: 81, WorkItemID: 90, InitiatorID: actor}, nil
			}
			f.m.decisionFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (delivery.DecisionNotificationFact, bool, error) {
				if mode == "work" {
					return delivery.DecisionNotificationFact{}, false, ErrUnavailable
				}
				project := guid.ID(81)
				if mode == "foreign" {
					project = 82
				}
				return delivery.DecisionNotificationFact{ProjectID: project}, true, nil
			}
			f.m.decisionFacts.WorkReadableOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return false, ErrUnavailable }
			row := &model.Notification{TenantID: 7, Type: "machine.offline", Params: `{"subject_id":"91"}`}
			switch mode {
			case "missing":
				f.m.machineNoticeFacts = nil
			case "params":
				row.Params = "[]"
			case "id":
				row.Params = `{"subject_id":"bad"}`
			case "empty":
				fact.Executions = nil
			case "execution_missing":
				f.m.executionFacts = nil
			}
			yes, err := f.m.machineAudienceOn(t.Context(), f.db, row, 11)
			require.False(t, yes)
			if mode == "empty" || mode == "foreign" || mode == "actor" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestMachineOfflineJobRejectsUnavailableCurrentEpisode(t *testing.T) {
	for _, mode := range []string{"payload", "database", "missing", "source", "visibility", "notify"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.machineNoticeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.MachineNoticeFact, bool, error) {
				if mode == "source" {
					return fleet.MachineNoticeFact{}, false, ErrUnavailable
				}
				return fleet.MachineNoticeFact{MachineID: 81, SessionID: 91, Offline: true, OfflineAt: f.m.now().Add(-time.Hour)}, true, nil
			}
			job := jobs.Job{TenantID: 7, Payload: []byte(`{"machine_id":"81","session_id":"91"}`)}
			switch mode {
			case "payload":
				job.Payload = []byte(`{}`)
			case "database":
				f.m.runtime = nil
			case "missing":
				f.m.machineNoticeFacts = nil
			case "visibility":
				f.err = ErrUnavailable
			case "notify":
				f.ids.err = ErrUnavailable
			}
			require.Error(t, f.m.machineOfflineJob(t.Context(), job))
			n, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestMachineOutageEmailsWaitCombineThrottleAndThreadRealRecovery(t *testing.T) {
	f := newFixture(t)
	c, messages := smtpFixture(t, 250)
	enableEmail(t, f, c)
	f.m.principal.ActiveHumanByID = func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id, Email: "reader@example.test"}, true, nil
	}
	start, now := f.m.now(), f.m.now()
	f.m.now = func() time.Time { return now }
	facts := map[guid.ID]fleet.MachineNoticeFact{
		91: {MachineID: 81, SessionID: 91, OfflineAt: start, Offline: true},
		92: {MachineID: 82, SessionID: 92, OfflineAt: start.Add(6 * time.Minute), Offline: true},
	}
	f.m.machineNoticeFacts = func(_ context.Context, _ *gorm.DB, _ guid.ID, machine, session guid.ID) (fleet.MachineNoticeFact, bool, error) {
		fact, found := facts[session]
		return fact, found && (machine == 0 || machine == fact.MachineID), nil
	}
	for _, session := range []guid.ID{91, 92} {
		now = facts[session].OfflineAt.Add(61 * time.Second)
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.notifyOfflineMachineOn(t.Context(), tx, 7, facts[session]) }))
	}
	q := query.BindDeliveries(f.db, f.m.names)
	rows, err := q.WithContext(t.Context()).Where(q.RecipientPrincipalID.Eq(guid.ID(11))).Order(q.ID).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2, "separate five-minute inbox groups")
	require.Error(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[0])), "email cannot send before fifteen minutes")
	require.Empty(t, messages())
	now = start.Add(23 * time.Minute)
	require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[0])))
	require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[1])))
	require.Len(t, messages(), 1, "due outage groups combine into one real SMTP request")
	require.Contains(t, messages()[0], "81")
	require.Contains(t, messages()[0], "82")
	rows, err = q.WithContext(t.Context()).Where(q.RecipientPrincipalID.Eq(guid.ID(11))).Order(q.ID).Find()
	require.NoError(t, err)
	require.Equal(t, rows[0].MessageID, rows[1].MessageID, "all actual digest constituents retain the emitted thread ID")
	firstMessage := rows[0].MessageID
	third := fleet.MachineNoticeFact{MachineID: 83, SessionID: 93, OfflineAt: start.Add(30 * time.Minute), Offline: true}
	facts[93], now = third, start.Add(32*time.Minute)
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.notifyOfflineMachineOn(t.Context(), tx, 7, third) }))
	rows, err = q.WithContext(t.Context()).Where(q.RecipientPrincipalID.Eq(guid.ID(11)), q.State.Eq("PENDING")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	now = start.Add(47 * time.Minute)
	require.Error(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[0])), "one outage email per recipient per hour")
	require.Len(t, messages(), 1)
	now = start.Add(84 * time.Minute)
	require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[0])))
	require.Len(t, messages(), 2, "persistent outage is deferred, not discarded")
	old := facts[91]
	old.Offline, old.Recovered = false, true
	facts[91] = old
	e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 81, SubjectKind: "machine", Type: "machine.state_changed"}
	require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectMachineOn(t.Context(), tx, e, "ONLINE") }))
	rows, err = q.WithContext(t.Context()).Where(q.RecipientPrincipalID.Eq(guid.ID(11)), q.State.Eq("PENDING")).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NoError(t, f.m.deliverJob(t.Context(), deliveryJob(t, rows[0])))
	require.Len(t, messages(), 3)
	require.Contains(t, messages()[2], "In-Reply-To: "+firstMessage)
}

func TestMachineAudienceRequiresCurrentAffectedWorkReadOrCurrentAgentOwner(t *testing.T) {
	f := newFixture(t)
	f.allow = false
	owners := []guid.ID{}
	f.m.machineNoticeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.MachineNoticeFact, bool, error) {
		return fleet.MachineNoticeFact{MachineID: 81, SessionID: 91, OfflineAt: f.m.now().Add(-time.Minute - time.Second), Offline: true, Owners: owners, Executions: []guid.ID{71}}, true, nil
	}
	f.m.executionFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID) (execution.NotificationFacts, error) {
		return execution.NotificationFacts{ProjectID: 9, WorkItemID: 61, InitiatorID: 11}, nil
	}
	f.m.decisionFacts.FactsOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, string) (delivery.DecisionNotificationFact, bool, error) {
		return delivery.DecisionNotificationFact{ProjectID: 9, WorkItemID: 61, AccountableID: 12}, true, nil
	}
	readable := true
	f.m.decisionFacts.WorkReadableOn = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (bool, error) { return readable, nil }
	row := &model.Notification{TenantID: 7, Type: "machine.offline", Params: `{"subject_id":"91"}`}
	for _, actor := range []guid.ID{11, 12} {
		yes, err := f.m.machineAudienceOn(t.Context(), f.db, row, actor)
		require.NoError(t, err)
		require.True(t, yes, "real initiator and accountable with current read")
	}
	readable = false
	yes, err := f.m.machineAudienceOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.False(t, yes, "current work read revocation wins")
	owners = []guid.ID{12}
	yes, err = f.m.machineAudienceOn(t.Context(), f.db, row, 12)
	require.NoError(t, err)
	require.True(t, yes, "currently bound Agent owner is a separate actual relationship")
}

func TestMachineOfflineEpisodeThresholdGroupingReplayAndRecovery(t *testing.T) {
	f := newFixture(t)
	start := f.m.now()
	now := start
	f.m.now = func() time.Time { return now }
	facts := map[guid.ID]fleet.MachineNoticeFact{91: {MachineID: 81, SessionID: 91, OfflineAt: start, Offline: true}, 92: {MachineID: 82, SessionID: 92, OfflineAt: start.Add(time.Minute), Offline: true}}
	f.m.machineNoticeFacts = func(_ context.Context, _ *gorm.DB, tenant, machine, session guid.ID) (fleet.MachineNoticeFact, bool, error) {
		require.EqualValues(t, 7, tenant)
		if session == 0 {
			session = machine + 10
		}
		fact, found := facts[session]
		return fact, found && (machine == 0 || fact.MachineID == machine), nil
	}
	run := func(machine, session guid.ID) error {
		raw, err := json.Marshal(machineOfflineInput{machine, session})
		require.NoError(t, err)
		return f.m.machineOfflineJob(t.Context(), jobs.Job{TenantID: 7, Payload: raw})
	}
	require.Error(t, run(81, 91), "retry before sixty seconds")
	q := query.BindNotifications(f.db, f.m.names)
	n, err := q.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, n)
	now = start.Add(61 * time.Second)
	for range 2 {
		require.NoError(t, run(81, 91))
	}
	now = start.Add(121 * time.Second)
	require.NoError(t, run(82, 92))
	rows, err := q.WithContext(t.Context()).Where(q.Type.Eq("machine.offline"), q.RecipientPrincipalID.Eq(guid.ID(11))).Find()
	require.NoError(t, err)
	require.Len(t, rows, 1, "two machines within five minutes share one unread notice")
	require.Equal(t, 2, rows[0].MergeCount, "replay does not count again")
	old := facts[91]
	old.Offline, old.Recovered = false, true
	facts[91] = old
	yes, err := f.m.visibleOn(t.Context(), f.db, rows[0], 11)
	require.NoError(t, err)
	require.True(t, yes, "other offline source survives recovery of first")
	require.Equal(t, 1, rows[0].MergeCount)
	e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectID: 81, SubjectKind: "machine", Type: "machine.state_changed", Data: []byte(`{"state":"ONLINE"}`)}
	for range 2 {
		require.NoError(t, f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectEventOn(t.Context(), tx, e) }))
	}
	recovered, err := q.WithContext(t.Context()).Where(q.Type.Eq("machine.recovered")).Find()
	require.NoError(t, err)
	require.Len(t, recovered, 2, "only actual original recipients, once each")
	for _, row := range recovered {
		require.Equal(t, 1, row.MergeCount)
	}
}

func TestMachineOfflineScanFindsAlreadyOfflineEpisodeAndKeepsReceipts(t *testing.T) {
	f := newFixture(t)
	f.m.machineOfflineMachines = func(_ context.Context, _ *gorm.DB, tenant, after guid.ID, limit int) ([]guid.ID, error) {
		require.EqualValues(t, 7, tenant)
		require.Zero(t, after)
		require.Equal(t, 100, limit)
		return []guid.ID{81}, nil
	}
	f.m.machineNoticeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.MachineNoticeFact, bool, error) {
		return fleet.MachineNoticeFact{MachineID: 81, SessionID: 91, Offline: true, OfflineAt: f.m.now().Add(-2 * time.Minute)}, true, nil
	}
	for range 2 {
		require.NoError(t, f.m.machineOfflineScanJob(t.Context(), jobs.Job{TenantID: 7}))
	}
	rows, err := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Find()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, 1, row.MergeCount)
	}
	schedules := f.m.JobSchedules()
	require.Equal(t, "notification.machine_offline_scan", schedules[2].Name)
	require.Equal(t, 15*time.Second, schedules[2].Interval)
}

func TestMachineEmailDelayAndRecoveryRequireActualSentThread(t *testing.T) {
	f := newFixture(t)
	at := f.m.now()
	runAt := time.Time{}
	msg := EmailMessage{Params: map[string]string{"offline_at": at.Format(time.RFC3339Nano)}}
	yes, err := f.m.machineEmailOn(t.Context(), f.db, &model.Notification{Type: "machine.offline"}, &msg, &runAt)
	require.NoError(t, err)
	require.True(t, yes)
	require.Equal(t, at.Add(15*time.Minute+time.Millisecond), runAt)
	msg.Params = map[string]string{"original_notification_id": "91"}
	row := &model.Notification{TenantID: 7, RecipientPrincipalID: 11, Type: "machine.recovered"}
	yes, err = f.m.machineEmailOn(t.Context(), f.db, row, &msg, &runAt)
	require.NoError(t, err)
	require.False(t, yes)
	notice, recipient := guid.ID(91), guid.ID(11)
	d := &model.Delivery{ID: 1001, TenantID: 7, NotificationID: &notice, RecipientPrincipalID: &recipient, Channel: string(Email), State: "SENT", MessageID: "<1001@example.test>", CreatedAt: at, UpdatedAt: at}
	require.NoError(t, query.BindDeliveries(f.db, f.m.names).WithContext(t.Context()).Create(d))
	yes, err = f.m.machineEmailOn(t.Context(), f.db, row, &msg, &runAt)
	require.NoError(t, err)
	require.True(t, yes)
	require.Equal(t, d.MessageID, msg.InReplyTo)
	require.Equal(t, []string{d.MessageID}, msg.References)
}

func TestMachineOfflineEventSchedulesActualSessionAndRejectsSourceFailures(t *testing.T) {
	for _, mode := range []string{"success", "source", "missing", "queue", "admission", "queue_failure", "invalid", "irrelevant"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			at := f.m.now()
			f.m.machineNoticeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.MachineNoticeFact, bool, error) {
				if mode == "source" {
					return fleet.MachineNoticeFact{}, false, ErrUnavailable
				}
				return fleet.MachineNoticeFact{MachineID: 81, SessionID: 91, OfflineAt: at, Offline: true}, true, nil
			}
			e := events.RecordedEvent{ID: 1001, TenantID: 7, SubjectKind: "machine", SubjectID: 81}
			state := "OFFLINE"
			switch mode {
			case "missing":
				f.m.machineNoticeFacts = nil
			case "queue":
				f.m.queue = nil
			case "admission":
				f.m.admission = func(context.Context, *gorm.DB, guid.ID) error { return ErrUnavailable }
			case "queue_failure":
				f.m.queue = &fixtureQueue{err: ErrUnavailable}
			case "invalid":
				e.SubjectID = 0
			case "irrelevant":
				state = "STARTING"
			}
			err := f.db.Transaction(func(tx *gorm.DB) error { return f.m.projectMachineOn(t.Context(), tx, e, state) })
			var count int64
			require.NoError(t, f.db.Table("test_notification_jobs").Count(&count).Error)
			if mode == "success" {
				require.NoError(t, err)
				require.EqualValues(t, 1, count)
				queue := f.m.queue.(*fixtureQueue)
				require.Len(t, queue.requests, 1)
				require.Equal(t, at.Add(time.Minute+time.Millisecond), queue.requests[0].RunAt)
			} else if mode == "irrelevant" {
				require.NoError(t, err)
				require.Zero(t, count)
			} else {
				require.Error(t, err)
				require.Zero(t, count)
			}
		})
	}
}
func TestOfflineScannerCurrentSourceFailuresLeaveInboxEmpty(t *testing.T) {
	for _, mode := range []string{"source", "order", "fact", "missing_candidates", "candidates", "candidate_order", "database", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.m.machineOfflineMachines = func(context.Context, *gorm.DB, guid.ID, guid.ID, int) ([]guid.ID, error) {
				if mode == "source" {
					return nil, ErrUnavailable
				}
				if mode == "order" {
					return []guid.ID{0}, nil
				}
				return []guid.ID{81}, nil
			}
			f.m.machineNoticeFacts = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID) (fleet.MachineNoticeFact, bool, error) {
				if mode == "fact" {
					return fleet.MachineNoticeFact{}, false, ErrUnavailable
				}
				return fleet.MachineNoticeFact{MachineID: 81, SessionID: 91, OfflineAt: f.m.now().Add(-2 * time.Minute), Offline: true}, true, nil
			}
			f.m.candidates = func(context.Context, *gorm.DB, guid.ID, int) ([]guid.ID, error) {
				if mode == "candidates" {
					return nil, ErrUnavailable
				}
				if mode == "candidate_order" {
					return []guid.ID{0}, nil
				}
				return nil, nil
			}
			if mode == "missing_candidates" {
				f.m.candidates = nil
			}
			if mode == "missing" {
				f.m.machineOfflineMachines = nil
			}
			if mode == "database" {
				f.m.runtime = nil
			}
			require.Error(t, f.m.machineOfflineScanJob(t.Context(), jobs.Job{TenantID: 7}))
			n, e := query.BindNotifications(f.db, f.m.names).WithContext(t.Context()).Count()
			require.NoError(t, e)
			require.Zero(t, n)
		})
	}
	f := newFixture(t)
	require.Error(t, f.m.machineOfflineScanJob(t.Context(), jobs.Job{}))
}
