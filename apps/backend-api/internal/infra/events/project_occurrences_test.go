package events

import (
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProjectOccurrencesFenceTenantProjectTypeAndWindow(t *testing.T) {
	m, db := newTestPublisher(t)
	base := testEvent()
	base.ExtraTopics = nil
	base.Data = map[string]any{"target": "DISCOVERY"}
	for _, mutate := range []func(*Event){func(*Event) {}, func(e *Event) { e.TenantID++ }, func(e *Event) { e.ProjectID++ }} {
		e := base
		mutate(&e)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, e); return err }))
	}
	read := func(types []string, since time.Time) []ProjectOccurrence {
		var rows []ProjectOccurrence
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			rows, err = m.projectOccurrencesOn(t.Context(), tx, base.TenantID, base.ProjectID, types, since)
			return err
		})
		require.NoError(t, err)
		return rows
	}
	rows := read([]string{base.Type}, m.now().Add(-time.Hour))
	require.Len(t, rows, 1)
	require.Equal(t, "DISCOVERY", rows[0].Target)
	require.True(t, rows[0].At.Equal(m.now()))
	require.Empty(t, read([]string{"unrelated.event"}, m.now().Add(-time.Hour)))
	require.Empty(t, read([]string{base.Type}, m.now().Add(time.Second)))
	_, err := m.projectOccurrencesOn(t.Context(), db, base.TenantID, base.ProjectID, []string{base.Type}, m.now().Add(-time.Hour))
	require.ErrorIs(t, err, ErrInvalidEvent)
}

func TestSubjectHumanActorUsesExactImmutableSource(t *testing.T) {
	m, db := newTestPublisher(t)
	base := testEvent()
	for _, change := range []func(*Event){
		func(e *Event) { e.Actor.ID = 31 },
		func(e *Event) { e.TenantID = 9; e.Actor.ID = 32 },
		func(e *Event) { e.ProjectID = 8; e.Actor.ID = 33 },
		func(e *Event) { e.Subject.ID++; e.Actor.ID = 34 },
		func(e *Event) { e.Actor.Kind = "AGENT"; e.Actor.ID = 35 },
	} {
		e := base
		e.ExtraTopics = nil
		change(&e)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, e); return err }))
	}
	actor, found, err := m.subjectHumanActorOn(t.Context(), db, base.TenantID, base.ProjectID, base.Subject.Kind, base.Subject.ID, base.Type)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, guid.ID(31), actor, "latest unrelated tenant/project/subject/Agent events cannot replace the Human")
	_, found, err = m.subjectHumanActorOn(t.Context(), db, base.TenantID, base.ProjectID, base.Subject.Kind, 987, base.Type)
	require.NoError(t, err)
	require.False(t, found)
	_, _, err = m.subjectHumanActorOn(t.Context(), nil, 1, 2, "project", 3, base.Type)
	require.ErrorIs(t, err, ErrInvalidEvent)
	s := module.NewServices()
	require.NoError(t, m.provideProjectOccurrences(s))
	require.NoError(t, s.Seal())
	source, err := module.Resolve[SubjectHumanActor](s)
	require.NoError(t, err)
	_, found, err = source(t.Context(), db, base.TenantID, base.ProjectID, "other", base.Subject.ID, base.Type)
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, db.Migrator().DropTable(m.names.Events.String()))
	_, _, err = source(t.Context(), db, base.TenantID, base.ProjectID, base.Subject.Kind, base.Subject.ID, base.Type)
	require.Error(t, err)
}
