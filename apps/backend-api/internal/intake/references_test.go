package intake

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	goosex "7link.com/platform/pkg/extension/goose"
	gormx "7link.com/platform/pkg/extension/gorm"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func referencesHarness(t *testing.T) (*Module, *gorm.DB) {
	t.Helper()
	router, err := gormx.NewDatasourceRouter(t.Context(), false, map[string]gormx.Datasource{"primary": {Type: "sqlite", DSN: filepath.Join(t.TempDir(), "intake.sqlite"), Readable: true, Writable: true}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, router.Close()) })
	db := router.Write()
	raw, err := db.DB()
	require.NoError(t, err)
	m := NewModule(appenv.NewRuntime(appenv.NewIdentity("test", "1"), appenv.WithTablePrefix("refs_")), Config{Config: module.Config{Enabled: true}})
	source := m.MigrationSource()
	source.Prefix = "refs_"
	require.NoError(t, goosex.Migrate(t.Context(), raw, source, "sqlite"))
	for id, tenant := range map[int]int{1: 7, 2: 7, 3: 8} {
		require.NoError(t, db.Table(m.names.Contacts.String()).Create(map[string]any{"id": id, "tenant_id": tenant, "display_name": "Contact", "note": "private", "created_at": time.Now(), "updated_at": time.Now()}).Error)
	}
	return m, db
}
func TestContactReferencesAreTenantBoundAndTransactional(t *testing.T) {
	m, db := referencesHarness(t)
	abort := errors.New("rollback")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		ids := []guid.ID{3, 2, 1, 2, 999}
		rows, err := m.ContactsOn(t.Context(), tx, 7, ids)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		require.Equal(t, guid.ID(1), rows[0].ID)
		require.Equal(t, guid.ID(2), rows[1].ID)
		require.Equal(t, []guid.ID{3, 2, 1, 2, 999}, ids)
		require.Equal(t, "ACTIVE", rows[0].State)
		require.Equal(t, "PERSON", rows[0].Kind)
		require.NoError(t, tx.Table(m.names.Contacts.String()).Where("id = ?", 1).Updates(map[string]any{"state": "BLOCKED", "archived_at": time.Now()}).Error)
		rows, err = m.ContactsOn(t.Context(), tx, 7, []guid.ID{1})
		require.NoError(t, err)
		require.Equal(t, "BLOCKED", rows[0].State)
		require.NotNil(t, rows[0].ArchivedAt)
		empty, err := m.ContactsOn(t.Context(), tx, 7, nil)
		require.NoError(t, err)
		require.NotNil(t, empty)
		require.Empty(t, empty)
		return abort
	}), abort)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		rows, err := m.ContactsOn(t.Context(), tx, 7, []guid.ID{1})
		require.NoError(t, err)
		require.Equal(t, "ACTIVE", rows[0].State)
		require.Nil(t, rows[0].ArchivedAt)
		return nil
	}))
}
func TestContactReferenceErrorsAndProvider(t *testing.T) {
	m, db := referencesHarness(t)
	require.Equal(t, "intake", m.ModuleName())
	require.True(t, m.Enabled())
	require.False(t, (*Module)(nil).Enabled())
	services := module.NewServices()
	require.NoError(t, m.ProvideServices(services))
	require.Error(t, m.ProvideServices(services))
	require.NoError(t, services.Seal())
	reader, err := module.Resolve[IntakeReader](services)
	require.NoError(t, err)
	require.NotNil(t, reader.ContactsOn)
	require.NotNil(t, reader.GetOn)
	_, err = (*Module)(nil).ContactsOn(t.Context(), nil, 7, nil)
	require.ErrorIs(t, err, ErrUnavailable)
	_, err = m.ContactsOn(t.Context(), db, 7, nil)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, err = m.ContactsOn(t.Context(), nil, 7, nil)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, err := m.ContactsOn(t.Context(), tx, 0, nil)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, err = m.ContactsOn(t.Context(), tx, 7, []guid.ID{1, -1})
		require.ErrorIs(t, err, ErrInvalidArgument)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = m.ContactsOn(ctx, tx, 7, nil)
		require.ErrorIs(t, err, context.Canceled)
		require.NoError(t, tx.Migrator().DropTable(m.names.Contacts.String()))
		_, err = m.ContactsOn(t.Context(), tx, 7, []guid.ID{1})
		require.Error(t, err)
		return nil
	}))
}

func TestIntakeRecordReferencesUseTenantAndCallerTransaction(t *testing.T) {
	m, db := referencesHarness(t)
	now := time.Now().UTC()
	require.NoError(t, db.Table(m.names.Records.String()).Create(map[string]any{"id": 10, "tenant_id": 7, "ref_no": 1, "channel_id": 3, "channel_kind": "SHARED_CHANNEL", "contact_id": 1, "title": "Source requirement", "raw_content": "private source", "raw_content_hash": strings.Repeat("a", 64), "received_at": now, "reply_token": "unique-token", "created_at": now, "updated_at": now}).Error)
	abort := errors.New("rollback linkage")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		ref, found, err := m.GetOn(t.Context(), tx, 7, 10)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "Source requirement", ref.Title)
		require.Equal(t, "RECEIVED", ref.Status)
		require.Equal(t, guid.ID(1), ref.ContactID)
		require.Nil(t, ref.ProjectID)
		for _, ids := range [][2]guid.ID{{8, 10}, {7, 999}} {
			ref, found, err = m.GetOn(t.Context(), tx, ids[0], ids[1])
			require.NoError(t, err)
			require.False(t, found)
			require.Zero(t, ref)
		}
		require.NoError(t, tx.Table(m.names.Records.String()).Where("id = ?", 10).Update("project_id", 20).Error)
		ref, found, err = m.GetOn(t.Context(), tx, 7, 10)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, guid.ID(20), *ref.ProjectID)
		return abort
	}), abort)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ref, found, err := m.GetOn(t.Context(), tx, 7, 10)
		require.NoError(t, err)
		require.True(t, found)
		require.Nil(t, ref.ProjectID)
		return nil
	}))
	_, _, err := m.GetOn(t.Context(), db, 7, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	_, _, err = (*Module)(nil).GetOn(t.Context(), nil, 7, 10)
	require.ErrorIs(t, err, ErrUnavailable)
	_, _, err = m.GetOn(t.Context(), nil, 7, 10)
	require.ErrorIs(t, err, ErrInvalidArgument)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		_, _, err := m.GetOn(t.Context(), tx, 0, 10)
		require.ErrorIs(t, err, ErrInvalidArgument)
		_, _, err = m.GetOn(t.Context(), tx, 7, 0)
		require.ErrorIs(t, err, ErrInvalidArgument)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err = m.GetOn(ctx, tx, 7, 10)
		require.ErrorIs(t, err, context.Canceled)
		require.NoError(t, tx.Migrator().DropTable(m.names.Records.String()))
		_, _, err = m.GetOn(t.Context(), tx, 7, 10)
		require.Error(t, err)
		return nil
	}))
}
