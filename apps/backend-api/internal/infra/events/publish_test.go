package events

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/events/model"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type testIDs struct {
	next atomic.Int64
	err  error
	zero bool
}

func (s *testIDs) Next() (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.zero {
		return 0, nil
	}
	return s.next.Add(1), nil
}
func newTestPublisher(t *testing.T) (*Module, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "events.sqlite"), Writable: true, Readable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = router.Close() })
	rt := appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithDatabase(router), appenv.WithTablePrefix("ev_"))
	m := NewModule(rt)
	m.ids = &testIDs{}
	m.now = func() time.Time { return time.Date(2026, 10, 15, 10, 0, 0, 123000000, time.UTC) }
	db := router.Write()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := m.MigrationSource()
	source.Prefix = "ev_"
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, db.Dialector.Name()))
	require.NoError(t, m.AddTypes("delivery", TypeSpec{Name: "project.created", SchemaVersion: 1, SubjectKind: "project", Realtime: RealtimeSignal}))
	require.NoError(t, m.Initialize(t.Context()))
	return m, db
}
func testEvent() Event {
	return Event{TenantID: 1, ProjectID: 2, Type: "project.created", Actor: Actor{Kind: "HUMAN", ID: 3, AuthoritySource: "role"}, Subject: Subject{Kind: "project", ID: 2}, SubjectVersion: 1, Data: map[string]any{"name": "Project"}, ExtraTopics: []string{"tenant/1/projects"}}
}
func TestPublishCommitsOrRollsBackWithBusinessWrite(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			m, db := newTestPublisher(t)
			require.NoError(t, db.Exec("CREATE TABLE business_probe (id BIGINT PRIMARY KEY)").Error)
			abort := errors.New("later project step failed")
			err := db.Transaction(func(tx *gorm.DB) error {
				require.NoError(t, tx.Exec("INSERT INTO business_probe VALUES (2)").Error)
				id, err := m.Publish(t.Context(), tx, testEvent())
				require.NoError(t, err)
				require.Positive(t, id)
				var row model.EventRow
				require.NoError(t, tx.Table("ev_domain_events").Take(&row).Error)
				require.Equal(t, id, row.ID)
				require.Nil(t, row.StreamSeq)
				require.Nil(t, row.SequencedAt)
				require.True(t, row.Durable)
				require.Equal(t, "SIGNAL", row.RealtimeMode)
				require.Equal(t, `["tenant/1/project/2","tenant/1/projects"]`, row.Topics)
				require.JSONEq(t, `{"name":"Project"}`, row.Data)
				require.Equal(t, m.now(), row.OccurredAt)
				require.EqualValues(t, 1, row.TenantID)
				require.EqualValues(t, 2, *row.ProjectID)
				require.EqualValues(t, 3, *row.ActorID)
				require.Nil(t, row.CausationID)
				if rollback {
					return abort
				}
				return nil
			})
			if rollback {
				require.ErrorIs(t, err, abort)
			} else {
				require.NoError(t, err)
			}
			expected := int64(1)
			if rollback {
				expected = 0
			}
			for _, table := range []string{"ev_domain_events", "business_probe"} {
				var count int64
				require.NoError(t, db.Table(table).Count(&count).Error)
				require.Equal(t, expected, count)
			}
		})
	}
}
func TestPublishRejectsInvalidEnvelopesWithoutWriting(t *testing.T) {
	m, db := newTestPublisher(t)
	cases := map[string]func(*Event){
		"system actor": func(e *Event) { e.Actor = Actor{Kind: "SYSTEM", ID: 9} },
		"unknown":      func(e *Event) { e.Type = "project.unknown" },
		"tenant":       func(e *Event) { e.TenantID = -1 }, "project": func(e *Event) { e.ProjectID = -1 },
		"subject": func(e *Event) { e.Subject.ID = 0 }, "subject kind": func(e *Event) { e.Subject.Kind = "task" },
		"version": func(e *Event) { e.SubjectVersion = -1 }, "causation": func(e *Event) { e.CausationID = -1 },
		"actor kind": func(e *Event) { e.Actor.Kind = "ROOT" }, "actor id": func(e *Event) { e.Actor.ID = 0 },
		"authority":     func(e *Event) { e.Actor.AuthoritySource = strings.Repeat("x", 161) },
		"correlation":   func(e *Event) { e.CorrelationID = strings.Repeat("x", 65) },
		"payload size":  func(e *Event) { e.Data = strings.Repeat("x", 16<<10) },
		"payload type":  func(e *Event) { e.Data = func() {} },
		"foreign topic": func(e *Event) { e.ExtraTopics = []string{"tenant/2/projects"} },
		"topic kind":    func(e *Event) { e.ExtraTopics = []string{"tenant/1/bad-name"} },
		"topic id":      func(e *Event) { e.ExtraTopics = []string{"tenant/1/project/0"} },
		"topic alias":   func(e *Event) { e.ExtraTopics = []string{"tenant/1/project/02"} },
		"topic stream":  func(e *Event) { e.ExtraTopics = []string{"tenant/1/project/2/*"} },
		"topic depth":   func(e *Event) { e.ExtraTopics = []string{"tenant/1/project/2/stream/extra"} },
		"topic count": func(e *Event) {
			e.ExtraTopics = []string{"tenant/1/a", "tenant/1/b", "tenant/1/c", "tenant/1/d", "tenant/1/e", "tenant/1/f", "tenant/1/g", "tenant/1/h"}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := testEvent()
			change(&e)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, e); require.Error(t, err); return nil }))
		})
	}
	var count int64
	require.NoError(t, db.Table("ev_domain_events").Count(&count).Error)
	require.Zero(t, count)
	_, err := m.Publish(t.Context(), nil, testEvent())
	require.ErrorIs(t, err, ErrInvalidEvent)
	_, err = m.Publish(t.Context(), db, testEvent())
	require.ErrorIs(t, err, ErrInvalidEvent)
}
func TestPublishPropagatesUnavailableAndStorageFailures(t *testing.T) {
	m, db := newTestPublisher(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		m.sealed = false
		_, err := m.Publish(t.Context(), tx, testEvent())
		require.ErrorIs(t, err, ErrUnavailable)
		m.sealed = true
		failure := errors.New("ID generator down")
		m.ids = &testIDs{err: failure}
		_, err = m.Publish(t.Context(), tx, testEvent())
		require.ErrorIs(t, err, failure)
		m.ids = &testIDs{zero: true}
		_, err = m.Publish(t.Context(), tx, testEvent())
		require.ErrorIs(t, err, ErrUnavailable)
		return nil
	}))
	m.ids = &testIDs{}
	require.NoError(t, db.Exec("DROP TABLE ev_domain_events").Error)
	err := db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(context.Background(), tx, testEvent()); return err })
	require.Error(t, err)
}
func TestPublishSystemEventAndBatchBudget(t *testing.T) {
	m, db := newTestPublisher(t)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		e := testEvent()
		e.ProjectID = 0
		e.Actor = Actor{Kind: "SYSTEM"}
		e.CausationID = 7
		e.ExtraTopics = []string{"tenant/1/project/2/channel"}
		for range 100 {
			_, err := m.Publish(t.Context(), tx, e)
			require.NoError(t, err)
		}
		_, err := m.Publish(t.Context(), tx, e)
		require.ErrorIs(t, err, ErrInvalidEvent)
		return nil
	}))
}

func TestPublishBudgetFollowsTransactionAcrossSessions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "prepared"}[prepared], func(t *testing.T) {
			m, db := newTestPublisher(t)
			for range 2 {
				err := db.Session(&gorm.Session{PrepareStmt: prepared}).Transaction(func(tx *gorm.DB) error {
					for i := range 101 {
						session := tx.WithContext(t.Context())
						if prepared {
							session = session.Session(&gorm.Session{PrepareStmt: true})
						}
						if i%2 == 0 {
							session = tx.Session(&gorm.Session{NewDB: true}).Model(&model.EventRow{})
						}
						_, err := m.Publish(t.Context(), session, testEvent())
						if i < 100 {
							require.NoError(t, err)
						} else {
							require.ErrorIs(t, err, ErrInvalidEvent)
						}
					}
					return nil
				})
				require.NoError(t, err)
			}
			var count int64
			require.NoError(t, db.Table("ev_domain_events").Count(&count).Error)
			require.EqualValues(t, 200, count)
		})
	}
}

func TestPublishPlatformEventUsesExplicitZeroTenantTopic(t *testing.T) {
	m, db := newTestPublisher(t)
	e := testEvent()
	e.TenantID = 0
	e.ProjectID = 0
	e.Actor = Actor{Kind: "SYSTEM"}
	e.ExtraTopics = []string{"tenant/0/projects"}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, e); return err }))
	var row model.EventRow
	require.NoError(t, db.Table("ev_domain_events").Take(&row).Error)
	require.Zero(t, row.TenantID)
	require.JSONEq(t, `["tenant/0/project/2","tenant/0/projects"]`, row.Topics)
	e.ExtraTopics = []string{"tenant/1/projects"}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.Publish(t.Context(), tx, e); return err }), ErrInvalidEvent)
}

func TestEphemeralTransportIsFullAndExcludedFromDomainConsumers(t *testing.T) {
	m, db := newTestPublisher(t)
	e := testEvent()
	e.Type = "notification.created"
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.PublishEphemeral(t.Context(), tx, e); return err }))
	var rows []model.EventRow
	require.NoError(t, db.Table(m.names.Events.String()).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Durable)
	require.Equal(t, string(RealtimeFull), rows[0].RealtimeMode)
	domain, err := m.ReadEventsOn(t.Context(), db, 0, 100)
	require.NoError(t, err)
	require.Empty(t, domain)
	e.Type = "invalid"
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { _, err := m.PublishEphemeral(t.Context(), tx, e); return err }))
}
