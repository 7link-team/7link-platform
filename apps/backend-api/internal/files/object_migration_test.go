package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func migrationFixture(t *testing.T) (*gorm.DB, *Local, *Local, *model.Object, []byte) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migration.sqlite")+"?_foreign_keys=on"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	source := (&Module{}).MigrationSource()
	source.Prefix = "move_"
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	local, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	target, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	body := bytes.Repeat([]byte("immutable migration bytes"), 10000)
	uri, err := local.Put(t.Context(), "objects/1", bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	sums := NewChecksums()
	_, _ = sums.Write(body)
	row := &model.Object{ID: 1, BackendID: "local", StorageKey: "objects/1", URI: uri, State: "verified", Size: int64(len(body)), SHA256: sums.Digests().SHA256, Version: 1, MIME: "application/octet-stream", Type: "application", Subtype: "octet-stream", MD5: sums.Digests().MD5, SHA1: sums.Digests().SHA1, SHA512: sums.Digests().SHA512, MetadataJSON: "{}", MetadataState: "unsupported", ExtractorVersion: "", CreatedAt: 1}
	q := query.Bind(db, "move_")
	require.NoError(t, q.Object.WithContext(t.Context()).Create(row))
	return db, local, target, row, body
}

func TestObjectMigrationVerifiedSwitchReplayAndRetainedSource(t *testing.T) {
	db, source, target, row, body := migrationFixture(t)
	effects := 0
	appendAudit := func(ctx context.Context, tx *gorm.DB, event audit.DomainEvent) error {
		require.Equal(t, "file_object_migrated", event.EventType)
		require.Equal(t, row.ID, event.TargetID)
		effects++
		return nil
	}
	report, err := migrateObject(t.Context(), db, "move_", 1, "local", "s3", source, target, appendAudit)
	require.NoError(t, err)
	require.False(t, report.Replayed)
	require.Equal(t, row.SHA256, report.SHA256)
	q := query.Bind(db, "move_")
	after, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(row.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "s3", after.BackendID)
	require.Equal(t, int64(2), after.Version)
	require.Equal(t, row.SHA256, after.SHA256)
	require.NoError(t, verifyMigrationBytes(t.Context(), source, row))
	require.NoError(t, verifyMigrationBytes(t.Context(), target, row))
	report, err = migrateObject(t.Context(), db, "move_", 1, "local", "s3", source, target, appendAudit)
	require.NoError(t, err)
	require.True(t, report.Replayed)
	require.Equal(t, 1, effects)
	set := &backendDriver{active: "s3", drivers: map[string]Driver{"s3": target, "local": source}, prefix: "move_", db: func() *gorm.DB { return db }}
	reader, err := set.Open(t.Context(), row.StorageKey)
	require.NoError(t, err)
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, body, actual)
}

func TestObjectMigrationInterruptedCopyRollbackCollisionAndCAS(t *testing.T) {
	for _, scenario := range []string{"copied-before-crash", "audit-rollback", "collision", "source-corrupt", "cas-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			db, source, target, row, body := migrationFixture(t)
			q := query.Bind(db, "move_")
			effects := 0
			appendAudit := func(context.Context, *gorm.DB, audit.DomainEvent) error {
				effects++
				if scenario == "audit-rollback" {
					return errors.New("audit unavailable")
				}
				return nil
			}
			var destination Driver = target
			switch scenario {
			case "copied-before-crash":
				_, err := target.Put(t.Context(), row.StorageKey, bytes.NewReader(body), row.Size)
				require.NoError(t, err)
			case "collision":
				_, err := target.Put(t.Context(), row.StorageKey, bytes.NewReader(bytes.Repeat([]byte("x"), len(body))), row.Size)
				require.NoError(t, err)
			case "source-corrupt":
				require.NoError(t, source.Delete(t.Context(), row.StorageKey))
				_, err := source.Put(t.Context(), row.StorageKey, bytes.NewReader(bytes.Repeat([]byte("x"), len(body))), row.Size)
				require.NoError(t, err)
			case "cas-conflict":
				destination = &migrationPutHook{Driver: target, hook: func() {
					_, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(row.ID)).Update(q.Object.Version, 2)
					require.NoError(t, err)
				}}
			}
			_, err := migrateObject(t.Context(), db, "move_", 1, "local", "s3", source, destination, appendAudit)
			if scenario == "copied-before-crash" {
				require.NoError(t, err)
				require.Equal(t, 1, effects)
			} else {
				require.Error(t, err)
				current, e := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(row.ID)).Take()
				require.NoError(t, e)
				require.Equal(t, "local", current.BackendID)
				if scenario == "cas-conflict" {
					require.ErrorIs(t, err, ErrMigrationConflict)
				}
				if scenario == "audit-rollback" {
					_, err = migrateObject(t.Context(), db, "move_", 1, "local", "s3", source, target, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
					require.NoError(t, err)
				}
			}
		})
	}
}

type migrationPutHook struct {
	Driver
	hook func()
}

func (d *migrationPutHook) Put(ctx context.Context, key string, r io.Reader, size int64) (string, error) {
	uri, err := d.Driver.Put(ctx, key, r, size)
	if err == nil {
		d.hook()
	}
	return uri, err
}

func TestObjectMigrationRealLocalToS3(t *testing.T) {
	target := realS3(t)
	db, source, _, row, _ := migrationFixture(t)
	require.NoError(t, target.Delete(t.Context(), row.StorageKey))
	t.Cleanup(func() { require.NoError(t, target.Delete(context.Background(), row.StorageKey)) })
	report, err := migrateObject(t.Context(), db, "move_", row.ID, "local", "s3", source, target, func(context.Context, *gorm.DB, audit.DomainEvent) error { return nil })
	require.NoError(t, err)
	require.False(t, report.Replayed)
	q := query.Bind(db, "move_")
	after, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(row.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "s3", after.BackendID)
	require.Contains(t, after.URI, "s3://")
	require.NoError(t, verifyMigrationBytes(t.Context(), target, row))
	require.NoError(t, verifyMigrationBytes(t.Context(), source, row))
	report, err = migrateObject(t.Context(), db, "move_", row.ID, "local", "s3", source, target, func(context.Context, *gorm.DB, audit.DomainEvent) error {
		t.Fatal("replay must not append again")
		return nil
	})
	require.NoError(t, err)
	require.True(t, report.Replayed)
}
