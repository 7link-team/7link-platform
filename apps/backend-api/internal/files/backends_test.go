package files

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBackendResolutionPropagatesStorageFailuresAndUsesPreparedIdentity(t *testing.T) {
	db, source, target, _, _ := migrationFixture(t)
	set := &backendDriver{active: "local", drivers: map[string]Driver{"local": source, "archive": target}, prefix: "move_", db: func() *gorm.DB { return db }}
	q := query.Bind(db, "move_")
	_, err := target.Put(t.Context(), "prepared/object", strings.NewReader("archive bytes"), 13)
	require.NoError(t, err)
	require.NoError(t, q.AccountPreparation.WithContext(t.Context()).Create(&model.AccountPreparation{ID: 20, PrincipalID: 3, BackendID: "archive", StorageKey: "prepared/object", State: "ready", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), CreatedAt: 1}))
	r, err := set.Open(t.Context(), "prepared/object")
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, "archive bytes", string(body))
	fault := errors.New("backend routing storage failure")
	for _, table := range []string{query.BindNames("move_").Objects.String(), query.BindNames("move_").AccountPreparations.String()} {
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:backend-resolution", func(tx *gorm.DB) {
			if tx.Statement.Table == table {
				tx.AddError(fault)
			}
		}))
		reader, err := set.Open(t.Context(), "prepared/object")
		require.ErrorIs(t, err, fault)
		require.Nil(t, reader)
		require.ErrorIs(t, set.Delete(t.Context(), "prepared/object"), fault)
		require.NoError(t, db.Callback().Query().Remove("test:backend-resolution"))
	}
	// Released intents no longer route reads to a historical backend.
	_, err = q.AccountPreparation.WithContext(t.Context()).Where(q.AccountPreparation.ID.Eq(guid.ID(20))).Update(q.AccountPreparation.State, "released")
	require.NoError(t, err)
	_, err = set.Open(t.Context(), "prepared/object")
	require.ErrorIs(t, err, ErrNotFound)
	r, err = target.Open(t.Context(), "prepared/object")
	require.NoError(t, err)
	require.NoError(t, r.Close())
}

func TestBackendDispatchReadsWritesCopiesAndReclaimsPersistedIdentity(t *testing.T) {
	db, source, target, row, body := migrationFixture(t)
	set := &backendDriver{active: "local", drivers: map[string]Driver{"local": source, "other": target}, prefix: "move_", db: func() *gorm.DB { return db }}
	for _, tc := range []struct {
		driver   Driver
		object   *model.Object
		expected error
	}{
		{source, nil, ErrNotFound},
		{nil, row, ErrNotFound},
		{set, &model.Object{BackendID: "removed", StorageKey: row.StorageKey}, ErrUnsupported},
	} {
		reader, err := openStoredObject(t.Context(), tc.driver, tc.object)
		require.ErrorIs(t, err, tc.expected)
		require.Nil(t, reader)
	}
	require.Equal(t, source.Capabilities(), set.Capabilities())
	uri, err := set.Put(t.Context(), "new/object", strings.NewReader("new"), 3)
	require.NoError(t, err)
	require.NotEmpty(t, uri)
	r, err := set.OpenRange(t.Context(), row.StorageKey, 1, 8)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, body[1:9], got)
	_, err = set.Copy(t.Context(), row.StorageKey, "copied/object")
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = source.Put(t.Context(), "copied/object", strings.NewReader(string(body)), int64(len(body)))
	require.NoError(t, err)
	r, err = set.Open(t.Context(), "copied/object")
	require.NoError(t, err)
	got, err = io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, body, got)
	require.NoError(t, set.ReclaimWrite(t.Context(), "copied/object"))
	_, err = set.Open(t.Context(), "copied/object")
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, set.Delete(t.Context(), "new/object"))
	_, err = set.Open(t.Context(), "new/object")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = set.WritePart(t.Context(), "new/multipart", "handle", 1, strings.NewReader("x"), 1)
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = set.InspectParts(t.Context(), "new/multipart", "handle")
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = set.Complete(t.Context(), "new/multipart", "handle", nil)
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorIs(t, set.Abort(t.Context(), "new/multipart", "handle"), ErrUnsupported)
	for _, key := range []string{"../bad"} {
		require.ErrorIs(t, set.Delete(t.Context(), key), ErrInvalidKey)
		_, err = set.OpenRange(t.Context(), key, 0, 1)
		require.ErrorIs(t, err, ErrInvalidKey)
		require.ErrorIs(t, set.ReclaimWrite(t.Context(), key), ErrInvalidKey)
	}
	noDB := &backendDriver{active: "local", drivers: set.drivers}
	_, err = noDB.Open(t.Context(), row.StorageKey)
	require.ErrorIs(t, err, ErrUnsupported)
}

func TestBackendsReadPersistedIdentityAndPreserveAccountAuthorization(t *testing.T) {
	db, source, target, row, body := migrationFixture(t)
	set := &backendDriver{active: "s3", drivers: map[string]Driver{"s3": target, "local": source}, prefix: "move_", db: func() *gorm.DB { return db }}
	accounts, err := NewAccountObjects(set, "s3", "move_")
	require.NoError(t, err)
	q := query.Bind(db, "move_")
	require.NoError(t, q.AccountReference.WithContext(t.Context()).Create(&model.AccountReference{ID: 2, PrincipalID: 3, ObjectID: row.ID, Purpose: "avatar", State: "active", CreatedAt: 1}))
	allowed, err := accounts.AccountKeyReadable(t.Context(), db, 3, row.StorageKey, time.Now())
	require.NoError(t, err)
	require.True(t, allowed, "changing the write backend must not revoke an existing private reference")
	allowed, err = accounts.AccountKeyReadable(t.Context(), db, 4, row.StorageKey, time.Now())
	require.NoError(t, err)
	require.False(t, allowed)
	reader, err := set.Open(t.Context(), row.StorageKey)
	require.NoError(t, err)
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, body, actual)
	_, err = q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(row.ID)).Update(q.Object.BackendID, "unconfigured")
	require.NoError(t, err)
	_, err = set.Open(t.Context(), row.StorageKey)
	require.ErrorIs(t, err, ErrUnsupported, "unknown identities must never fall back to the active backend")
	_, err = q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(row.ID)).Update(q.Object.BackendID, "local")
	require.NoError(t, err)
	_, err = set.Copy(t.Context(), row.StorageKey, "copy/1")
	require.ErrorIs(t, err, ErrUnsupported, "cross-backend copying is an explicit migration")
	_, err = set.Open(t.Context(), "../outside")
	require.ErrorIs(t, err, ErrInvalidKey)
	one := &backendDriver{active: "s3", drivers: map[string]Driver{"s3": target}, prefix: "move_", db: func() *gorm.DB { return db }}
	_, err = one.Open(t.Context(), row.StorageKey)
	require.ErrorIs(t, err, ErrUnsupported, "removing a legacy identity cannot reinterpret unmigrated rows")
	_, err = set.Begin(t.Context(), "objects/2")
	require.ErrorIs(t, err, ErrUnsupported, "local target must not acquire synthetic multipart capability")
	_, err = q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(row.ID)).Update(q.Object.BackendID, "s3")
	require.NoError(t, err)
	_, err = set.Open(t.Context(), row.StorageKey)
	require.ErrorIs(t, err, ErrNotFound, "a missing target object cannot silently read retained source bytes")
	require.Equal(t, []string{"local", "s3"}, accounts.readableBackends())
	var missing *Module
	_, err = missing.MigrateObject(context.Background(), guid.ID(1), "local")
	require.ErrorIs(t, err, ErrUnsupported)
}

func TestBackendsRejectDuplicateIdentityAndRegisterNativeS3(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("storage-test", "1"))
	m := NewModule(runtime, Config{ReadBackends: map[string]BackendConfig{"local": {Driver: "local", Options: map[string]string{"root": t.TempDir()}}}})
	_, err := m.configuredDriver("local", DriverConfig{"root": t.TempDir()})
	require.Error(t, err)
	_, err = m.registry.Build("s3", DriverConfig{"endpoint": "not-an-origin"})
	require.ErrorContains(t, err, "S3 endpoint")
}

func TestBackendConfigurationBuildsExplicitReadDriversAndRejectsUnknownFactories(t *testing.T) {
	runtime := appenv.NewRuntime(appenv.NewIdentity("storage-test", "1"))
	m := NewModule(runtime, Config{ReadBackends: map[string]BackendConfig{"archive": {Driver: "local", Options: map[string]string{"root": t.TempDir()}}}})
	driver, err := m.configuredDriver("local", DriverConfig{"root": t.TempDir()})
	require.NoError(t, err)
	set, ok := driver.(*backendDriver)
	require.True(t, ok)
	require.Contains(t, set.drivers, "archive")
	_, err = set.Open(t.Context(), "objects/old")
	require.ErrorIs(t, err, ErrUnsupported, "persisted routing requires an available datasource")
	m.config.ReadBackends["archive"] = BackendConfig{Driver: "missing"}
	_, err = m.configuredDriver("local", DriverConfig{"root": t.TempDir()})
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = m.configuredDriver("missing", nil)
	require.ErrorIs(t, err, ErrUnsupported)
}

func TestBackendsAuthorizedRowReadUsesNoSecondConnection(t *testing.T) {
	db, source, target, row, body := migrationFixture(t)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	set := &backendDriver{active: "s3", drivers: map[string]Driver{"s3": target, "local": source}, prefix: "move_", db: func() *gorm.DB { return db }}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, "move_")
		stored, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(row.ID)).Take()
		if err != nil {
			return err
		}
		reader, err := openStoredObject(ctx, set, stored)
		if err != nil {
			return err
		}
		actual, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.Equal(t, body, actual)
		return reader.Close()
	})
	require.NoError(t, err)
}
