package clientwork

import (
	"7link.com/platform/internal/agents"
	"7link.com/platform/internal/fleet"
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/intake"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestIntakeWakeQueuesActualReferenceAndRollsBack(t *testing.T) {
	m, db, _, _ := harness(t)
	ctx := testContext(t, 2)
	require.NoError(t, db.Exec("CREATE TABLE wake_probe (source TEXT UNIQUE, subject INTEGER)").Error)
	e := events.Envelope{EventID: 501, EventType: "intake.received", TenantID: 1, ProjectID: 99, Actor: events.Actor{Kind: "HUMAN", ID: 2}, Subject: events.Subject{Kind: "intake", ID: 10}, SubjectVersion: 1}
	m.triageIdentity.AgentOn = func(_ context.Context, _ *gorm.DB, tenant, agent guid.ID) (fleet.AgentIdentity, bool, error) {
		require.EqualValues(t, 77, agent)
		return fleet.AgentIdentity{ID: agent, TenantID: tenant, PrincipalID: 177, State: "ACTIVE"}, true, nil
	}
	m.triageTarget = intake.TriageWakeTarget{ReadOn: func(_ context.Context, tx *gorm.DB, actual events.Envelope) (guid.ID, bool, error) {
		require.Equal(t, e, actual)
		require.NotNil(t, tx)
		return 77, true, nil
	}}
	m.triageInbox = agents.InboxAppender{AppendOn: func(_ context.Context, tx *gorm.DB, tenant, agent guid.ID, in agents.InboxReference) (int64, error) {
		require.EqualValues(t, 1, tenant)
		require.EqualValues(t, 177, agent)
		require.Equal(t, "STATE", in.Kind)
		require.EqualValues(t, 10, in.SubjectID)
		require.Empty(t, in.StreamKey)
		return 1, tx.Exec("INSERT INTO wake_probe VALUES (?,?) ON CONFLICT(source) DO NOTHING", in.SourceEventID, in.SubjectID).Error
	}}
	run := func() error { return db.Transaction(func(tx *gorm.DB) error { return m.intakeWakeOn(ctx, tx, e) }) }
	require.NoError(t, run())
	require.NoError(t, run())
	var count int64
	require.NoError(t, db.Table("wake_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	fault := errors.New("wake outbox unavailable")
	m.triageInbox.AppendOn = func(_ context.Context, tx *gorm.DB, _ guid.ID, _ guid.ID, _ agents.InboxReference) (int64, error) {
		require.NoError(t, tx.Exec("INSERT INTO wake_probe VALUES ('rollback',10)").Error)
		return 0, fault
	}
	require.ErrorIs(t, run(), fault)
	require.NoError(t, db.Table("wake_probe").Count(&count).Error)
	require.EqualValues(t, 1, count)
	m.triageTarget.ReadOn = func(context.Context, *gorm.DB, events.Envelope) (guid.ID, bool, error) { return 0, false, nil }
	require.NoError(t, run())
	m.triageTarget.ReadOn = nil
	require.ErrorIs(t, run(), ErrUnavailable)
}
