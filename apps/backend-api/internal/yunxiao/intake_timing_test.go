package yunxiao

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTriageCoordinatorChecksCurrentExactChannel(t *testing.T) {
	m, db, ctx := workHarness(t)
	agent := guid.ID(77)
	settings := intakeSettingsFixture()
	settings.Enabled = true
	settings.TriageAgentID = &agent
	valid := true
	fault := errors.New("coordinator revoked")
	m.projects.ValidateTriageAgentOn = func(_ context.Context, _ *gorm.DB, tenant, id guid.ID) error {
		require.EqualValues(t, 1, tenant)
		require.Equal(t, agent, id)
		if !valid {
			return fault
		}
		return nil
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings)
		if err != nil {
			return err
		}
		id, found, err := m.intakeTriageCoordinatorOn(ctx, tx, 1, 99, v.ID, "API")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, agent, id)
		for _, project := range []guid.ID{98, 100} {
			_, found, err = m.intakeTriageCoordinatorOn(ctx, tx, 1, project, v.ID, "API")
			require.NoError(t, err)
			require.False(t, found)
		}
		_, found, err = m.intakeTriageCoordinatorOn(ctx, tx, 1, 99, v.ID, "EMAIL")
		require.NoError(t, err)
		require.False(t, found)
		valid = false
		_, _, err = m.intakeTriageCoordinatorOn(ctx, tx, 1, 99, v.ID, "API")
		require.ErrorIs(t, err, fault)
		valid = true
		settings.Enabled = false
		_, err = m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", v.Version, settings)
		require.NoError(t, err)
		_, found, err = m.intakeTriageCoordinatorOn(ctx, tx, 1, 99, v.ID, "API")
		require.NoError(t, err)
		require.False(t, found)
		return nil
	}))
	_, _, err := m.intakeTriageCoordinatorOn(ctx, db, 1, 99, 1, "API")
	require.ErrorIs(t, err, ErrInvalid)
}

func TestTimingReadsExactStoredChannelPolicy(t *testing.T) {
	m, db, ctx := workHarness(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "EMAIL", 0, intakeSettingsFixture())
		if err != nil {
			return err
		}
		timing, found, err := m.clarificationTimingOn(ctx, tx, 1, 99, v.ID, "EMAIL")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, 48, timing.ReminderHours)
		require.Equal(t, 168, timing.TimeoutHours)
		require.Equal(t, 2, timing.MaxReminders)
		require.Equal(t, v.Version, timing.ChannelVersion)
		for _, project := range []int64{98, 100} {
			_, found, err = m.clarificationTimingOn(ctx, tx, 1, guid.ID(project), v.ID, "EMAIL")
			require.NoError(t, err)
			require.False(t, found)
		}
		_, found, err = m.clarificationTimingOn(ctx, tx, 1, 99, v.ID, "API")
		require.NoError(t, err)
		require.False(t, found)
		_, _, err = m.clarificationTimingOn(ctx, tx, 2, 99, v.ID, "EMAIL")
		require.Error(t, err)
		return nil
	}))
	_, _, err := m.clarificationTimingOn(ctx, db, 1, 99, 1, "EMAIL")
	require.ErrorIs(t, err, ErrInvalid)
	s := module.NewServices()
	called := false
	require.NoError(t, module.Provide[intake.TriageAgentConnector](s, intake.TriageAgentConnector{BindRequestCoordinator: func(r intake.TriageRequestCoordinator) error { require.NotNil(t, r); return nil }, Bind: func(r intake.TriageAgentReader) error { require.NotNil(t, r); return nil }}))
	require.NoError(t, module.Provide[intake.ChannelTimingConnector](s, intake.ChannelTimingConnector{Bind: func(r intake.ChannelTimingReader) error { called = r != nil; return nil }}))
	require.NoError(t, s.Seal())
	require.NoError(t, m.bindIntakeTiming(s))
	require.True(t, called)
}

func TestRequestedTriageLocksParticipantsBeforeProjectAndRejectsStaleChannel(t *testing.T) {
	m, db, ctx := workHarness(t)
	agent := guid.ID(77)
	settings := intakeSettingsFixture()
	settings.Enabled = true
	settings.TriageAgentID = &agent
	var order []string
	m.projects.ValidateTriageAgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error {
		order = append(order, "participants")
		return nil
	}
	var channel guid.ID
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		v, err := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings)
		channel = v.ID
		return err
	}))
	order = nil
	check := intake.TriageRequestAuthority(func(_ context.Context, _ *gorm.DB, tenant, project guid.ID, permission string) error {
		order = append(order, "project")
		require.Equal(t, "intake_triage", permission)
		return nil
	})
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return m.intakeRequestedCoordinatorOn(ctx, tx, 1, 99, channel, "API", check) }))
	require.Equal(t, []string{"participants", "project"}, order)
	denied := errors.New("current authority revoked")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.intakeRequestedCoordinatorOn(ctx, tx, 1, 99, channel, "API", func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return denied })
	}), denied)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return m.intakeRequestedCoordinatorOn(ctx, tx, 1, 99, channel, "API", func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ string) error {
			return tx.Table(m.names.IntakeChannels.String()).Where("id=?", channel).Update("version", 2).Error
		})
	}), ErrConflict)
	require.Error(t, m.intakeRequestedCoordinatorOn(ctx, db, 1, 99, channel, "API", check))
}

func TestIntakeTimingBindingsRejectUnavailableAndFailedConnectors(t *testing.T) {
	fault := errors.New("connector rejected reader")
	for _, mode := range []string{"missing", "timing_nil", "timing_failure", "triage_nil", "triage_failure", "request_nil", "request_failure", "timing_only"} {
		t.Run(mode, func(t *testing.T) {
			m := &Module{}
			s := module.NewServices()
			if mode != "missing" {
				timing := intake.ChannelTimingConnector{Bind: func(intake.ChannelTimingReader) error { return nil }}
				if mode == "timing_nil" {
					timing.Bind = nil
				}
				if mode == "timing_failure" {
					timing.Bind = func(intake.ChannelTimingReader) error { return fault }
				}
				require.NoError(t, module.Provide[intake.ChannelTimingConnector](s, timing))
			}
			if mode != "timing_only" {
				triage := intake.TriageAgentConnector{Bind: func(intake.TriageAgentReader) error { return nil }, BindRequestCoordinator: func(intake.TriageRequestCoordinator) error { return nil }}
				switch mode {
				case "triage_nil":
					triage.Bind = nil
				case "triage_failure":
					triage.Bind = func(intake.TriageAgentReader) error { return fault }
				case "request_nil":
					triage.BindRequestCoordinator = nil
				case "request_failure":
					triage.BindRequestCoordinator = func(intake.TriageRequestCoordinator) error { return fault }
				}
				require.NoError(t, module.Provide[intake.TriageAgentConnector](s, triage))
			}
			require.NoError(t, s.Seal())
			err := m.bindIntakeTiming(s)
			if mode == "timing_only" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestIntakePolicyReadersRejectCorruptOrUnusableCurrentPolicy(t *testing.T) {
	for _, mode := range []string{"corrupt", "invalid", "disabled", "agent_denied", "query_failure", "deleted_on_authorize"} {
		t.Run(mode, func(t *testing.T) {
			m, db, ctx := workHarness(t)
			settings := intakeSettingsFixture()
			settings.Enabled = true
			agent := guid.ID(77)
			settings.TriageAgentID = &agent
			m.projects.ValidateTriageAgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return nil }
			var channel guid.ID
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				v, e := m.saveIntakeChannelOn(ctx, tx, 1, 99, "API", 0, settings)
				channel = v.ID
				return e
			}))
			switch mode {
			case "corrupt":
				require.NoError(t, db.Table(m.names.IntakeChannels.String()).Where("id=?", channel).Update("document", "[").Error)
			case "invalid":
				require.NoError(t, db.Table(m.names.IntakeChannels.String()).Where("id=?", channel).Update("document", `{}`).Error)
			case "disabled":
				require.NoError(t, db.Table(m.names.IntakeChannels.String()).Where("id=?", channel).Update("document", `{"display_name":"disabled","language":"en-US","daily_limit":20,"reminder_hours":48,"timeout_hours":168}`).Error)
			case "agent_denied":
				m.projects.ValidateTriageAgentOn = func(context.Context, *gorm.DB, guid.ID, guid.ID) error { return ErrForbidden }
			case "query_failure":
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("policy_read_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == m.names.IntakeChannels.String() {
						tx.AddError(ErrUnavailable)
					}
				}))
				defer db.Callback().Query().Remove("policy_read_failure")
			}
			authorize := intake.TriageRequestAuthority(func(_ context.Context, tx *gorm.DB, _, _ guid.ID, _ string) error {
				if mode == "deleted_on_authorize" {
					return tx.Table(m.names.IntakeChannels.String()).Where("id=?", channel).Delete(map[string]any{}).Error
				}
				return nil
			})
			require.Error(t, db.Transaction(func(tx *gorm.DB) error {
				return m.intakeRequestedCoordinatorOn(ctx, tx, 1, 99, channel, "API", authorize)
			}))
			if mode == "corrupt" || mode == "invalid" || mode == "query_failure" {
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					_, _, e := m.clarificationTimingOn(ctx, tx, 1, 99, channel, "API")
					require.Error(t, e)
					_, _, e = m.intakeTriageCoordinatorOn(ctx, tx, 1, 99, channel, "API")
					require.Error(t, e)
					return nil
				}))
			}
		})
	}
	m, db, ctx := workHarness(t)
	_, _, e := m.intakeTriageCoordinatorOn(ctx, nil, 1, 99, 1, "API")
	require.ErrorIs(t, e, ErrInvalid)
	_, _, e = m.clarificationTimingOn(ctx, nil, 1, 99, 1, "API")
	require.ErrorIs(t, e, ErrInvalid)
	require.ErrorIs(t, m.intakeRequestedCoordinatorOn(ctx, nil, 1, 99, 1, "API", nil), ErrInvalid)
	require.ErrorIs(t, m.intakeRequestedCoordinatorOn(ctx, db, 1, 99, 1, "API", nil), ErrInvalid)
}
