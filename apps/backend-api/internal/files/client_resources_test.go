package files

import (
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"testing"
	"time"
)

func TestClientPackageUnpinStorageFailuresPreserveActiveBinding(t *testing.T) {
	for _, scenario := range []string{"binding", "space", "owner", "purpose", "unbind", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			h, p, input, _ := pinnedFixture(t)
			input.Purpose, input.ResourceKind = "clientwork.package", "client_package"
			require.NoError(t, p.module.bindings.RegisterPurpose(ResourcePurpose{Name: input.Purpose, ResourceKind: input.ResourceKind, OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }}))
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := p.PinOn(h.ctx, tx, input); return err }))
			fault := errors.New("package unpin storage failure")
			names := query.BindNames(h.tree.prefix)
			table := names.ResourceBindings.String()
			remove := func() {}
			switch scenario {
			case "space":
				table = names.Spaces.String()
			case "owner":
				table = names.Nodes.String()
			case "purpose":
				delete(p.module.bindings.purposes, input.Purpose)
			case "audit":
				p.module.operations.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			}
			if scenario == "unbind" {
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("test:package-unpin", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Update().Remove("test:package-unpin")) }
			} else if scenario != "purpose" && scenario != "audit" {
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:package-unpin", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Query().Remove("test:package-unpin")) }
			}
			err := h.db.Transaction(func(tx *gorm.DB) error {
				return p.module.unpinClientPackageResourceOn(h.ctx, tx, input.ID, input.ResourceID)
			})
			remove()
			if scenario == "purpose" {
				require.ErrorIs(t, err, errResourcePurpose)
			} else {
				require.ErrorIs(t, err, fault)
			}
			q := query.Bind(h.db, h.tree.prefix)
			binding, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.ID.Eq(input.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "active", binding.State)
			require.EqualValues(t, 1, binding.Version)
			h.assertAudit(t, 1)
		})
	}
}

func TestClientCollaborationPackageUnpinAuthorizationAndRollback(t *testing.T) {
	h, p, input, allowed := pinnedFixture(t)
	input.Purpose, input.ResourceKind = "clientwork.package", "client_package"
	require.NoError(t, p.module.bindings.RegisterPurpose(ResourcePurpose{Name: input.Purpose, ResourceKind: input.ResourceKind, OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, _ *gorm.DB, id guid.ID, owner ResourceOwner) error {
		claims, ok := authn.FromContext(ctx)
		if !ok || claims.PrincipalID != 70 || id != input.ResourceID || owner.ID != 80 || !*allowed {
			return errOperationForbidden
		}
		return nil
	}}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.PinOn(h.ctx, tx, input); return e }))
	h.assertAudit(t, 1)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		return p.module.unpinClientPackageResourceOn(h.ctx, tx, input.ID, input.ResourceID+1)
	}))
	*allowed = false
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		return p.module.unpinClientPackageResourceOn(h.ctx, tx, input.ID, input.ResourceID)
	}), errOperationForbidden)
	*allowed = true
	rollback := errors.New("package update failed after unpin")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, p.module.unpinClientPackageResourceOn(h.ctx, tx, input.ID, input.ResourceID))
		return rollback
	}), rollback)
	h.assertAudit(t, 1)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		read, e := p.module.openClientCollaborationResourceOn(h.ctx, tx, input.ID)
		if e != nil {
			return e
		}
		return read.Reader.Close()
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return p.module.unpinClientPackageResourceOn(h.ctx, tx, input.ID, input.ResourceID)
	}))
	h.assertAudit(t, 3)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := p.module.openClientCollaborationResourceOn(h.ctx, tx, input.ID)
		return e
	}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		return p.module.unpinClientPackageResourceOn(h.ctx, tx, input.ID, input.ResourceID)
	}))
	h.assertAudit(t, 3)
}

func TestClientCollaborationPinnedFilesUseCurrentPurposeAuthorization(t *testing.T) {
	h, p, input, allowed := pinnedFixture(t)
	input.Purpose = "clientwork.preview"
	input.ResourceKind = "client_preview"
	require.NoError(t, p.module.bindings.RegisterPurpose(ResourcePurpose{Name: input.Purpose, ResourceKind: input.ResourceKind, OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, _ *gorm.DB, id guid.ID, owner ResourceOwner) error {
		claims, ok := authn.FromContext(ctx)
		if !ok || claims.PrincipalID != 70 || id != input.ResourceID || owner.ID != 80 || !*allowed {
			return errOperationForbidden
		}
		return nil
	}}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.PinOn(h.ctx, tx, input); return e }))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		read, e := p.module.openClientCollaborationResourceOn(h.ctx, tx, input.ID)
		require.NoError(t, e)
		data, e := io.ReadAll(read.Reader)
		require.NoError(t, e)
		require.NoError(t, read.Reader.Close())
		require.Equal(t, "old", string(data))
		require.Equal(t, *input.VersionID, read.VersionID)
		*allowed = false
		_, e = p.module.openClientCollaborationResourceOn(h.ctx, tx, input.ID)
		require.Error(t, e)
		*allowed = true
		require.NoError(t, p.module.bindings.Unbind(h.ctx, tx, input.SpaceID, input.ID, 1, time.Now()))
		_, e = p.module.openClientCollaborationResourceOn(h.ctx, tx, input.ID)
		require.Error(t, e)
		return nil
	}))
	_, e := p.module.openClientCollaborationResourceOn(h.ctx, h.db, input.ID)
	require.Error(t, e)
}

func TestClientPinnedReadStorageFailuresReturnNoReader(t *testing.T) {
	for _, scenario := range []string{"binding", "space", "reread", "owner", "version", "object", "audit", "purpose", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			h, p, input, _ := pinnedFixture(t)
			input.Purpose, input.ResourceKind = "clientwork.preview", "client_preview"
			require.NoError(t, p.module.bindings.RegisterPurpose(ResourcePurpose{Name: input.Purpose, ResourceKind: input.ResourceKind, OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }}))
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := p.PinOn(h.ctx, tx, input); return err }))
			fault := errors.New("pinned storage dependency unavailable")
			names := query.BindNames(h.tree.prefix)
			table := names.ResourceBindings.String()
			nth := 1
			remove := func() {}
			switch scenario {
			case "space":
				table = names.Spaces.String()
			case "reread":
				nth = 2
			case "owner":
				table = names.Nodes.String()
			case "version":
				table = names.Versions.String()
			case "object":
				table = names.Objects.String()
			case "audit":
				p.module.operations.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "purpose":
				p.module.bindings.mu.Lock()
				delete(p.module.bindings.purposes, input.Purpose)
				p.module.bindings.mu.Unlock()
			case "expired":
				require.NoError(t, h.db.Table(names.ResourceBindings.String()).Where("id = ?", input.ID).Update("expires_at", time.Now().Add(-time.Minute).UnixMilli()).Error)
			}
			if scenario != "audit" && scenario != "purpose" && scenario != "expired" {
				reads := 0
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:pinned-read", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						reads++
						if reads == nth {
							tx.AddError(fault)
						}
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Query().Remove("test:pinned-read")) }
			}
			var read PinnedResourceRead
			err := h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				read, err = p.module.openClientCollaborationResourceOn(h.ctx, tx, input.ID)
				return err
			})
			remove()
			require.Nil(t, read.Reader)
			if scenario == "purpose" {
				require.ErrorIs(t, err, errResourcePurpose)
			} else if scenario == "expired" {
				require.ErrorIs(t, err, ErrNotFound)
			} else {
				require.ErrorIs(t, err, fault)
			}
			h.assertAudit(t, 1)
		})
	}
}
