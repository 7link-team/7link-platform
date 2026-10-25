package intake

import (
	"7link.com/platform/internal/infra/events"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestTriageWakeUsesOnlyCurrentCanonicalSource(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id = ?", 10).Updates(map[string]any{"status": "RECEIVED", "channel_id": 99, "channel_kind": "API"}).Error)
	event := events.Envelope{EventID: 501, EventType: "intake.received", TenantID: 7, ProjectID: 20, Subject: events.Subject{Kind: "intake", ID: 10}, SubjectVersion: 1}
	calls := 0
	m.triageRequestCoordinator = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, TriageRequestAuthority) error {
		return nil
	}
	m.triageAgent = func(_ context.Context, _ *gorm.DB, tenant, project, channel guid.ID, kind string) (guid.ID, bool, error) {
		calls++
		require.EqualValues(t, 7, tenant)
		require.EqualValues(t, 20, project)
		require.EqualValues(t, 99, channel)
		require.Equal(t, "API", kind)
		return 77, true, nil
	}
	read := func(e events.Envelope) (guid.ID, bool, error) {
		var id guid.ID
		var found bool
		err := db.Transaction(func(tx *gorm.DB) error { var err error; id, found, err = m.triageWakeTargetOn(ctx, tx, e); return err })
		return id, found, err
	}
	id, found, err := read(event)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 77, id)
	for _, mutate := range []func(*events.Envelope){func(e *events.Envelope) { e.TenantID = 8 }, func(e *events.Envelope) { e.ProjectID = 21 }, func(e *events.Envelope) { e.Subject.ID = 999 }, func(e *events.Envelope) { e.SubjectVersion = 2 }} {
		e := event
		mutate(&e)
		_, found, err = read(e)
		require.NoError(t, err)
		require.False(t, found)
	}
	require.Equal(t, 1, calls)
	_, _, err = m.triageWakeTargetOn(ctx, db, event)
	require.ErrorIs(t, err, ErrInvalidArgument)
	m.triageAgent = nil
	_, _, err = read(event)
	require.ErrorIs(t, err, ErrUnavailable)
	fault := errors.New("current channel unavailable")
	m.triageAgent = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, bool, error) {
		return 0, false, fault
	}
	_, _, err = read(event)
	require.ErrorIs(t, err, fault)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id = ?", 10).Update("status", "CONVERTED").Error)
	_, found, err = read(event)
	require.NoError(t, err)
	require.False(t, found)
}

func TestTriageWakeRequestSurvivesLaterEligibleSourceEdits(t *testing.T) {
	m, db, ctx, _, _ := changeSourceHarness(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Updates(map[string]any{"status": "CLARIFYING", "version": 3, "last_triage_requested_at": at}).Error)
	m.triageRequestCoordinator = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, TriageRequestAuthority) error {
		return nil
	}
	m.triageAgent = func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, bool, error) {
		return 77, true, nil
	}
	event := events.Envelope{EventID: 501, EventType: "intake.triage_requested", TenantID: 7, ProjectID: 20, Subject: events.Subject{Kind: "intake", ID: 10}, SubjectVersion: 2}
	read := func(e events.Envelope) bool {
		var found bool
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, f, err := m.triageWakeTargetOn(ctx, tx, e); found = f; return err }))
		return found
	}
	require.True(t, read(event), "a later clarification/source update must not silently discard an accepted request before the consumer sees it")
	event.SubjectVersion = 4
	require.False(t, read(event), "future versions are not persisted request facts")
	event.SubjectVersion = 2
	require.NoError(t, db.Table(m.names.Records.String()).Where("id=10").Update("status", "CONVERTED").Error)
	require.False(t, read(event), "a resolved source must not be queued again")
}

func TestTriageConnectorBindsOnce(t *testing.T) {
	m, _ := referencesHarness(t)
	s := module.NewServices()
	require.NoError(t, m.provideTriageAgent(s))
	require.NoError(t, s.Seal())
	c, err := module.Resolve[TriageAgentConnector](s)
	require.NoError(t, err)
	require.Error(t, c.Bind(nil))
	reader := TriageAgentReader(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) (guid.ID, bool, error) {
		return 0, false, nil
	})
	require.Error(t, c.BindAuthority(nil))
	authority := TriageRequestAuthority(func(context.Context, *gorm.DB, guid.ID, guid.ID, string) error { return nil })
	require.NoError(t, c.BindAuthority(authority))
	require.Error(t, c.BindAuthority(authority))
	require.Error(t, c.BindRequestCoordinator(nil))
	request := TriageRequestCoordinator(func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string, TriageRequestAuthority) error {
		return nil
	})
	require.NoError(t, c.BindRequestCoordinator(request))
	require.Error(t, c.BindRequestCoordinator(request))
	require.NoError(t, c.Bind(reader))
	require.Error(t, c.Bind(reader))
}
