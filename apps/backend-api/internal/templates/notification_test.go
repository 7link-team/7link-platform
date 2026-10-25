package templates

import (
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/internal/templates/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestPublishedNotificationUsesCurrentPinAndTenant(t *testing.T) {
	m, db, ctx := templateHarness(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, v := range []model.Version{{ID: 1, TenantID: 1, Key: "k", Semver: "1", State: "PUBLISHED", UpdatedAt: now}, {ID: 2, TenantID: 1, Key: "k", Semver: "2", State: "PUBLISHED", UpdatedAt: now.Add(time.Second)}, {ID: 3, TenantID: 1, Key: "other", Semver: "1", State: "PUBLISHED", UpdatedAt: now}, {ID: 4, TenantID: 2, Key: "k", Semver: "1", State: "PUBLISHED", UpdatedAt: now}} {
		require.NoError(t, db.Table(m.names.Versions.String()).Create(&v).Error)
	}
	for _, b := range []model.Binding{{ID: 1, TenantID: 1, ProjectID: 11, TemplateVersionID: 1}, {ID: 3, TenantID: 1, ProjectID: 12, TemplateVersionID: 2}, {ID: 4, TenantID: 1, ProjectID: 13, TemplateVersionID: 3}, {ID: 5, TenantID: 2, ProjectID: 14, TemplateVersionID: 4}} {
		require.NoError(t, db.Table(m.names.Bindings.String()).Create(&b).Error)
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		ids, err := m.publishedNotificationProjectsOn(ctx, tx, 1, 2)
		require.NoError(t, err)
		require.Equal(t, []guid.ID{11}, ids)
		ids, err = m.publishedNotificationProjectsOn(ctx, tx, 2, 2)
		require.NoError(t, err)
		require.Empty(t, ids)
		return nil
	}))
	_, err := m.publishedNotificationProjectsOn(ctx, db, 1, 2)
	require.ErrorIs(t, err, ErrInvalid)
}
