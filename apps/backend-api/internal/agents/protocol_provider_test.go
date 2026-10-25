package agents

import (
	"7link.com/platform/internal/agents/query"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"strings"
	"testing"
	"time"
)

func TestCommandsRollbackOnOwnedStorageFailure(t *testing.T) {
	cases := []struct {
		name, table, callback string
		run                   func(context.Context, *Module, *gorm.DB, ReminderView, ToolView, ResetView) error
	}{
		{"inbox-query", "inbox", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.inboxOn(c, tx, 1, 7, 0, 50)
			return e
		}},
		{"live-create", "live", "create", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.appendOn(c, tx, 1, 7, InboxReference{SourceEventID: "next", Kind: "STATE", SubjectID: 1})
			return e
		}},
		{"append-query", "inbox", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.appendOn(c, tx, 1, 7, InboxReference{SourceEventID: "next", Kind: "STATE", SubjectID: 1})
			return e
		}},
		{"append-create", "inbox", "create", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.appendOn(c, tx, 1, 7, InboxReference{SourceEventID: "next", Kind: "STATE", SubjectID: 1})
			return e
		}},
		{"append-update", "live", "update", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.appendOn(c, tx, 1, 7, InboxReference{SourceEventID: "next", Kind: "STATE", SubjectID: 1})
			return e
		}},
		{"ack-update", "live", "update", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.ackOn(c, tx, 1, 7, 1)
			return e
		}},
		{"report-query", "live", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			return m.reportOn(c, tx, 1, 7, 1, "ONLINE")
		}},
		{"report-update", "live", "update", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			return m.reportOn(c, tx, 1, 7, 1, "ONLINE")
		}},
		{"complete-query", "reset", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, r ResetView) error {
			return m.completeResetOn(c, tx, 1, 7, r.ID, "DONE")
		}},
		{"complete-update", "reset", "update", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, r ResetView) error {
			return m.completeResetOn(c, tx, 1, 7, r.ID, "DONE")
		}},
		{"complete-live-update", "live", "update", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, r ResetView) error {
			return m.completeResetOn(c, tx, 1, 7, r.ID, "DONE")
		}},
		{"reminder-create", "reminder", "create", func(c context.Context, m *Module, tx *gorm.DB, r ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.reminderOn(c, tx, 1, 7, r.ReminderInput)
			return e
		}},
		{"reminder-list", "reminder", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, _, e := m.remindersOn(c, tx, 1, 7, 0, 50)
			return e
		}},
		{"reminder-change", "reminder", "update", func(c context.Context, m *Module, tx *gorm.DB, r ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.changeReminderOn(c, tx, 1, 7, r.ID, 1, "PAUSED")
			return e
		}},
		{"reminder-fire-query", "reminder", "query", func(c context.Context, m *Module, tx *gorm.DB, r ReminderView, _ ToolView, _ ResetView) error {
			return m.fireReminderOn(c, tx, 1, r.ID, r.DueAt)
		}},
		{"reminder-fire-update", "reminder", "update", func(c context.Context, m *Module, tx *gorm.DB, r ReminderView, _ ToolView, _ ResetView) error {
			return m.fireReminderOn(c, tx, 1, r.ID, r.DueAt)
		}},
		{"tools-list", "tool", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, _, e := m.toolsOn(agentContext(t, 2), tx, 1, 0, 0, 50)
			return e
		}},
		{"tools-create", "tool", "create", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, _ ToolView, _ ResetView) error {
			_, e := m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "another-tool", Kind: "SKILL", Title: "Another", SkillBody: "Instructions"})
			return e
		}},
		{"grant-tool-query", "tool", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, v ToolView, _ ResetView) error {
			_, e := m.grantOn(agentContext(t, 2), tx, 1, v.ID, 7, "ACTIVE")
			return e
		}},
		{"grant-query", "grant", "query", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, v ToolView, _ ResetView) error {
			_, e := m.grantOn(agentContext(t, 2), tx, 1, v.ID, 7, "ACTIVE")
			return e
		}},
		{"grant-create", "grant", "create", func(c context.Context, m *Module, tx *gorm.DB, _ ReminderView, v ToolView, _ ResetView) error {
			_, e := m.grantOn(agentContext(t, 2), tx, 1, v.ID, 7, "ACTIVE")
			return e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, db, _ := agentHarness(t)
			ctx := agentContext(t, 7)
			now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error { return nil }}))
			var reminder ReminderView
			var tool ToolView
			var reset ResetView
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				_, e := m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "first", Kind: "STATE", SubjectID: 1})
				require.NoError(t, e)
				reminder, e = m.reminderOn(ctx, tx, 1, 7, ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Once", DueAt: now.Add(time.Minute), MaxFires: 1})
				require.NoError(t, e)
				tool, e = m.createToolOn(agentContext(t, 2), tx, 1, ToolInput{EntryKey: "test-tool", Kind: "SKILL", Title: "Test", SkillBody: "Instructions"})
				require.NoError(t, e)
				reset, e = m.resetOn(agentContext(t, 2), tx, 1, 7, ResetInput{Kind: "SESSION", Reason: "Clear stale session state"})
				return e
			}))
			tables := map[string]string{"inbox": m.names.InboxEvent.String(), "live": m.names.LiveState.String(), "reset": m.names.Reset.String(), "reminder": m.names.Reminder.String(), "tool": m.names.ToolEntry.String(), "grant": m.names.ToolGrant.String()}
			fault := errors.New("storage provider unavailable")
			inject := func(tx *gorm.DB) {
				if tx.Statement.Table == tables[tc.table] {
					tx.AddError(fault)
				}
			}
			switch tc.callback {
			case "query":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:provider", inject))
				defer db.Callback().Query().Remove("test:provider")
			case "create":
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:provider", inject))
				defer db.Callback().Create().Remove("test:provider")
			case "update":
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:provider", inject))
				defer db.Callback().Update().Remove("test:provider")
			}
			require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { return tc.run(ctx, m, tx, reminder, tool, reset) }), fault)
			db.Callback().Query().Remove("test:provider")
			db.Callback().Create().Remove("test:provider")
			db.Callback().Update().Remove("test:provider")
			state, e := query.BindLiveState(db, m.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.EqualValues(t, 1, state.Head)
			require.Zero(t, state.Ack)
			require.Equal(t, 1, state.SessionEpoch)
			r, e := query.BindReminder(db, m.names).WithContext(ctx).First()
			require.NoError(t, e)
			require.Equal(t, 1, r.Version)
			require.Zero(t, r.FireCount)
		})
	}
}

type failedAgentIDs struct {
	id  int64
	err error
}

func (f failedAgentIDs) Next() (int64, error) { return f.id, f.err }

func TestUnavailableDependenciesAndSnowflakeFailuresLeaveNoCommands(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	self := agentContext(t, 7)
	fault := errors.New("id generator unavailable")
	now := m.now()
	in := ReminderInput{ProjectID: new(guid.ID(3)), ThreadID: 4, Title: "Review", DueAt: now.Add(time.Hour), MaxFires: 1}
	require.NoError(t, m.bindRuntime(RuntimeControl{RequestOn: func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, int, int) error { return nil }}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		tool, e := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "test-grant", Kind: "SKILL", Title: "Test", SkillBody: "Instructions"})
		require.NoError(t, e)
		for _, ids := range []failedAgentIDs{{err: fault}, {id: 0}} {
			m.ids = ids
			_, e = m.appendOn(self, tx, 1, 7, InboxReference{SourceEventID: "no-id", Kind: "STATE", SubjectID: 1})
			require.Error(t, e)
			_, e = m.reminderOn(self, tx, 1, 7, in)
			require.Error(t, e)
			_, e = m.resetOn(ctx, tx, 1, 7, ResetInput{Kind: "SESSION", Reason: "Clear stale session state"})
			require.Error(t, e)
			_, e = m.grantOn(ctx, tx, 1, tool.ID, 7, "ACTIVE")
			require.Error(t, e)
		}
		m.ids = nil
		_, e = m.reminderOn(self, tx, 1, 7, in)
		require.ErrorIs(t, e, ErrUnavailable)
		_, e = m.grantOn(ctx, tx, 1, tool.ID, 7, "ACTIVE")
		require.ErrorIs(t, e, ErrUnavailable)
		_, e = m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "test-no-id", Kind: "SKILL", Title: "Test", SkillBody: "Instructions"})
		require.ErrorIs(t, e, ErrUnavailable)
		m.threads.CheckOn = nil
		_, _, e = m.remindersOn(self, tx, 1, 7, 0, 50)
		require.NoError(t, e)
		require.ErrorIs(t, m.checkReminderAccess(self, tx, 1, 7, 1), ErrUnavailable)
		_, e = m.reminderOn(self, tx, 1, 7, in)
		require.ErrorIs(t, e, ErrUnavailable)
		for _, modify := range []func(*ReminderInput){func(v *ReminderInput) { v.Checks = strings.Repeat("x", 2001) }, func(v *ReminderInput) { v.Escalation = strings.Repeat("x", 2001) }, func(v *ReminderInput) { v.OutputFormat = strings.Repeat("x", 2001) }} {
			bad := in
			modify(&bad)
			_, e = m.reminderOn(self, tx, 1, 7, bad)
			require.ErrorIs(t, e, ErrInvalid)
		}
		m.audit = nil
		require.ErrorIs(t, m.record(self, tx, 1, 7, "ACK", false), ErrUnavailable)
		return nil
	}))
	for _, table := range []string{m.names.Reset.String(), m.names.Reminder.String(), m.names.ToolGrant.String(), m.names.InboxEvent.String()} {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		require.Zero(t, n)
	}
}

func TestAuthorityAndIdentifierProvidersNeverBecomeImplicitPermission(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 2)
	fault := errors.New("authority provider unavailable")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		original := m.principals
		m.principals.LockActiveRow = func(context.Context, *gorm.DB, guid.ID) (identity.Principal, bool, error) {
			return identity.Principal{}, false, fault
		}
		_, e := m.authorize(ctx, tx, 1, 7, "agent_manage", true)
		require.ErrorIs(t, e, fault)
		require.ErrorIs(t, m.teamToolAuthority(ctx, tx, 1), fault)
		m.principals = original
		m.principals.HumanByLogin = func(context.Context, *gorm.DB, string) (identity.Principal, bool, error) {
			return identity.Principal{}, false, fault
		}
		_, e = m.authorize(ctx, tx, 1, 7, "agent_manage", true)
		require.ErrorIs(t, e, fault)
		require.ErrorIs(t, m.teamToolAuthority(ctx, tx, 1), fault)
		m.principals = original
		m.agents.ByPrincipalOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) (fleet.AgentIdentity, bool, error) {
			return fleet.AgentIdentity{}, false, fault
		}
		_, e = m.authorize(ctx, tx, 1, 7, "agent_manage", true)
		require.ErrorIs(t, e, fault)
		_, e = m.appendOn(ctx, tx, 1, 7, InboxReference{SourceEventID: "fault", Kind: "STATE", SubjectID: 1})
		require.ErrorIs(t, e, fault)
		require.ErrorIs(t, m.reportOn(ctx, tx, 1, 7, 1, "ONLINE"), fault)
		_, e = m.grantOn(ctx, tx, 1, 1, 7, "ACTIVE")
		require.ErrorIs(t, e, fault)
		_, e = m.authorize(ctx, tx, 1, 7, "SELF", false)
		require.ErrorIs(t, e, fault)
		return nil
	}))
	// Failed/zero snowflakes cannot produce a catalog row, even for an owner.
	for _, ids := range []failedAgentIDs{{err: fault}, {id: 0}} {
		m.ids = ids
		require.Error(t, db.Transaction(func(tx *gorm.DB) error {
			_, e := m.createToolOn(ctx, tx, 1, ToolInput{EntryKey: "test-ids", Kind: "SKILL", Title: "Instructions", SkillBody: "Do the work"})
			return e
		}))
	}
	var rows int64
	require.NoError(t, db.Table(m.names.ToolEntry.String()).Count(&rows).Error)
	require.Zero(t, rows)
}

func TestOwnerConstraintsUseTargetOwnerAndUnavailableAuthorityFailsClosed(t *testing.T) {
	m, db, _ := agentHarness(t)
	ctx := agentContext(t, 3)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{OwnerIDs: []string{"3"}}, nil
		}
		_, e := m.authorize(ctx, tx, 1, 7, "agent_manage", true)
		require.ErrorIs(t, e, ErrForbidden)
		m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{OwnerIDs: []string{"2"}}, nil
		}
		_, e = m.authorize(ctx, tx, 1, 7, "agent_manage", true)
		require.NoError(t, e)
		m.tenantPolicy = func(context.Context, *gorm.DB, guid.ID, string, guid.ID) (authz.DataConstraint, error) {
			return authz.DataConstraint{}, errors.New("policy unavailable")
		}
		_, e = m.authorize(ctx, tx, 1, 7, "agent_manage", true)
		require.ErrorContains(t, e, "policy unavailable")
		m.tenantPolicy = nil
		_, e = m.authorize(ctx, tx, 1, 7, "agent_manage", true)
		require.ErrorIs(t, e, ErrUnavailable)
		require.ErrorIs(t, m.teamToolAuthority(ctx, tx, 1), ErrUnavailable)
		m.principals = identity.PrincipalStateStore{}
		_, e = m.authorize(ctx, tx, 1, 7, "SELF", false)
		require.ErrorIs(t, e, ErrUnavailable)
		return nil
	}))
}
