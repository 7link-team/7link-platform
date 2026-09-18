package files

import (
	"path/filepath"
	"testing"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFileMigrationsAndGeneratedQueriesSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "files.sqlite")+"?_foreign_keys=on"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	source := (&Module{}).MigrationSource()
	source.Prefix = "ft_"
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	q := query.Bind(db, source.Prefix)
	object := model.Object{ID: 1, BackendID: "local", StorageKey: "objects/1", URI: "file:///backend/objects/1", State: "verified", Size: 3, MIME: "application/octet-stream", Type: "application", Subtype: "octet-stream", MetadataJSON: "{}", MetadataState: "unknown", Version: 1, CreatedAt: 1}
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&object))
	require.NoError(t, q.AccountReference.WithContext(t.Context()).Create(&model.AccountReference{ID: 2, PrincipalID: 3, ObjectID: 1, Purpose: "avatar", State: "pending", CreatedAt: 1}))
	found, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(guid.ID(1))).Take()
	require.NoError(t, err)
	require.Equal(t, object.URI, found.URI)
	duplicate := object
	duplicate.ID = 4
	require.Error(t, q.Object.WithContext(t.Context()).Create(&duplicate))
	require.Error(t, q.AccountReference.WithContext(t.Context()).Create(&model.AccountReference{ID: 5, PrincipalID: 3, ObjectID: 99, Purpose: "avatar", State: "pending"}))
	upload := model.Upload{ID: 6, PrincipalID: 3, BackendID: "local", StorageKey: "uploads/6", Name: "file.png", Size: 3, State: "active", ReleaseState: "none", Version: 1, ExpiresAt: 100}
	require.NoError(t, q.Upload.WithContext(t.Context()).Create(&upload))
	part := model.UploadPart{ID: 7, UploadID: 6, Index: 1, Length: 3, State: "confirmed", ReleaseState: "none"}
	require.NoError(t, q.UploadPart.WithContext(t.Context()).Create(&part))
	duplicatePart := part
	duplicatePart.ID = 8
	require.Error(t, q.UploadPart.WithContext(t.Context()).Create(&duplicatePart))
	badOffset := upload
	badOffset.ID, badOffset.Offset = 9, 4
	require.Error(t, q.Upload.WithContext(t.Context()).Create(&badOffset))
	require.NoError(t, goosex.MigrateDownTo(t.Context(), sqlDB, source, "sqlite", 0))
	require.False(t, db.Migrator().HasTable(query.BindNames(source.Prefix).Objects.String()))
}
