package files

import (
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"strings"
	"testing"
	"time"
)

func TestTenantResourceCreateStorageFailuresRollbackPreparedAttachment(t *testing.T) {
	for _, scenario := range []string{"root", "node", "acl", "version", "binding", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			h, p, _ := tenantFilesFixture(t)
			now := time.Now()
			prep, err := p.StageOn(h.ctx, h.db, 90, "reports.export", strings.NewReader("report"), 6, now.Add(time.Hour))
			require.NoError(t, err)
			fault := errors.New("tenant file storage unavailable")
			names := query.BindNames(h.tree.prefix)
			remove := func() {}
			if scenario == "audit" {
				p.module.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			} else if scenario == "root" {
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:tenant-create", func(tx *gorm.DB) {
					if tx.Statement.Table == names.Spaces.String() {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Query().Remove("test:tenant-create")) }
			} else {
				table := names.Nodes.String()
				switch scenario {
				case "acl":
					table = names.NodeACL.String()
				case "version":
					table = names.Versions.String()
				case "binding":
					table = names.ResourceBindings.String()
				}
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("test:tenant-create", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Create().Remove("test:tenant-create")) }
			}
			err = h.db.Transaction(func(tx *gorm.DB) error { _, err := p.CreateOn(h.ctx, tx, tenantInput(prep), now); return err })
			remove()
			require.ErrorIs(t, err, fault)
			q := query.Bind(h.db, h.tree.prefix)
			intent, err := q.AccountPreparation.WithContext(h.ctx).Where(q.AccountPreparation.ID.Eq(prep.object.object.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ready", intent.State)
			n, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.Purpose.Eq("reports.export")).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestTenantResourceReadStorageAndCurrentBindingFailuresReturnNoReader(t *testing.T) {
	for _, scenario := range []string{"binding", "space", "reread", "owner", "version", "object", "audit", "purpose", "kind", "expired", "anonymous"} {
		t.Run(scenario, func(t *testing.T) {
			h, p, _ := tenantFilesFixture(t)
			now := time.Now()
			prep, err := p.StageOn(h.ctx, h.db, 90, "reports.export", strings.NewReader("report"), 6, now.Add(time.Hour))
			require.NoError(t, err)
			var pin PinnedResource
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				pin, err = p.CreateOn(h.ctx, tx, tenantInput(prep), now)
				return err
			}))
			fault := errors.New("tenant read dependency unavailable")
			names := query.BindNames(h.tree.prefix)
			table, nth := names.ResourceBindings.String(), 1
			ctx := h.ctx
			remove := func() {}
			switch scenario {
			case "space":
				table = names.Spaces.String()
			case "reread":
				nth = 3
			case "owner":
				table = names.Nodes.String()
			case "version":
				table = names.Versions.String()
			case "object":
				table = names.Objects.String()
			case "audit":
				p.module.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "purpose":
				delete(p.purposes, "reports.export")
			case "kind":
				v := p.purposes["reports.export"]
				v.ResourceKind = "different"
				p.purposes["reports.export"] = v
			case "expired":
				require.NoError(t, h.db.Table(names.ResourceBindings.String()).Where("id = ?", pin.BindingID).Update("expires_at", now.Add(-time.Minute).UnixMilli()).Error)
			case "anonymous":
				ctx = context.Background()
			}
			if scenario == "binding" || scenario == "space" || scenario == "reread" || scenario == "owner" || scenario == "version" || scenario == "object" {
				reads := 0
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:tenant-read", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						reads++
						if reads == nth {
							tx.AddError(fault)
						}
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Query().Remove("test:tenant-read")) }
			}
			read, err := p.Open(ctx, h.db, 90, pin.FileID)
			remove()
			require.Nil(t, read.Reader)
			switch scenario {
			case "purpose", "kind":
				require.ErrorIs(t, err, errResourcePurpose)
			case "expired":
				require.ErrorIs(t, err, ErrNotFound)
			case "anonymous":
				require.ErrorIs(t, err, errOperationForbidden)
			default:
				require.ErrorIs(t, err, fault)
			}
			h.assertAudit(t, 1)
		})
	}
}

func tenantFilesFixture(t *testing.T) (operationServiceHarness, *TenantResourceFiles, *bool) {
	h := newOperationServiceHarness(t)
	bindings, err := NewResourceBindings(h.tree.prefix)
	require.NoError(t, err)
	m := &Module{runtime: h.service.runtime, accounts: h.service.objects, driver: h.service.driver, bindings: bindings, ids: h.service.ids, appender: h.service.appender, references: func(context.Context, *gorm.DB, guid.ID, core.AuthorizationReferences) (bool, error) { return true, nil }}
	services := module.NewServices()
	require.NoError(t, m.provideTenantResourceFiles(services))
	services.Seal()
	p, err := module.Resolve[*TenantResourceFiles](services)
	require.NoError(t, err)
	allowed := true
	purpose := TenantPurpose{Name: "reports.export", Module: "reports", ResourceKind: "report", MaxSize: 1024, AllowedMIME: []string{"text/plain"}, AllowExpiry: true, Authorize: func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, action string) error {
		require.NotEmpty(t, action)
		_, active := tx.Statement.ConnPool.(gorm.TxCommitter)
		require.True(t, active)
		if !allowed || authn.PrincipalIDFromContext(ctx) != 70 || tenant != 90 || (id != 0 && id != 4000 && id != 4001) {
			return errOperationForbidden
		}
		return nil
	}}
	require.NoError(t, p.RegisterPurpose(purpose))
	require.Error(t, p.RegisterPurpose(purpose))
	purpose.Name = "bad"
	purpose.MaxSize = 101 << 20
	require.Error(t, p.RegisterPurpose(purpose))
	return h, p, &allowed
}
func tenantInput(prep *PreparedTenantObject) TenantResourceInput {
	return TenantResourceInput{TenantID: 90, ResourceID: 4000, Purpose: "reports.export", ResourceKind: "report", Name: "report.txt", MIME: "text/plain", Prepared: prep}
}

func TestTenantPurposeRejectsUnsafeDirectoriesMIMEAndConflictingBindingRegistry(t *testing.T) {
	_, p, _ := tenantFilesFixture(t)
	base := p.purposes["reports.export"]
	for _, scenario := range []string{"directory", "mime", "conflict"} {
		v := base
		v.Name = "reports.other"
		switch scenario {
		case "directory":
			v.Directory = "../private"
		case "mime":
			v.AllowedMIME = []string{"text/*"}
		case "conflict":
			require.NoError(t, p.module.bindings.RegisterPurpose(ResourcePurpose{Name: v.Name, ResourceKind: v.ResourceKind, OwnerScopes: []string{"tenant"}, VersionMode: "pinned", ValidateResource: func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }}))
		}
		require.ErrorIs(t, p.RegisterPurpose(v), errResourcePurpose)
		_, err := p.purpose(v.Name)
		require.ErrorIs(t, err, errResourcePurpose)
	}
}

func TestTenantResourceIdentifierFailuresPreserveStagedObject(t *testing.T) {
	for _, available := range []int64{0, 1, 40, 41, 42} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			h, p, _ := tenantFilesFixture(t)
			now := time.Now()
			prep, err := p.StageOn(h.ctx, h.db, 90, "reports.export", strings.NewReader("report"), 6, now.Add(time.Hour))
			require.NoError(t, err)
			ids := &failingIDs{}
			ids.remaining.Store(available)
			ids.next.Store(50000)
			p.module.ids = ids
			err = h.db.Transaction(func(tx *gorm.DB) error { _, err := p.CreateOn(h.ctx, tx, tenantInput(prep), now); return err })
			require.ErrorIs(t, err, ErrUnsupported)
			q := query.Bind(h.db, h.tree.prefix)
			intent, err := q.AccountPreparation.WithContext(h.ctx).Where(q.AccountPreparation.ID.Eq(prep.object.object.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ready", intent.State)
			n, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.Purpose.Eq("reports.export")).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestTenantResourceRebindFailurePreservesOriginalResourceAndRevision(t *testing.T) {
	for _, scenario := range []string{"kind", "resource", "authority", "binding_write", "space_write", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			h, p, _ := tenantFilesFixture(t)
			now := time.Now()
			prep, err := p.StageOn(h.ctx, h.db, 90, "reports.export", strings.NewReader("report"), 6, now.Add(time.Hour))
			require.NoError(t, err)
			var pin PinnedResource
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				var err error
				pin, err = p.CreateOn(h.ctx, tx, tenantInput(prep), now)
				return err
			}))
			fault := errors.New("rebind dependency unavailable")
			kind, resource := "report", guid.ID(4001)
			remove := func() {}
			switch scenario {
			case "kind":
				kind = "wrong"
			case "resource":
				resource = 0
			case "authority":
				v := p.purposes["reports.export"]
				original := v.Authorize
				v.Authorize = func(ctx context.Context, tx *gorm.DB, tenant, id guid.ID, action string) error {
					if id == 4001 {
						return fault
					}
					return original(ctx, tx, tenant, id, action)
				}
				p.purposes["reports.export"] = v
			case "audit":
				p.module.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			default:
				names := query.BindNames(h.tree.prefix)
				table := names.ResourceBindings.String()
				if scenario == "space_write" {
					table = names.Spaces.String()
				}
				require.NoError(t, h.db.Callback().Update().Before("gorm:update").Register("test:tenant-rebind", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Update().Remove("test:tenant-rebind")) }
			}
			err = h.db.Transaction(func(tx *gorm.DB) error { return p.Rebind(h.ctx, tx, pin.BindingID, kind, resource, 1, now) })
			remove()
			if scenario == "kind" || scenario == "resource" {
				require.ErrorIs(t, err, errResourcePurpose)
			} else {
				require.ErrorIs(t, err, fault)
			}
			q := query.Bind(h.db, h.tree.prefix)
			binding, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.ID.Eq(pin.BindingID)).First()
			require.NoError(t, err)
			require.Equal(t, guid.ID(4000), binding.ResourceID)
			require.EqualValues(t, 1, binding.Version)
			h.assertAudit(t, 1)
		})
	}
}
func TestTenantResourceCommitReadRebindRelease(t *testing.T) {
	h, p, allowed := tenantFilesFixture(t)
	now := time.Now().UTC()
	prep, err := p.StageOn(h.ctx, h.db, 90, "reports.export", strings.NewReader("actual report"), 13, now.Add(time.Hour))
	require.NoError(t, err)
	var pin PinnedResource
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { var e error; pin, e = p.CreateOn(h.ctx, tx, tenantInput(prep), now); return e }))
	q := query.Bind(h.db, h.tree.prefix)
	path, err := h.tree.pathNodes(h.ctx, h.db, pin.SpaceID, pin.FileID)
	require.NoError(t, err)
	names := []string{}
	for _, node := range path {
		names = append(names, node.Name)
	}
	require.Contains(t, strings.Join(names, "/"), ".system/reports/"+now.Format("2006/01"))
	for _, node := range path {
		if node.ID == pin.FileID {
			require.Equal(t, "resource", node.ManagementMode)
		}
	}
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		for _, node := range path {
			if node.Name == "reports" {
				permissions, e := h.tree.loadNodePermissions(h.ctx, tx, pin.SpaceID, node.ID)
				require.NoError(t, e)
				require.True(t, tenantSystemDirectoryDenied(permissions, "create"))
				require.True(t, tenantSystemDirectoryDenied(permissions, "manage_acl"))
				require.False(t, tenantSystemDirectoryDenied(permissions, "read"))
			}
		}
		return nil
	}))
	read, err := p.Open(h.ctx, h.db, 90, pin.FileID)
	require.NoError(t, err)
	body, err := io.ReadAll(read.Reader)
	require.NoError(t, err)
	require.NoError(t, read.Reader.Close())
	require.Equal(t, "actual report", string(body))
	_, err = p.Open(h.ctx, h.db, 91, pin.FileID)
	require.Error(t, err)
	*allowed = false
	_, err = p.Open(h.ctx, h.db, 90, pin.FileID)
	require.ErrorIs(t, err, errOperationForbidden)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { return p.Release(h.ctx, tx, pin.BindingID, now) }))
	*allowed = true
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { return p.Rebind(h.ctx, tx, pin.BindingID, "report", 4001, 2, now) }), errResourceBindingConflict)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return p.Rebind(h.ctx, tx, pin.BindingID, "report", 4001, 1, now) }))
	b, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.ID.Eq(pin.BindingID)).Take()
	require.NoError(t, err)
	require.Equal(t, guid.ID(4001), b.ResourceID)
	require.EqualValues(t, 2, b.Version)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { return p.Release(h.ctx, tx, pin.BindingID, now) }))
	_, err = p.Open(h.ctx, h.db, 90, pin.FileID)
	require.Error(t, err)
}
func TestTenantResourceRollbackCurrentAuthorizationAndExpiryCleanup(t *testing.T) {
	h, p, allowed := tenantFilesFixture(t)
	now := time.Now()
	deadline := now.Add(time.Minute)
	_, err := p.StageOn(h.ctx, h.db, 90, "missing", strings.NewReader("x"), 1, deadline)
	require.Error(t, err)
	_, err = p.StageOn(h.ctx, h.db, 90, "reports.export", strings.NewReader("x"), 1025, deadline)
	require.Error(t, err)
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := p.StageOn(h.ctx, tx, 90, "reports.export", strings.NewReader("x"), 1, deadline)
		return e
	}), ErrAccountTransaction)
	prep, err := p.StageOn(h.ctx, h.db, 90, "reports.export", strings.NewReader("abc"), 3, deadline)
	require.NoError(t, err)
	*allowed = false
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.CreateOn(h.ctx, tx, tenantInput(prep), now); return e }), errOperationForbidden)
	*allowed = true
	bad := tenantInput(prep)
	bad.TenantID = 91
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.CreateOn(h.ctx, tx, bad, now); return e }))
	bad = tenantInput(prep)
	bad.MIME = "image/png"
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := p.CreateOn(h.ctx, tx, bad, now); return e }))
	sentinel := errors.New("business rollback")
	var file guid.ID
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		pin, e := p.CreateOn(h.ctx, tx, tenantInput(prep), now)
		require.NoError(t, e)
		file = pin.FileID
		return sentinel
	}), sentinel)
	q := query.Bind(h.db, h.tree.prefix)
	count, err := q.Node.WithContext(h.ctx).Where(q.Node.ID.Eq(file)).Count()
	require.NoError(t, err)
	require.Zero(t, count)
	intent, err := q.AccountPreparation.WithContext(h.ctx).Where(q.AccountPreparation.ID.Eq(prep.object.object.ID)).Take()
	require.NoError(t, err)
	require.Equal(t, "ready", intent.State)
	_, err = p.module.accounts.CollectAccountObjects(h.ctx, h.db, deadline.Add(time.Hour), 0, 100)
	require.NoError(t, err)
	_, err = p.module.driver.Open(h.ctx, prep.object.object.StorageKey)
	require.Error(t, err)
}
