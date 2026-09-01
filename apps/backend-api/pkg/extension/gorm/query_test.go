package gormx

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingQueryObserver struct {
	mu       sync.Mutex
	events   []QueryEvent
	started  int
	finished int
}

func (observer *recordingQueryObserver) ObserveQuery(_ context.Context, event QueryEvent) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
}

func (observer *recordingQueryObserver) QueryStarted(_ context.Context, _ QueryActivity) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.started++
}

func (observer *recordingQueryObserver) QueryFinished(_ context.Context, _ QueryActivity) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.finished++
}

func (observer *recordingQueryObserver) snapshot() []QueryEvent {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]QueryEvent(nil), observer.events...)
}

type observedWidget struct {
	ID   uint
	Name string
}

func TestDatasourceRouterObservesSanitizedSQLTemplates(t *testing.T) {
	router, err := NewDatasourceRouter(t.Context(), false, map[string]Datasource{
		"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "observed.sqlite"), Writable: true, Readable: true},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	observer := &recordingQueryObserver{}
	require.NoError(t, router.RegisterQueryObserver(observer))
	db := router.Write()
	require.NoError(t, db.AutoMigrate(&observedWidget{}))
	require.NoError(t, db.Create(&observedWidget{Name: "private-value"}).Error)
	var widgets []observedWidget
	require.NoError(t, db.Where("name = ?", "private-value").Find(&widgets).Error)
	require.NoError(t, db.Exec(`UPDATE observed_widgets SET name = 'raw-secret' WHERE id = 123`).Error)

	events := observer.snapshot()
	require.NotEmpty(t, events)
	operations := make(map[string]bool)
	for _, event := range events {
		operations[event.Operation] = true
		assert.Equal(t, "primary", event.Datasource)
		assert.NotContains(t, event.Statement, "private-value")
		assert.NotContains(t, event.Statement, "raw-secret")
		assert.NotContains(t, event.Statement, "123")
		assert.NotContains(t, event.Statement, "--")
	}
	assert.True(t, operations["INSERT"])
	assert.True(t, operations["SELECT"])
	assert.True(t, operations["UPDATE"])
	observer.mu.Lock()
	assert.Equal(t, observer.started, observer.finished)
	assert.Equal(t, len(observer.events), observer.finished)
	observer.mu.Unlock()
}

func TestSanitizeSQLTemplateRemovesLiteralsAndComments(t *testing.T) {
	result := sanitizeSQLTemplate("SELECT * FROM widgets WHERE name = 'private' AND id = 42 AND note = $tag$secret$tag$ AND code = 0xDEAD /* token */ -- tail\nLIMIT 1")
	assert.Equal(t, "SELECT * FROM widgets WHERE name = ? AND id = ? AND note = ? AND code = ? LIMIT ?", result)
}
