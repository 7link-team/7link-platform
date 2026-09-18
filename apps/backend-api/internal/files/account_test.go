package files

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"7link.com/platform/internal/files/query"
	goosex "7link.com/platform/pkg/extension/goose"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAccountObjectRegistrationUsesCallerTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "account.sqlite")+"?_foreign_keys=on"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	source := (&Module{}).MigrationSource()
	source.Prefix = "account_"
	require.NoError(t, goosex.Migrate(t.Context(), sqlDB, source, "sqlite"))
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	service, err := NewAccountObjects(driver, "local", source.Prefix)
	require.NoError(t, err)
	now := time.Now()
	payload := bytes.Repeat([]byte("bounded streaming content"), 20000)
	prepared, err := service.Prepare(t.Context(), 1, "objects/1", bytes.NewReader(payload), int64(len(payload)), now)
	require.NoError(t, err)
	want := NewChecksums()
	_, err = want.Write(payload)
	require.NoError(t, err)
	require.Equal(t, want.Digests().SHA512, prepared.object.SHA512)
	require.Equal(t, want.Digests().MD5, prepared.object.MD5)
	require.Equal(t, want.Digests().SHA1, prepared.object.SHA1)
	require.Equal(t, want.Digests().SHA256, prepared.object.SHA256)
	require.NotEmpty(t, prepared.object.URI)
	rollback := errors.New("account update failed")
	err = db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, service.Register(t.Context(), tx, prepared, 2, 3, "avatar", nil))
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	q := query.Bind(db, source.Prefix)
	count, err := q.Object.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = q.AccountReference.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	expiry := now.Add(time.Hour)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return service.Register(t.Context(), tx, prepared, 2, 3, "avatar_candidate", &expiry)
	}))
	object, err := q.Object.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, prepared.object, *object)
	reference, err := q.AccountReference.WithContext(t.Context()).Take()
	require.NoError(t, err)
	require.Equal(t, expiry.UnixMilli(), *reference.ExpiresAt)
	require.Error(t, service.Register(t.Context(), db, prepared, 4, 3, "avatar_candidate", nil))
	require.Error(t, service.Register(t.Context(), db, prepared, 4, 3, "avatar", &expiry))
	other, err := NewAccountObjects(driver, "local", source.Prefix)
	require.NoError(t, err)
	require.Error(t, other.Register(t.Context(), db, prepared, 4, 3, "avatar", nil))
	var imageData bytes.Buffer
	require.NoError(t, png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 19, 23))))
	second, err := service.Prepare(t.Context(), 5, "objects/5", bytes.NewReader(imageData.Bytes()), int64(imageData.Len()), now)
	require.NoError(t, err)
	require.Equal(t, "image/png", second.object.MIME)
	require.Equal(t, 19, *second.object.Width)
	require.Equal(t, 23, *second.object.Height)
	// A reference insertion failure must also roll back the preceding object row.
	require.Error(t, db.Transaction(func(tx *gorm.DB) error {
		return service.Register(t.Context(), tx, second, 2, 3, "avatar", nil)
	}))
	count, err = q.Object.WithContext(t.Context()).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestAccountObjectPreparationValidation(t *testing.T) {
	_, err := NewAccountObjects(nil, "local", "")
	require.Error(t, err)
	driver, err := NewLocal(t.TempDir())
	require.NoError(t, err)
	_, err = NewAccountObjects(driver, "", "")
	require.Error(t, err)
	service, err := NewAccountObjects(driver, "local", "")
	require.NoError(t, err)
	_, err = service.Prepare(t.Context(), 0, "objects/1", bytes.NewReader(nil), 0, time.Now())
	require.Error(t, err)
	_, err = service.Prepare(t.Context(), 1, "../1", bytes.NewReader(nil), 0, time.Now())
	require.Error(t, err)
	_, err = service.Prepare(t.Context(), 1, "objects/1", bytes.NewReader([]byte("short")), 100, time.Now())
	require.ErrorIs(t, err, ErrLength)
	_, err = driver.Open(t.Context(), "objects/1")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.Prepare(t.Context(), 1, "objects/1", bytes.NewReader(nil), 0, time.Now())
	require.NoError(t, err)
	_, err = service.Prepare(t.Context(), 2, "objects/1", bytes.NewReader(nil), 0, time.Now())
	require.ErrorIs(t, err, ErrExists)
}
