package todo

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	todoi18n "7link.com/platform/internal/todo/i18n"
	"7link.com/platform/pkg/appenv"
	"7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// stubIDs hands out ascending identifiers so tests do not need the guid module
// and its worker-ID lease.
type stubIDs struct{ next atomic.Int64 }

func (s *stubIDs) Next() (int64, error) { return s.next.Add(1), nil }

func todoContext(t *testing.T) context.Context {
	return authn.WithClaims(t.Context(), &authn.Claims{TenantID: 1, EntityID: 10, PrincipalID: 11})
}

func newTestService(t *testing.T, prefix string) (*service, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(todoContext(t), false, map[string]gormx.Datasource{
		"primary": {
			Type: "sqlite", DSN: filepath.Join(t.TempDir(), "todo.sqlite"),
			Writable: true, Readable: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = router.Close() })

	db := router.Write()
	require.NotNil(t, db)
	sqlDB, err := db.DB()
	require.NoError(t, err)

	source := (&Module{}).MigrationSource()
	source.Prefix = prefix
	require.NoError(t, goosex.Migrate(todoContext(t), sqlDB, source, db.Dialector.Name()))

	rt := appenv.NewRuntime(
		appenv.NewIdentity("test", "1.0.0"),
		appenv.WithDatabase(router),
		appenv.WithTablePrefix(prefix),
	)
	// The module registers its catalog in Initialize; tests that reach the
	// service directly must do the same, or every message resolves to its own
	// key and a locale assertion proves nothing.
	require.NoError(t, platformi18n.NewModule(rt, platformi18n.Base).Initialize(todoContext(t)))
	require.NoError(t, todoi18n.Register())
	svc := newService(rt, &stubIDs{})
	svc.checker, svc.departments = &todoPolicy{}, &todoPolicy{}
	svc.fields = &todoPolicy{}
	return svc, db
}

func draft(title string) Draft {
	return Draft{Title: title, Status: StatusOpen, Priority: DefaultPriority}
}

func TestCreateStoresValidatedDraft(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("write the spec"))
	require.NoError(t, err)

	assert.Equal(t, "write the spec", row.Title)
	assert.Equal(t, string(StatusOpen), row.Status)
	assert.Equal(t, 1, row.Version, "a new task starts at version 1")
	assert.Nil(t, row.CompletedAt, "an open task has no completion time")
	assert.False(t, row.CreatedAt.IsZero())
	assert.Equal(t, row.CreatedAt, row.UpdatedAt)
}

func TestCreateStampsCompletionWhenBornDone(t *testing.T) {
	svc, _ := newTestService(t, "")
	d := draft("already finished")
	d.Status = StatusDone
	row, err := svc.Create(todoContext(t), d)
	require.NoError(t, err)
	require.NotNil(t, row.CompletedAt, "entering done must stamp completed_at")
}

func TestUpdateRequiresMatchingVersion(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)

	title := "renamed"
	updated, err := svc.Update(todoContext(t), row.ID, row.Version, Patch{Title: &title})
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Title)
	assert.Equal(t, 2, updated.Version, "a write advances the version")

	// The stale version must be rejected rather than silently overwriting.
	_, err = svc.Update(todoContext(t), row.ID, row.Version, Patch{Title: &title})
	require.ErrorIs(t, err, ErrVersionConflict)
}

// TestConcurrentUpdatesLoseNothing is the reason the version column exists: two
// writers holding the same view must not both succeed.
func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("contended"))
	require.NoError(t, err)

	first, second := "first", "second"
	_, errFirst := svc.Update(todoContext(t), row.ID, row.Version, Patch{Title: &first})
	_, errSecond := svc.Update(todoContext(t), row.ID, row.Version, Patch{Title: &second})

	require.NoError(t, errFirst)
	require.ErrorIs(t, errSecond, ErrVersionConflict)

	final, err := svc.Get(todoContext(t), row.ID)
	require.NoError(t, err)
	assert.Equal(t, "first", final.Title, "the winning write must survive intact")
}

func TestCompletionTimeFollowsStatus(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)

	done := StatusDone
	completed, err := svc.Update(todoContext(t), row.ID, row.Version, Patch{Status: &done})
	require.NoError(t, err)
	require.NotNil(t, completed.CompletedAt, "entering done stamps completed_at")

	open := StatusOpen
	reopened, err := svc.Update(todoContext(t), completed.ID, completed.Version, Patch{Status: &open})
	require.NoError(t, err)
	assert.Nil(t, reopened.CompletedAt, "leaving done clears completed_at")
}

func TestPatchDistinguishesClearFromLeaveAlone(t *testing.T) {
	svc, _ := newTestService(t, "")
	due := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	d := draft("task")
	d.DueAt = &due
	row, err := svc.Create(todoContext(t), d)
	require.NoError(t, err)
	require.NotNil(t, row.DueAt)

	// A patch that does not mention due_at leaves it untouched.
	title := "renamed"
	untouched, err := svc.Update(todoContext(t), row.ID, row.Version, Patch{Title: &title})
	require.NoError(t, err)
	require.NotNil(t, untouched.DueAt, "an unmentioned field must survive")

	// A patch carrying an explicit nil clears it.
	var cleared *time.Time
	got, err := svc.Update(todoContext(t), untouched.ID, untouched.Version, Patch{DueAt: &cleared})
	require.NoError(t, err)
	assert.Nil(t, got.DueAt, "an explicit null must clear the field")
}

func TestDeleteHidesTaskButKeepsItRecoverable(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)

	require.NoError(t, svc.Delete(todoContext(t), row.ID, row.Version))

	_, err = svc.Get(todoContext(t), row.ID)
	require.ErrorIs(t, err, ErrNotFound, "a deleted task is invisible to the live query")

	deleted, err := svc.GetIncludingDeleted(todoContext(t), row.ID)
	require.NoError(t, err)
	require.NotNil(t, deleted.DeletedAt)

	restored, err := svc.Restore(todoContext(t), deleted.ID, deleted.Version)
	require.NoError(t, err)
	assert.Nil(t, restored.DeletedAt)

	live, err := svc.Get(todoContext(t), row.ID)
	require.NoError(t, err)
	assert.Equal(t, "task", live.Title)
}

func TestDeleteRejectsStaleVersion(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)

	title := "renamed"
	_, err = svc.Update(todoContext(t), row.ID, row.Version, Patch{Title: &title})
	require.NoError(t, err)

	err = svc.Delete(todoContext(t), row.ID, row.Version)
	require.ErrorIs(t, err, ErrVersionConflict, "deleting a stale view is a lost update too")
}

func TestDeleteTwiceReportsNotFound(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)

	require.NoError(t, svc.Delete(todoContext(t), row.ID, row.Version))
	err = svc.Delete(todoContext(t), row.ID, row.Version+1)
	require.ErrorIs(t, err, ErrNotFound, "an already-deleted task is indistinguishable from absent")
}

func TestRestoreRejectsLiveTask(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)

	_, err = svc.Restore(todoContext(t), row.ID, row.Version)
	require.ErrorIs(t, err, ErrNotDeleted)
}

func TestOperationsFailWithoutWritableDatasource(t *testing.T) {
	svc := newService(appenv.NewRuntime(appenv.NewIdentity("test", "1.0.0")), &stubIDs{})
	_, err := svc.Create(context.Background(), draft("task"))
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = svc.Get(context.Background(), guid.ID(1))
	require.ErrorIs(t, err, ErrUnavailable)
}

// TestPrefixedSchemaIsUsedThroughout migrates only the prefixed schema, so any
// statement that forgot the prefix would hit a table that does not exist.
func TestPrefixedSchemaIsUsedThroughout(t *testing.T) {
	const prefix = "wp_"
	svc, db := newTestService(t, prefix)

	assert.True(t, db.Migrator().HasTable(prefix+"todos"))
	assert.False(t, db.Migrator().HasTable("todos"), "the unprefixed table must not exist")

	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)
	title := "renamed"
	updated, err := svc.Update(todoContext(t), row.ID, row.Version, Patch{Title: &title})
	require.NoError(t, err)
	require.NoError(t, svc.Delete(todoContext(t), updated.ID, updated.Version))
	deleted, err := svc.GetIncludingDeleted(todoContext(t), row.ID)
	require.NoError(t, err)
	_, err = svc.Restore(todoContext(t), deleted.ID, deleted.Version)
	require.NoError(t, err)
}

// closedService returns a service whose datasource is shut, so every statement
// fails at the driver. It covers the paths where the database itself errors,
// which no amount of valid input can reach.
func closedService(t *testing.T) *service {
	t.Helper()
	svc, db := newTestService(t, "")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	return svc
}

func TestDatabaseFailuresPropagate(t *testing.T) {
	svc := closedService(t)
	id := guid.ID(1)

	_, err := svc.Create(todoContext(t), draft("task"))
	assert.Error(t, err, "create must report a driver failure")

	_, err = svc.Get(todoContext(t), id)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound,
		"a driver failure is not the same as a missing row")

	_, err = svc.Update(todoContext(t), id, 1, Patch{})
	assert.Error(t, err)

	assert.Error(t, svc.Delete(todoContext(t), id, 1))

	_, err = svc.Restore(todoContext(t), id, 1)
	assert.Error(t, err)
}

func TestUpdateMissingTaskReportsNotFound(t *testing.T) {
	svc, _ := newTestService(t, "")
	title := "renamed"
	_, err := svc.Update(todoContext(t), guid.ID(404), 1, Patch{Title: &title})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteMissingTaskReportsNotFound(t *testing.T) {
	svc, _ := newTestService(t, "")
	assert.ErrorIs(t, svc.Delete(todoContext(t), guid.ID(404), 1), ErrNotFound)
}

func TestRestoreMissingTaskReportsNotFound(t *testing.T) {
	svc, _ := newTestService(t, "")
	_, err := svc.Restore(todoContext(t), guid.ID(404), 1)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRestoreRejectsStaleVersion(t *testing.T) {
	svc, _ := newTestService(t, "")
	row, err := svc.Create(todoContext(t), draft("task"))
	require.NoError(t, err)
	require.NoError(t, svc.Delete(todoContext(t), row.ID, row.Version))

	// The delete advanced the version; restoring with the pre-delete one is a
	// stale write.
	_, err = svc.Restore(todoContext(t), row.ID, row.Version)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

// failingIDs stands in for an identifier source that cannot mint IDs, which
// leaves the module degraded rather than broken.
type failingIDs struct{}

func (failingIDs) Next() (int64, error) { return 0, guid.ErrUnavailable }

func TestCreateFailsWhenIdentifiersAreUnavailable(t *testing.T) {
	svc, _ := newTestService(t, "")
	svc.ids = failingIDs{}
	_, err := svc.Create(todoContext(t), draft("task"))
	assert.ErrorIs(t, err, ErrUnavailable)
}
