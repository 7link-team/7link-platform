package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAccountCleanupHarness(t *testing.T) (*gorm.DB, *AccountObjects, *Local) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cleanup.sqlite")+"?_foreign_keys=on&_busy_timeout=10000&_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	source := (&Module{}).MigrationSource()
	source.Prefix = "cleanup_"
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	accounts, err := NewAccountObjects(driver, "local", source.Prefix)
	require.NoError(t, err)
	return db, accounts, driver
}

func TestStagedAccountObjectSurvivesRollbackAndIsCollected(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/1.png", now)
	require.NoError(t, err)
	require.False(t, readable)
	rollback := errors.New("audit failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, accounts.ReplaceAvatar(t.Context(), tx, prepared, 2, 3))
		return rollback
	}), rollback)
	q := query.Bind(db, accounts.prefix)
	object, err := q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "staged", object.State)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "ready", intent.State)
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Hour), 0, 100)
	require.NoError(t, err)
	_, err = driver.Open(t.Context(), intent.StorageKey)
	require.ErrorIs(t, err, ErrNotFound)
	object, err = q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "deleted", object.State)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, prepared, 2, 3) }))
}

func TestLiveAvatarProtectedReplacementAndCandidateExpiry(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	first, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("one"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, first, 2, 4) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, first, 2, 3) }))
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Hour), 0, 100)
	require.NoError(t, err)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/1.png", now)
	require.NoError(t, err)
	require.True(t, readable)
	readable, err = accounts.AccountKeyReadable(t.Context(), db, 4, "avatars/3/1.png", now)
	require.NoError(t, err)
	require.False(t, readable)
	second, err := accounts.Stage(t.Context(), db, 5, 3, "avatars/3/5.png", bytes.NewBufferString("two"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, second, 6, 3) }))
	candidate, err := accounts.Stage(t.Context(), db, 7, 3, "avatars/3/7.png", bytes.NewBufferString("new"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	expiry := now.Add(time.Minute)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.Register(t.Context(), tx, candidate, 8, 3, "avatar_candidate", &expiry)
	}))
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Minute), 0, 100)
	require.NoError(t, err)
	for _, key := range []string{"avatars/3/1.png", "avatars/3/7.png"} {
		_, err := driver.Open(t.Context(), key)
		require.ErrorIs(t, err, ErrNotFound)
	}
	readable, err = accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/5.png", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, readable)
}

type cleanupFailureDriver struct {
	Driver
	fail bool
}

func (d *cleanupFailureDriver) Delete(ctx context.Context, key string) error {
	if d.fail {
		return errors.New("storage offline")
	}
	return d.Driver.Delete(ctx, key)
}

func TestCleanupFailureRetriesWithoutRevivingObject(t *testing.T) {
	db, accounts, local := newAccountCleanupHarness(t)
	driver := &cleanupFailureDriver{Driver: local, fail: true}
	accounts.driver = driver
	now := time.Now()
	prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(2*time.Hour), 0, 100)
	require.ErrorContains(t, err, "storage offline")
	require.Error(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReplaceAvatar(t.Context(), tx, prepared, 2, 3) }))
	driver.fail = false
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(3*time.Hour), 0, 100)
	require.NoError(t, err)
	q := query.Bind(db, accounts.prefix)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "released", intent.State)
}

func TestStageCollisionNeverDeletesPreexistingBytes(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	_, err := driver.Put(t.Context(), "avatars/3/1.png", bytes.NewBufferString("old"), 3)
	require.NoError(t, err)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("new"), 3, time.Now().Add(time.Hour))
	require.ErrorIs(t, err, ErrExists)
	_, err = accounts.CollectAccountObjects(t.Context(), db, time.Now().Add(2*time.Hour), 0, 100)
	require.NoError(t, err)
	reader, err := driver.Open(t.Context(), "avatars/3/1.png")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "old", string(data))
}

func TestAbandonedLocalWriteReclaimsPendingAndKeepsTombstone(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	key := "avatars/3/1.png"
	q := query.Bind(db, accounts.prefix)
	require.NoError(t, q.AccountPreparation.WithContext(t.Context()).Create(&model.AccountPreparation{ID: 1, PrincipalID: 3, BackendID: "local", StorageKey: key, State: "writing", ExpiresAt: now.Add(-time.Hour).UnixMilli(), CreatedAt: now.Add(-2 * time.Hour).UnixMilli()}))
	require.NoError(t, os.MkdirAll(filepath.Join(driver.directory, "avatars/3"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(driver.directory, localPendingKey(key)), []byte("partial"), 0600))
	_, err := accounts.CollectAccountObjects(t.Context(), db, now, 0, 100)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(driver.directory, localPendingKey(key)))
	require.ErrorIs(t, err, os.ErrNotExist)
	intent, err := q.AccountPreparation.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, "abandoned", intent.State)
	// A delayed remote-style write cannot gain a reference; the next sweep
	// removes any bytes that appeared after the first reclamation.
	_, err = driver.Put(t.Context(), key, bytes.NewBufferString("late"), 4)
	require.NoError(t, err)
	_, err = accounts.CollectAccountObjects(t.Context(), db, now.Add(time.Hour), 0, 100)
	require.NoError(t, err)
	_, err = driver.Open(t.Context(), key)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAccountTransactionBoundariesAndCandidatePromotion(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
	require.NoError(t, err)
	require.ErrorIs(t, accounts.Register(t.Context(), db, prepared, 2, 3, "avatar", nil), ErrAccountTransaction)
	require.ErrorIs(t, accounts.UseAvatarCandidate(t.Context(), db, 3, 2, "avatars/3/1.png", now), ErrAccountTransaction)
	require.ErrorIs(t, accounts.ReleaseAccountResources(t.Context(), db, 3, now), ErrAccountTransaction)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := accounts.Stage(t.Context(), tx, 4, 3, "avatars/3/4.png", bytes.NewReader(nil), 0, now.Add(time.Hour))
		require.ErrorIs(t, err, ErrAccountTransaction)
		return nil
	}))
	expiry := now.Add(time.Minute)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.Register(t.Context(), tx, prepared, 2, 3, "avatar_candidate", &expiry)
	}))
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.UseAvatarCandidate(t.Context(), tx, 4, 5, "avatars/3/1.png", now)
	}), ErrNotFound)
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.UseAvatarCandidate(t.Context(), tx, 3, 5, "avatars/3/1.png", expiry.Add(time.Second))
	}), ErrNotFound)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return accounts.UseAvatarCandidate(t.Context(), tx, 3, 5, "avatars/3/1.png", now)
	}))
	rollback := errors.New("erasure failed")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, accounts.ReleaseAccountResources(t.Context(), tx, 3, now))
		return rollback
	}), rollback)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/1.png", expiry.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, readable, "current reference has no candidate expiry")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return accounts.ReleaseAccountResources(t.Context(), tx, 3, now) }))
	_, err = accounts.CollectAccountObjects(t.Context(), db, now, 0, 100)
	require.NoError(t, err)
	_, err = driver.Open(t.Context(), "avatars/3/1.png")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStageFailureValidationAndCleanupCursor(t *testing.T) {
	db, accounts, _ := newAccountCleanupHarness(t)
	now := time.Now()
	_, err := accounts.Stage(t.Context(), nil, 1, 3, "avatars/3/1.png", bytes.NewReader(nil), 0, now.Add(time.Hour))
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewReader(nil), 0, now.Add(-time.Hour))
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewBufferString("short"), 100, now.Add(time.Hour))
	require.ErrorIs(t, err, ErrLength)
	_, err = accounts.Stage(t.Context(), db, 1, 3, "avatars/3/1.png", bytes.NewReader(nil), 0, now.Add(time.Hour))
	require.Error(t, err, "the durable key must never be reused")
	_, err = accounts.CollectAccountObjects(t.Context(), db, now, 0, 0)
	require.ErrorIs(t, err, ErrInvalidKey)
	after, err := accounts.CollectAccountObjects(t.Context(), db, now, 0, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, after)
	after, err = accounts.CollectAccountObjects(t.Context(), db, now, after, 1)
	require.NoError(t, err)
	require.Zero(t, after)
	readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/missing.png", now)
	require.NoError(t, err)
	require.False(t, readable)
}

func TestAccountObjectAdoptionValidatesAndRollsBackDatabaseFailures(t *testing.T) {
	db, accounts, driver := newAccountCleanupHarness(t)
	now := time.Now()
	deadline := now.Add(time.Hour)
	_, err := driver.Put(t.Context(), "avatars/3/adopt.png", bytes.NewBufferString("abc"), 3)
	require.NoError(t, err)

	var nilAccounts *AccountObjects
	_, err = nilAccounts.Adopt(t.Context(), db, 1, 3, "avatars/3/adopt.png", deadline)
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = accounts.Adopt(t.Context(), db, 0, 3, "avatars/3/adopt.png", deadline)
	require.ErrorIs(t, err, ErrInvalidKey)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, adoptErr := accounts.Adopt(t.Context(), tx, 1, 3, "avatars/3/adopt.png", deadline)
		require.ErrorIs(t, adoptErr, ErrAccountTransaction)
		return nil
	}))
	_, err = accounts.Adopt(t.Context(), db, 1, 3, "avatars/3/missing.png", deadline)
	require.ErrorIs(t, err, ErrNotFound)

	for _, table := range []string{"preparations", "objects"} {
		t.Run(table, func(t *testing.T) {
			db, accounts, driver := newAccountCleanupHarness(t)
			_, putErr := driver.Put(t.Context(), "avatars/3/adopt.png", bytes.NewBufferString("abc"), 3)
			require.NoError(t, putErr)
			names := query.BindNames(accounts.prefix)
			tableName := names.AccountPreparations.String()
			if table == "objects" {
				tableName = names.Objects.String()
			}
			fault := failTableAfter(t, db, tableName, 0)
			prepared, adoptErr := accounts.Adopt(t.Context(), db, 1, 3, "avatars/3/adopt.png", time.Now().Add(time.Hour))
			require.True(t, fault.Fired())
			require.ErrorIs(t, adoptErr, errInjectedTableFault)
			require.Nil(t, prepared)
			if table == "preparations" {
				count, countErr := query.Bind(db, accounts.prefix).Object.WithContext(t.Context()).Count()
				require.NoError(t, countErr)
				require.Zero(t, count)
			} else {
				count, countErr := query.Bind(db, accounts.prefix).AccountPreparation.WithContext(t.Context()).Count()
				require.NoError(t, countErr)
				require.Zero(t, count)
			}
			reader, openErr := driver.Open(t.Context(), "avatars/3/adopt.png")
			require.NoError(t, openErr)
			body, readErr := io.ReadAll(reader)
			require.NoError(t, readErr)
			require.NoError(t, reader.Close())
			require.Equal(t, "abc", string(body))
		})
	}
}

func TestAttachFileObjectDatabaseFailuresLeavePreparationReady(t *testing.T) {
	for _, table := range []string{"preparations", "objects"} {
		t.Run(table, func(t *testing.T) {
			db, accounts, _ := newAccountCleanupHarness(t)
			prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/staged.png", bytes.NewBufferString("abc"), 3, time.Now().Add(time.Hour))
			require.NoError(t, err)
			names := query.BindNames(accounts.prefix)
			tableName := names.AccountPreparations.String()
			if table == "objects" {
				tableName = names.Objects.String()
			}
			fault := failTableAfter(t, db, tableName, 0)
			err = db.Transaction(func(tx *gorm.DB) error {
				_, attachErr := accounts.attachFileObject(t.Context(), tx, prepared, 3)
				return attachErr
			})
			require.True(t, fault.Fired())
			require.ErrorIs(t, err, errInjectedTableFault)
			q := query.Bind(db, accounts.prefix)
			if table == "preparations" {
				object, objectErr := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(guid.ID(1))).Take()
				require.NoError(t, objectErr)
				require.Equal(t, "staged", object.State)
			} else {
				intent, intentErr := q.AccountPreparation.WithContext(t.Context()).Where(q.AccountPreparation.ID.Eq(guid.ID(1))).Take()
				require.NoError(t, intentErr)
				require.Equal(t, "ready", intent.State)
			}
		})
	}

	db, accounts, _ := newAccountCleanupHarness(t)
	var nilAccounts *AccountObjects
	_, err := nilAccounts.attachFileObject(t.Context(), db, &PreparedAccountObject{}, 3)
	require.ErrorIs(t, err, ErrInvalidKey)
	_, err = accounts.attachFileObject(t.Context(), db, &PreparedAccountObject{owner: accounts}, 3)
	require.ErrorIs(t, err, ErrInvalidKey)
}

func TestUseAvatarCandidateDatabaseFailuresRollBackEveryStep(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table func(query.TableNames) string
		skip  int64
	}{
		{name: "object lookup", table: func(names query.TableNames) string { return names.Objects.String() }},
		{name: "object fence", table: func(names query.TableNames) string { return names.Objects.String() }, skip: 1},
		{name: "candidate lookup", table: func(names query.TableNames) string { return names.AccountReferences.String() }},
		{name: "candidate retirement", table: func(names query.TableNames) string { return names.AccountReferences.String() }, skip: 1},
		{name: "current retirement", table: func(names query.TableNames) string { return names.AccountReferences.String() }, skip: 2},
		{name: "current creation", table: func(names query.TableNames) string { return names.AccountReferences.String() }, skip: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, accounts, _ := newAccountCleanupHarness(t)
			now := time.Now()
			prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/candidate.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
			require.NoError(t, err)
			expires := now.Add(30 * time.Minute)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				return accounts.Register(t.Context(), tx, prepared, 2, 3, "avatar_candidate", &expires)
			}))
			fault := failTableAfter(t, db, tc.table(query.BindNames(accounts.prefix)), tc.skip)
			err = db.Transaction(func(tx *gorm.DB) error {
				return accounts.UseAvatarCandidate(t.Context(), tx, 3, 5, "avatars/3/candidate.png", now)
			})
			require.True(t, fault.Fired())
			require.ErrorIs(t, err, errInjectedTableFault)
			if tc.table(query.BindNames(accounts.prefix)) == query.BindNames(accounts.prefix).Objects.String() {
				candidate, candidateErr := query.Bind(db, accounts.prefix).AccountReference.WithContext(t.Context()).Where(query.Bind(db, accounts.prefix).AccountReference.ID.Eq(guid.ID(2))).Take()
				require.NoError(t, candidateErr)
				require.Equal(t, "active", candidate.State)
			} else {
				object, objectErr := query.Bind(db, accounts.prefix).Object.WithContext(t.Context()).Where(query.Bind(db, accounts.prefix).Object.ID.Eq(guid.ID(1))).Take()
				require.NoError(t, objectErr)
				require.EqualValues(t, 1, object.Version)
			}
		})
	}
}

func TestAccountObjectReadAndReleasePropagateDatabaseFailures(t *testing.T) {
	for _, table := range []string{"objects", "references"} {
		t.Run("read "+table, func(t *testing.T) {
			db, accounts, _ := newAccountCleanupHarness(t)
			now := time.Now()
			prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/avatar.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
			require.NoError(t, err)
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				return accounts.Register(t.Context(), tx, prepared, 2, 3, "avatar", nil)
			}))
			names := query.BindNames(accounts.prefix)
			tableName := names.Objects.String()
			if table == "references" {
				tableName = names.AccountReferences.String()
			}
			fault := failTableAfter(t, db, tableName, 0)
			readable, err := accounts.AccountKeyReadable(t.Context(), db, 3, "avatars/3/avatar.png", now)
			require.True(t, fault.Fired())
			require.ErrorIs(t, err, errInjectedTableFault)
			require.False(t, readable)
		})
	}

	t.Run("release preparations", func(t *testing.T) {
		db, accounts, _ := newAccountCleanupHarness(t)
		now := time.Now()
		prepared, err := accounts.Stage(t.Context(), db, 1, 3, "avatars/3/avatar.png", bytes.NewBufferString("abc"), 3, now.Add(time.Hour))
		require.NoError(t, err)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return accounts.Register(t.Context(), tx, prepared, 2, 3, "avatar", nil)
		}))
		fault := failTableAfter(t, db, query.BindNames(accounts.prefix).AccountPreparations.String(), 0)
		err = db.Transaction(func(tx *gorm.DB) error {
			return accounts.ReleaseAccountResources(t.Context(), tx, 3, now)
		})
		require.True(t, fault.Fired())
		require.ErrorIs(t, err, errInjectedTableFault)
		ref, refErr := query.Bind(db, accounts.prefix).AccountReference.WithContext(t.Context()).Where(query.Bind(db, accounts.prefix).AccountReference.ID.Eq(guid.ID(2))).Take()
		require.NoError(t, refErr)
		require.Equal(t, "active", ref.State)
	})

	db, accounts, _ := newAccountCleanupHarness(t)
	require.ErrorIs(t, accounts.ReleaseAccountResources(t.Context(), nil, 3, time.Now()), ErrInvalidKey)
	_, err := accounts.AccountKeyReadable(t.Context(), db, 0, "avatars/3/avatar.png", time.Now())
	require.ErrorIs(t, err, ErrInvalidKey)
}
