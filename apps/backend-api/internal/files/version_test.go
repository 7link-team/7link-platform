package files

import (
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFileVersionsOverwriteRollbackAndImmutableMetadata(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	now := time.Now()
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", now))
	q := query.Bind(db, accounts.prefix)
	parent, slot := guid.ID(2), 1
	node := model.Node{ID: 3, SpaceID: 1, ParentID: &parent, ParentKind: "directory", LiveSlot: &slot, Kind: "file", Name: "movie.mp4", NameKey: "movie.mp4", Ext: "mp4", State: "active", Version: 1, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}
	require.NoError(t, q.Node.WithContext(t.Context()).Create(&node))
	object := model.Object{ID: 4, BackendID: "local", StorageKey: "objects/4", URI: "file:///backend/objects/4", State: "verified", Size: 3, MIME: "video/mp4", Type: "video", Subtype: "mp4", MetadataJSON: "{}", MetadataState: "unknown", Version: 1, CreatedAt: 1}
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&object))
	replacement := object
	replacement.ID, replacement.StorageKey, replacement.URI = 8, "objects/8", "file:///backend/objects/8"
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&replacement))
	appendVersion := func(id guid.ID, number, revision int64) error {
		objectID := guid.ID(4)
		if number > 1 {
			objectID = 8
		}
		return db.Transaction(func(tx *gorm.DB) error {
			return tree.appendVersion(t.Context(), tx, model.Version{ID: id, SpaceID: 1, NodeID: 3, ObjectID: objectID, Number: number, CreatedBy: 90, Ext: "mp4", DeclaredMIME: "video/mp4"}, revision, now)
		})
	}
	require.ErrorIs(t, tree.appendVersion(t.Context(), db, model.Version{ID: 5, SpaceID: 1, NodeID: 3, ObjectID: 4, Number: 1, CreatedBy: 90}, 1, now), ErrAccountTransaction)
	require.NoError(t, appendVersion(5, 1, 1))
	space, err := q.Space.WithContext(t.Context()).Take()
	require.NoError(t, err)
	for _, invalid := range []struct{ number, revision int64 }{{2, 1}, {3, 2}, {1, 2}} {
		require.ErrorIs(t, appendVersion(6, invalid.number, invalid.revision), errVersionConflict)
		after, err := q.Space.WithContext(t.Context()).Take()
		require.NoError(t, err)
		require.Equal(t, space.Version, after.Version, "failed operation rolls back the space lock/revision")
	}
	require.NoError(t, appendVersion(6, 2, 2))
	current, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(3))).Take()
	require.NoError(t, err)
	require.Equal(t, guid.ID(3), current.ID)
	require.Equal(t, guid.ID(6), *current.CurrentVersionID)
	require.EqualValues(t, 3, current.Version)
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(3))).UpdateSimple(q.Node.Name.Value("movie.apk"), q.Node.NameKey.Value("movie.apk"), q.Node.Ext.Value("apk"))
	require.NoError(t, err)
	history, err := q.Version.WithContext(t.Context()).Order(q.Version.Number.Asc()).Find()
	require.NoError(t, err)
	require.Len(t, history, 2)
	for _, version := range history {
		require.Equal(t, "mp4", version.Ext)
		_, err = q.Version.WithContext(t.Context()).Where(q.Version.ID.Eq(version.ID)).UpdateSimple(q.Version.Ext.Value("apk"))
		require.Error(t, err, "history is immutable at the database boundary")
	}
	physical, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(guid.ID(4))).Take()
	require.NoError(t, err)
	require.Equal(t, "video/mp4", physical.MIME)
	_, err = q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).UpdateSimple(q.Node.CurrentVersionID.Value(guid.ID(5)))
	require.Error(t, err, "directory cannot acquire a file head")
	other := node
	other.ID, other.Name, other.NameKey = 7, "other.mp4", "other.mp4"
	other.CurrentVersionID = current.CurrentVersionID
	require.Error(t, q.Node.WithContext(t.Context()).Create(&other), "head cannot refer to another node")
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(time.Hour), 0, 100)
	require.NoError(t, err)
	physical, err = q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(guid.ID(4))).Take()
	require.NoError(t, err)
	require.Equal(t, "verified", physical.State, "historical references protect physical content")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	source := (&Module{}).MigrationSource()
	source.Prefix = accounts.prefix
	require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 4), "populated heads and history must support migration rollback")
	require.False(t, db.Migrator().HasTable(query.BindNames(accounts.prefix).Versions.String()))
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
}

func TestFileVersionScopeAndObjectStateFailuresRollback(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	tree := directoryTree{prefix: accounts.prefix}
	now := time.Now()
	require.NoError(t, tree.createSpace(t.Context(), db, 1, 2, 90, "tenant", now))
	require.NoError(t, tree.createSpace(t.Context(), db, 10, 11, 91, "tenant", now))
	q := query.Bind(db, accounts.prefix)
	parent, slot := guid.ID(2), 1
	node := model.Node{ID: 3, SpaceID: 1, ParentID: &parent, ParentKind: "directory", LiveSlot: &slot, Kind: "file", Name: "a", NameKey: "a", State: "active", Version: 1}
	require.NoError(t, q.Node.WithContext(t.Context()).Create(&node))
	object := model.Object{ID: 4, BackendID: "local", StorageKey: "objects/4", URI: "file:///backend/objects/4", State: "staged", Size: 3, MIME: "application/octet-stream", Type: "application", Subtype: "octet-stream", MetadataJSON: "{}", MetadataState: "unknown", Version: 1}
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&object))
	input := model.Version{ID: 5, SpaceID: 1, NodeID: 3, ObjectID: 4, Number: 1, CreatedBy: 90}
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.appendVersion(t.Context(), tx, input, 1, now)
	}), ErrNotFound, "unverified storage cannot become a version")
	_, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(guid.ID(4))).UpdateSimple(q.Object.State.Value("verified"))
	require.NoError(t, err)
	input.SpaceID = 10
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.appendVersion(t.Context(), tx, input, 1, now)
	}), errVersionConflict)
	input.SpaceID, input.NodeID = 1, 2
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return tree.appendVersion(t.Context(), tx, input, 1, now)
	}), errVersionConflict, "directories cannot have content versions")
	count, err := q.Version.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	physical, err := q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.EqualValues(t, 1, physical.Version)
}
