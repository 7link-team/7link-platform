package files

import (
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"strings"
	"testing"
)

func TestDeliveryBundleRejectsUnboundPurposesAndForeignStagingScopes(t *testing.T) {
	h, p, _, _ := delegatedArtifactFixture(t)
	m := p.module
	for _, tc := range []struct {
		project  guid.ID
		purpose  string
		reader   io.Reader
		size     int64
		expected error
	}{
		{80, "delivery_bundle", nil, 1, errOperationInput},
		{80, "delivery_bundle", strings.NewReader("x"), 0, errOperationInput},
		{80, "delivery_bundle", strings.NewReader("x"), 512<<20 + 1, errOperationInput},
		{81, "delivery_bundle", strings.NewReader("x"), 1, errOperationForbidden},
		{80, "unknown", strings.NewReader("x"), 1, errResourcePurpose},
		{80, "delivery_manifest", strings.NewReader("x"), 1, errResourcePurpose},
	} {
		out, err := m.stageDeliveryBundle(h.ctx, tc.project, tc.purpose, tc.reader, tc.size)
		require.ErrorIs(t, err, tc.expected)
		require.Nil(t, out)
	}
	_, err := m.stageDeliveryBundle(context.Background(), 80, "delivery_bundle", strings.NewReader("x"), 1)
	require.ErrorIs(t, err, errOperationForbidden)
	var absent *Module
	require.ErrorIs(t, absent.deliveryBundleReady(h.db), errOperationUnavailable)
	require.ErrorIs(t, m.deliveryBundleReady(h.db), ErrAccountTransaction)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := m.attachDeliveryBundleOn(h.ctx, tx, nil, 9001)
		require.ErrorIs(t, err, errOperationForbidden)
		return nil
	}))
	s := module.NewServices()
	require.NoError(t, m.provideDeliveryBundleFiles(s))
	require.NoError(t, s.Seal())
	capability, err := module.Resolve[DeliveryBundleFiles](s)
	require.NoError(t, err)
	require.NotNil(t, capability.Stage)
	require.NotNil(t, capability.AttachOn)
	require.NotNil(t, capability.OpenOn)
	require.False(t, DeliveryBundleWrite(h.ctx))
}

func TestDeliveryBundleAttachmentFailuresPreservePreparedObject(t *testing.T) {
	for _, scenario := range []string{"foreign_actor", "foreign_project", "root", "version", "binding", "audit"} {
		t.Run(scenario, func(t *testing.T) {
			h, p, _, _ := delegatedArtifactFixture(t)
			m := p.module
			require.NoError(t, m.bindings.RegisterPurpose(ResourcePurpose{Name: "delivery_bundle", ResourceKind: "delivery", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, _ *gorm.DB, _ guid.ID, _ ResourceOwner) error {
				require.True(t, DeliveryBundleWrite(ctx))
				return nil
			}}))
			prepared, err := m.stageDeliveryBundle(h.ctx, 80, "delivery_bundle", strings.NewReader("zip"), 3)
			require.NoError(t, err)
			ctx := h.ctx
			fault := errors.New("delivery files persistence failure")
			remove := func() {}
			names := query.BindNames(h.tree.prefix)
			switch scenario {
			case "foreign_actor":
				ctx = authn.WithClaims(h.ctx, &authn.Claims{TenantID: 90, EntityID: 80, PrincipalID: 71, SubjectType: authn.SubjectTypePrincipal})
			case "foreign_project":
				ctx = authn.WithClaims(h.ctx, &authn.Claims{TenantID: 90, EntityID: 81, PrincipalID: 70, SubjectType: authn.SubjectTypePrincipal})
			case "audit":
				m.appender = func(context.Context, *gorm.DB, audit.DomainEvent) error { return fault }
			case "root":
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:bundle", func(tx *gorm.DB) {
					if tx.Statement.Table == names.Spaces.String() {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Query().Remove("test:bundle")) }
			default:
				table := names.Versions.String()
				if scenario == "binding" {
					table = names.ResourceBindings.String()
				}
				require.NoError(t, h.db.Callback().Create().Before("gorm:create").Register("test:bundle", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(fault)
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Create().Remove("test:bundle")) }
			}
			err = h.db.Transaction(func(tx *gorm.DB) error { _, err := m.attachDeliveryBundleOn(ctx, tx, prepared, 9001); return err })
			remove()
			if scenario == "foreign_actor" || scenario == "foreign_project" {
				require.ErrorIs(t, err, errOperationForbidden)
			} else {
				require.ErrorIs(t, err, fault)
			}
			q := query.Bind(h.db, h.tree.prefix)
			intent, err := q.AccountPreparation.WithContext(h.ctx).Where(q.AccountPreparation.ID.Eq(prepared.object.object.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ready", intent.State)
			n, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.Purpose.Eq("delivery_bundle")).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestDeliveryBundleIdentifierFailuresRollbackAttachment(t *testing.T) {
	for _, available := range []int64{0, 1, 2, 3, 4} {
		t.Run(string(rune('0'+available)), func(t *testing.T) {
			h, p, _, _ := delegatedArtifactFixture(t)
			m := p.module
			require.NoError(t, m.bindings.RegisterPurpose(ResourcePurpose{Name: "delivery_bundle", ResourceKind: "delivery", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(context.Context, *gorm.DB, guid.ID, ResourceOwner) error { return nil }}))
			prepared, err := m.stageDeliveryBundle(h.ctx, 80, "delivery_bundle", strings.NewReader("zip"), 3)
			require.NoError(t, err)
			ids := &failingIDs{}
			ids.remaining.Store(available)
			ids.next.Store(50000)
			m.ids = ids
			err = h.db.Transaction(func(tx *gorm.DB) error { _, err := m.attachDeliveryBundleOn(h.ctx, tx, prepared, 9001); return err })
			require.ErrorIs(t, err, ErrUnsupported)
			q := query.Bind(h.db, h.tree.prefix)
			intent, err := q.AccountPreparation.WithContext(h.ctx).Where(q.AccountPreparation.ID.Eq(prepared.object.object.ID)).First()
			require.NoError(t, err)
			require.Equal(t, "ready", intent.State)
			n, err := q.ResourceBinding.WithContext(h.ctx).Where(q.ResourceBinding.Purpose.Eq("delivery_bundle")).Count()
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

func TestDeliveryBundleFilesStageAtomicBindCurrentAuthorityAndPinnedRead(t *testing.T) {
	h, store, _, _ := delegatedArtifactFixture(t)
	m := store.module
	allowed := true
	for _, name := range []string{"delivery_bundle", "delivery_manifest"} {
		require.NoError(t, m.bindings.RegisterPurpose(ResourcePurpose{Name: name, ResourceKind: "delivery", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, _ *gorm.DB, id guid.ID, owner ResourceOwner) error {
			if !allowed || authn.PrincipalIDFromContext(ctx) != 70 || owner.Scope != "entity" || owner.ID != 80 || (id != 0 && id != 9001) {
				return errOperationForbidden
			}
			return nil
		}}))
	}
	prep, e := m.stageDeliveryBundle(h.ctx, 80, "delivery_manifest", strings.NewReader(`{"schema_version":"1"}`), 22)
	require.NoError(t, e)
	allowed = false
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, e := m.attachDeliveryBundleOn(h.ctx, tx, prep, 9001); return e }))
	allowed = true
	marker := errors.New("rollback")
	var pin PinnedResource
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		pin, e = m.attachDeliveryBundleOn(h.ctx, tx, prep, 9001)
		if e != nil {
			return e
		}
		return marker
	}), marker)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var e error
		pin, e = m.attachDeliveryBundleOn(h.ctx, tx, prep, 9001)
		return e
	}))
	require.Equal(t, "delivery_manifest", pin.Purpose)
	require.Equal(t, "delivery", pin.ResourceKind)
	path, e := h.tree.pathNodes(h.ctx, h.db, pin.SpaceID, pin.FileID)
	require.NoError(t, e)
	names := []string{}
	for _, node := range path {
		names = append(names, node.Name)
	}
	require.Contains(t, strings.Join(names, "/"), "deliveries/9001/")
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		read, e := m.openClientCollaborationResourceOn(h.ctx, tx, pin.BindingID)
		if e != nil {
			return e
		}
		defer read.Reader.Close()
		raw, e := io.ReadAll(read.Reader)
		require.Equal(t, `{"schema_version":"1"}`, string(raw)) //nolint:testifylint // Serialized bytes are frozen by this regression contract.
		return e
	}))
	allowed = false
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, e := m.openClientCollaborationResourceOn(h.ctx, tx, pin.BindingID)
		return e
	}))
}

func TestSavedTestReportUsesPinnedPDFBytesAndCurrentDomainAuthority(t *testing.T) {
	h, store, _, _ := delegatedArtifactFixture(t)
	m := store.module
	allowed := true
	require.NoError(t, m.bindings.RegisterPurpose(ResourcePurpose{Name: "test_report", ResourceKind: "delivery", OwnerScopes: []string{"entity"}, VersionMode: "pinned", ValidateResource: func(ctx context.Context, _ *gorm.DB, id guid.ID, owner ResourceOwner) error {
		if !allowed || owner.ID != 80 {
			return errOperationForbidden
		}
		return nil
	}}))
	raw := "%PDF-1.4\nFrozen test report\n%%EOF\n"
	prepared, err := m.stageDeliveryBundle(h.ctx, 80, "test_report", strings.NewReader(raw), int64(len(raw)))
	require.NoError(t, err)
	var pin PinnedResource
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		var err error
		pin, err = m.attachDeliveryBundleOn(h.ctx, tx, prepared, 9001)
		return err
	}))
	require.Equal(t, "test_report", pin.Purpose)
	require.True(t, strings.HasSuffix(pin.Name, "test-report.pdf"))
	require.Equal(t, "application/pdf", pin.MIME)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		read, err := m.openClientCollaborationResourceOn(h.ctx, tx, pin.BindingID)
		if err != nil {
			return err
		}
		defer read.Reader.Close()
		got, err := io.ReadAll(read.Reader)
		require.Equal(t, raw, string(got))
		require.Equal(t, pin.VersionID, read.VersionID)
		return err
	}))
	allowed = false
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := m.openClientCollaborationResourceOn(h.ctx, tx, pin.BindingID)
		return err
	}))
}
