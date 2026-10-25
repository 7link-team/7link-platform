package agents

import (
	"7link.com/platform/internal/agents/model"
	"7link.com/platform/internal/agents/query"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"7link.com/platform/internal/collab"
	"7link.com/platform/internal/delivery"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/infra/idempotency"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/extension/authz"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	platformi18n "7link.com/platform/pkg/i18n"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type activeMembers struct{}

func (activeMembers) IsMemberActive(context.Context, guid.ID, guid.ID) (bool, error) {
	return true, nil
}
func (activeMembers) IsMemberActiveOn(_ context.Context, _ *gorm.DB, tenant, actor guid.ID) (bool, error) {
	return tenant == 1 && actor != 9, nil
}
func TestInboxPersistsWhileHostIsStoppedAndResumesAfterStart(t *testing.T) {
	for _, initial := range []string{"DRAFT", "STOPPED", "CRASHED", "RETIRING", "ACTIVE"} {
		t.Run(initial, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			state := initial
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{TenantID: 1, PrincipalID: 7, State: state}, true, nil
			}
			in := InboxReference{SourceEventID: "message:55", Kind: "MESSAGE", SubjectID: 55, StreamKey: "channel:3", StreamSeq: 1}
			for range 2 {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					seq, err := m.appendOn(agentContext(t, 2), tx, 1, 7, in)
					if err != nil {
						return err
					}
					require.EqualValues(t, 1, seq)
					return nil
				}))
			}
			state = "ACTIVE"
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				page, err := m.inboxOn(agentContext(t, 7), tx, 1, 7, 0, 50)
				if err != nil {
					return err
				}
				require.Len(t, page.Items, 1)
				require.Equal(t, in.SubjectID, page.Items[0].SubjectID)
				require.EqualValues(t, 1, page.Head)
				require.Zero(t, page.Ack, "starting the host must not acknowledge work")
				live, err := m.ackOn(agentContext(t, 7), tx, 1, 7, page.Head)
				require.EqualValues(t, 1, live.Ack)
				return err
			}))
		})
	}
}

func TestInboxRejectsRetiredOrUnknownAgentBeforeCreatingStateOrReceipt(t *testing.T) {
	for _, lifecycle := range []string{"RETIRED", "UNKNOWN", ""} {
		t.Run(lifecycle, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
				return fleet.AgentIdentity{TenantID: 1, PrincipalID: 7, State: lifecycle}, true, nil
			}
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, err := m.appendOn(agentContext(t, 2), tx, 1, 7, InboxReference{SourceEventID: "retired-dispatch", Kind: "DISPATCH", SubjectID: 5})
				require.ErrorIs(t, err, ErrForbidden)
				state := query.BindLiveState(tx, m.names)
				count, err := state.WithContext(t.Context()).Count()
				require.NoError(t, err)
				require.Zero(t, count)
				inbox := query.BindInboxEvent(tx, m.names)
				count, err = inbox.WithContext(t.Context()).Count()
				require.NoError(t, err)
				require.Zero(t, count)
				return nil
			}))
		})
	}
}

func agentContext(t *testing.T, actor guid.ID) context.Context {
	t.Helper()
	return authn.WithClaims(t.Context(), &authn.Claims{PrincipalID: actor, TenantID: 1, SubjectType: authn.SubjectTypePrincipal})
}
func agentHarness(t *testing.T) (*Module, *gorm.DB, *module.Services) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "agents.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("k_"), appenv.WithDatabase(router))
	m := NewModule(rt, Config{Config: module.Config{Enabled: true}})
	db := router.Write()
	s := module.NewServices()
	require.NoError(t, module.Provide[delivery.SelectedAgentWork](s, delivery.SelectedAgentWork{}))
	require.NoError(t, module.Provide[collab.AgentDirectReader](s, collab.AgentDirectReader{}))
	ids, err := guid.New(1)
	require.NoError(t, err)
	require.NoError(t, module.Provide[guid.Source](s, ids))
	require.NoError(t, module.Provide[identity.PrincipalStateStore](s, identity.PrincipalStateStore{LockActiveRow: func(_ context.Context, _ *gorm.DB, id guid.ID) (identity.Principal, bool, error) {
		return identity.Principal{ID: id, LoginName: id.String()}, id > 0 && id != 8, nil
	}, HumanByLogin: func(_ context.Context, _ *gorm.DB, login string) (identity.Principal, bool, error) {
		id, e := strconv.ParseInt(login, 10, 64)
		return identity.Principal{ID: guid.ID(id)}, e == nil && id != 7, nil
	}}))
	require.NoError(t, module.Provide[core.ActiveMemberChecker](s, activeMembers{}))
	require.NoError(t, module.Provide[core.EntityConstraintOn](s, core.EntityConstraintOn(func(context.Context, *gorm.DB, guid.ID, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})))
	require.NoError(t, module.Provide[core.TenantConstraintOn](s, core.TenantConstraintOn(func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
		return authz.DataConstraint{AllowAll: true}, nil
	})))
	require.NoError(t, module.Provide[fleet.AgentRuntimeFacts](s, fleet.AgentRuntimeFacts{}))
	require.NoError(t, module.Provide[fleet.AgentDirectory](s, fleet.AgentDirectory{ByPrincipalOn: func(_ context.Context, _ *gorm.DB, tenant, principal guid.ID) (fleet.AgentIdentity, bool, error) {
		return fleet.AgentIdentity{ID: 70, TenantID: tenant, PrincipalID: principal, OwnerPrincipalID: 2, State: "ACTIVE", Name: "Test agent"}, principal == 7, nil
	}}))
	require.NoError(t, db.Exec("CREATE TABLE audit_probe (event_type TEXT NOT NULL)").Error)
	require.NoError(t, module.Provide[audit.Appender](s, func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		require.NotEmpty(t, event.Detail["authority_source"])
		return tx.WithContext(ctx).Exec("INSERT INTO audit_probe VALUES (?)", event.EventType).Error
	}))
	locks := dlock.NewModule(rt)
	require.NoError(t, locks.ProvideServices(s))
	publisher := events.NewModule(rt)
	require.NoError(t, publisher.ProvideServices(s))
	replay := idempotency.NewModule(rt)
	require.NoError(t, replay.ProvideServices(s))
	registry, err := authz.NewRegistry()
	require.NoError(t, err)
	require.NoError(t, module.Provide[*authz.Registrar](s, authz.NewDeclarationOnlyRegistrar(registry)))
	require.NoError(t, module.Provide[collab.ThreadAccess](s, collab.ThreadAccess{CheckOn: func(_ context.Context, _ *gorm.DB, tenant, project, thread, target guid.ID) (int64, error) {
		if project != 3 || thread != 4 || target != 7 {
			return 0, ErrForbidden
		}
		return 10, nil
	}}))
	require.NoError(t, module.Provide[collab.MessageObserverConnector](s, collab.MessageObserverConnector{Bind: func(collab.MessageObserver) error { return nil }}))
	require.NoError(t, module.Provide[authn.ReauthenticationConsumer](s, authn.ReauthenticationConsumer(func(context.Context, *gorm.DB, guid.ID, string, string, string, string) error { return ErrForbidden })))
	require.NoError(t, s.Seal())
	require.NoError(t, m.InjectServices(s))
	require.NoError(t, publisher.InjectServices(s))
	require.NoError(t, replay.InjectServices(s))
	require.NoError(t, publisher.Initialize(t.Context()))
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, m.Initialize(t.Context()))
	require.NoError(t, replay.Initialize(t.Context()))
	raw, err := db.DB()
	require.NoError(t, err)
	for _, source := range []goosex.Source{m.MigrationSource(), locks.MigrationSource(), publisher.MigrationSource(), replay.MigrationSource()} {
		source.Prefix = "k_"
		require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	}
	return m, db, s
}

func TestInboxDedupCursorWakeAndTenantBoundary(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	var seq int64
	in := InboxReference{SourceEventID: "message:55", Kind: "MESSAGE", SubjectID: 55, StreamKey: "channel:3", StreamSeq: 1}
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; seq, e = m.appendOn(ctx, tx, 1, 7, in); return e }))
		require.EqualValues(t, 1, seq)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		page, e := m.inboxOn(ctx, tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.EqualValues(t, 1, page.Head)
		_, e = m.inboxOn(agentContext(t, 9), tx, 1, 7, 0, 50)
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.inboxOn(ctx, tx, 2, 7, 0, 50)
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.inboxOn(agentContext(t, 3), tx, 1, 7, 0, 50)
		require.NoError(t, e) // workspace administrator via explicit policy
		v, e := m.ackOn(ctx, tx, 1, 7, 1)
		require.NoError(t, e)
		require.EqualValues(t, 1, v.Ack)
		v, e = m.ackOn(ctx, tx, 1, 7, 0)
		require.NoError(t, e)
		require.EqualValues(t, 1, v.Ack)
		_, e = m.ackOn(ctx, tx, 1, 7, 2)
		require.ErrorIs(t, e, ErrInvalid)
		_, e = m.ackOn(agentContext(t, 2), tx, 1, 7, 1)
		require.ErrorIs(t, e, ErrForbidden)
		return nil
	}))
	var payloads []string
	require.NoError(t, db.Raw("SELECT data FROM k_domain_events WHERE event_type='agents.wake'").Scan(&payloads).Error)
	require.Len(t, payloads, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(payloads[0]), &payload))
	require.Equal(t, map[string]any{"head": float64(1)}, payload)
}
func TestInboxOutboxFailureRollsBackSequence(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	publisher := m.events
	m.events = badPublisher{}
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		_, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "event:1", Kind: "STATE", SubjectID: 5})
		return e
	}))
	q := query.BindLiveState(db, m.names)
	n, e := q.WithContext(ctx).Count()
	require.NoError(t, e)
	require.Zero(t, n)
	m.events = publisher
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		seq, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "event:1", Kind: "STATE", SubjectID: 5})
		require.EqualValues(t, 1, seq)
		return e
	}))
}

type badPublisher struct{}

func (badPublisher) Publish(context.Context, *gorm.DB, events.Event) (guid.ID, error) {
	return 0, errors.New("outbox unavailable")
}
func TestLifecycleRequiresTrustedEpochAndResetCompletion(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, e := m.liveOn(ctx, tx, 1, 7)
		require.NoError(t, e)
		require.Equal(t, "OFFLINE", v.Activity)
		require.Nil(t, v.UpdatedAt)
		_, e = m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "SESSION", Reason: "Clear the stale session"})
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
	require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error { return nil }}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, m.reportOn(ctx, tx, 1, 7, 1, "WORKING"))
		require.ErrorIs(t, m.reportOn(ctx, tx, 1, 7, 2, "ONLINE"), ErrConflict)
		_, e := m.resetOn(agentContext(t, 7), tx, 1, 7, ResetInput{Kind: "FULL", Reason: "Reset all runtime state", ConfirmName: "Test agent", ReauthProof: "proof"})
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "FULL", Reason: "Reset all runtime state", ConfirmName: "wrong", ReauthProof: "proof"})
		require.ErrorIs(t, e, ErrInvalid)
		r, e := m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "SESSION", Reason: "Clear the stale session"})
		require.NoError(t, e)
		require.Equal(t, "REQUESTED", r.State)
		_, e = m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "SESSION", Reason: "Clear the stale session"})
		require.ErrorIs(t, e, ErrConflict)
		v, e := m.liveOn(ctx, tx, 1, 7)
		require.NoError(t, e)
		require.Equal(t, 1, v.SessionEpoch)
		require.NoError(t, m.completeResetOn(ctx, tx, 1, 7, r.ID, "DONE"))
		require.NoError(t, m.completeResetOn(ctx, tx, 1, 7, r.ID, "DONE"))
		v, e = m.liveOn(ctx, tx, 1, 7)
		require.NoError(t, e)
		require.Equal(t, 2, v.SessionEpoch)
		require.Equal(t, 1, v.WorkspaceEpoch)
		return nil
	}))
}
func TestReminderCoalescesMissedFiresAndDoesNotPostMessages(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return start }
	in := ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Daily check", DueAt: start.Add(time.Minute), IntervalMinutes: 15, MaxFires: 20, Checks: "Read current context", BudgetMinor: 100, Escalation: "Stop when budget exceeded", OutputFormat: "Draft summary and unverified facts"}
	var reminder ReminderView
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { var e error; reminder, e = m.reminderOn(ctx, tx, 1, 7, in); return e }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, reminder.ID, start.Add(46*time.Minute)) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.fireReminderOn(ctx, tx, 1, reminder.ID, start.Add(46*time.Minute)) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, _, e := m.remindersOn(ctx, tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Len(t, rows, 1)
		require.Equal(t, 4, rows[0].FireCount)
		require.Equal(t, start.Add(61*time.Minute), rows[0].DueAt)
		page, e := m.inboxOn(ctx, tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.Len(t, page.Items, 1)
		require.Equal(t, "REMINDER", page.Items[0].Kind)
		_, e = m.changeReminderOn(ctx, tx, 1, 7, reminder.ID, 1, "PAUSED")
		require.ErrorIs(t, e, ErrConflict)
		_, e = m.changeReminderOn(ctx, tx, 1, 7, reminder.ID, 2, "PAUSED")
		require.NoError(t, e)
		return nil
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		q := query.BindReminder(tx, m.names)
		r, e := q.WithContext(ctx).First()
		require.NoError(t, e)
		require.Equal(t, "PAUSED", r.State)
		return nil
	}))
}
func TestProtocolValidationAndCurrentPolicy(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 7)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for _, in := range []InboxReference{{SourceEventID: "a", Kind: "BODY", SubjectID: 4}, {SourceEventID: "a", Kind: "STATE", SubjectID: 4, StreamKey: "private:4"}, {SourceEventID: "a", Kind: "STATE", SubjectID: 4, StreamKey: "thread:x"}} {
			_, e := m.appendOn(ctx, tx, 1, 7, in)
			require.ErrorIs(t, e, ErrInvalid)
		}
		for _, actor := range []guid.ID{0, 8, 9} {
			_, e := m.liveOn(agentContext(t, actor), tx, 1, 7)
			require.ErrorIs(t, e, ErrForbidden)
		}
		m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, nil
		}
		_, e := m.liveOn(agentContext(t, 3), tx, 1, 7)
		require.ErrorIs(t, e, ErrForbidden)
		_, e = m.liveOn(agentContext(t, 2), tx, 1, 7)
		require.NoError(t, e) // owner relation
		return nil
	}))
	_, e := m.appendOn(ctx, db, 1, 7, InboxReference{SourceEventID: "a", Kind: "STATE", SubjectID: 4})
	require.ErrorIs(t, e, ErrInvalid)
	// The database itself rejects impossible acknowledgements.
	require.Error(t, db.Table(m.names.LiveState.String()).Create(&model.LiveState{AgentPrincipalID: 70, TenantID: 1, Head: 1, Ack: 2, Activity: "OFFLINE", SessionEpoch: 1, WorkspaceEpoch: 1}).Error)
}
