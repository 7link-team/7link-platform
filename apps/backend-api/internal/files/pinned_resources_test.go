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
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPinnedResourceReadFailsClosedOnStorageAndAuditFailures(t *testing.T) {
	for _, scenario := range []string{"binding", "space", "reread", "owner", "version", "object", "audit", "purpose", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			h, p, input, _ := pinnedFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := p.PinOn(h.ctx, tx, input); return err }))
			fault := errors.New("pinned resource storage failure")
			names := query.BindNames(h.tree.prefix)
			table, nth := names.ResourceBindings.String(), 1
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
				delete(p.module.bindings.purposes, input.Purpose)
			case "expired":
				require.NoError(t, h.db.Table(names.ResourceBindings.String()).Where("id = ?", input.ID).Update("expires_at", time.Now().Add(-time.Minute).UnixMilli()).Error)
			}
			if scenario != "audit" && scenario != "purpose" && scenario != "expired" {
				reads := 0
				require.NoError(t, h.db.Callback().Query().Before("gorm:query").Register("test:resource-read", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						reads++
						if reads == nth {
							tx.AddError(fault)
						}
					}
				}))
				remove = func() { require.NoError(t, h.db.Callback().Query().Remove("test:resource-read")) }
			}
			var read PinnedResourceRead
			err := h.db.Transaction(func(tx *gorm.DB) error { var err error; read, err = p.OpenOn(h.ctx, tx, input.ID); return err })
			remove()
			require.Nil(t, read.Reader)
			switch scenario {
			case "purpose":
				require.ErrorIs(t, err, errResourcePurpose)
			case "expired":
				require.ErrorIs(t, err, errResourceBinding)
			default:
				require.ErrorIs(t, err, fault)
			}
			h.assertAudit(t, 1)
		})
	}
}

func pinnedFixture(t *testing.T) (operationServiceHarness, pinnedResources, ResourceBindingInput, *bool) {
	t.Helper()
	h := newOperationServiceHarness(t)
	q := query.Bind(h.db, h.tree.prefix)
	_, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).UpdateSimple(q.Node.RootScope.Value("entity"), q.Node.RootOwnerID.Value(guid.ID(80)))
	require.NoError(t, err)
	bindings, err := NewResourceBindings(h.tree.prefix)
	require.NoError(t, err)
	allowed := new(bool)
	*allowed = true
	require.NoError(t, bindings.RegisterPurpose(ResourcePurpose{
		Name: "result.evidence", ResourceKind: "result", OwnerScopes: []string{"entity"}, VersionMode: "pinned",
		ValidateResource: func(ctx context.Context, tx *gorm.DB, id guid.ID, owner ResourceOwner) error {
			claims, ok := authn.FromContext(ctx)
			if !ok || claims.PrincipalID != 70 || owner.Scope != "entity" || owner.ID != 80 || (id != 4000 && id != 4001) || !*allowed {
				return errOperationForbidden
			}
			if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
				return ErrAccountTransaction
			}
			return nil
		},
	}))
	_, err = h.service.driver.Put(h.ctx, "objects/1030", strings.NewReader("old"), 3)
	require.NoError(t, err)
	file := treeNode(t, h.db, h.tree.prefix, 30)
	input := ResourceBindingInput{ID: 6000, SpaceID: 1, FileID: 30, ExpectedFileVersion: file.Version, VersionID: file.CurrentVersionID, Purpose: "result.evidence", ResourceKind: "result", ResourceID: 4000, Slot: "attachment", ActorID: 70}
	return h, pinnedResources{module: &Module{bindings: bindings, operations: h.service, driver: h.service.driver}}, input, allowed
}

func TestPinnedResourceExactVersionAndLiveAuthorization(t *testing.T) {
	h, provider, input, workAllowed := pinnedFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		result, err := provider.PinOn(h.ctx, tx, input)
		require.NoError(t, err)
		require.Equal(t, guid.ID(2030), result.VersionID)
		require.Equal(t, input.ID, result.BindingID)
		return nil
	}))
	h.assertAudit(t, 1)
	q := query.Bind(h.db, h.tree.prefix)
	require.NoError(t, q.Object.WithContext(t.Context()).Create(&model.Object{ID: 7000, BackendID: "local", StorageKey: "objects/new", URI: "file:///objects/new", State: "verified", Size: 3, MIME: "text/plain", Type: "text", Subtype: "plain", MetadataJSON: "{}", MetadataState: "unknown", Version: 1}))
	_, err := h.service.driver.Put(h.ctx, "objects/new", strings.NewReader("new"), 3)
	require.NoError(t, err)
	file := treeNode(t, h.db, h.tree.prefix, input.FileID)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return h.tree.appendVersion(h.ctx, tx, model.Version{ID: 7001, SpaceID: 1, NodeID: input.FileID, Number: 2, ObjectID: 7000, CreatedBy: 70}, file.Version, time.Now())
	}))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		read, err := provider.OpenOn(h.ctx, tx, input.ID)
		require.NoError(t, err)
		defer read.Reader.Close()
		body, err := io.ReadAll(read.Reader)
		require.NoError(t, err)
		require.Equal(t, "old", string(body))
		require.Equal(t, guid.ID(2030), read.VersionID)
		return nil
	}))
	h.assertAudit(t, 2)
	_, err = h.service.downloadFile(h.ctx, input.FileID)
	require.ErrorIs(t, err, errOperationForbidden, "ordinary routes remain denied")
	*workAllowed = false
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.OpenOn(h.ctx, tx, input.ID); return err }), errOperationForbidden)
	*workAllowed = true
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{ID: 8000, SpaceID: 1, NodeID: input.FileID, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "download", Applies: "self", CreatedBy: 70}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.OpenOn(h.ctx, tx, input.ID); return err }), errOperationForbidden)
	h.assertAudit(t, 2)
}

func TestPinnedResourceRejectsUnauthorizedPinAndInvalidContext(t *testing.T) {
	for _, scenario := range []string{"actor", "project", "acl", "unverified", "version", "work", "transaction", "no_claims"} {
		t.Run(scenario, func(t *testing.T) {
			h, provider, input, workAllowed := pinnedFixture(t)
			q := query.Bind(h.db, h.tree.prefix)
			ctx := h.ctx
			switch scenario {
			case "actor":
				input.ActorID = 71
			case "project":
				_, err := q.Node.WithContext(t.Context()).Where(q.Node.ID.Eq(guid.ID(2))).UpdateSimple(q.Node.RootScope.Value("tenant"), q.Node.RootOwnerID.Value(guid.ID(90)))
				require.NoError(t, err)
			case "acl":
				require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{ID: 8000, SpaceID: 1, NodeID: input.FileID, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "download", Applies: "self", CreatedBy: 70}))
			case "unverified":
				_, err := q.Object.WithContext(t.Context()).Where(q.Object.ID.Eq(guid.ID(1030))).Update(q.Object.State, "pending")
				require.NoError(t, err)
			case "version":
				id := guid.ID(2031)
				input.VersionID = &id
			case "work":
				*workAllowed = false
			case "no_claims":
				ctx = t.Context()
			}
			if scenario == "transaction" {
				_, err := provider.PinOn(ctx, h.db, input)
				require.ErrorIs(t, err, ErrAccountTransaction)
			} else {
				require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.PinOn(ctx, tx, input); return err }))
			}
			count, err := q.ResourceBinding.WithContext(t.Context()).Count()
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, input.FileID).ManagementMode)
			h.assertAudit(t, 0)
		})
	}
}

func TestPinnedResourceRollbackAndInactiveRead(t *testing.T) {
	h, provider, input, _ := pinnedFixture(t)
	abort := errors.New("caller abort")
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error {
		_, err := provider.PinOn(h.ctx, tx, input)
		require.NoError(t, err)
		return abort
	}), abort)
	require.Equal(t, "user", treeNode(t, h.db, h.tree.prefix, input.FileID).ManagementMode)
	h.assertAudit(t, 0)
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.OpenOn(h.ctx, tx, input.ID); return err }))
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.PinOn(h.ctx, tx, input); return err }))
	_, err := provider.OpenOn(h.ctx, h.db, input.ID)
	require.ErrorIs(t, err, ErrAccountTransaction)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		return provider.module.bindings.Unbind(h.ctx, tx, input.SpaceID, input.ID, 1, time.Now())
	}))
	require.Error(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.OpenOn(h.ctx, tx, input.ID); return err }))
	h.assertAudit(t, 1)
}

func TestPinnedResourceRepinRequiresCurrentACLAndResourceAuthorization(t *testing.T) {
	h, provider, input, allowed := pinnedFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.PinOn(h.ctx, tx, input); return err }))
	input.ID++
	input.Slot = "revision"
	input.ResourceID = 4001
	input.ExpectedFileVersion = treeNode(t, h.db, h.tree.prefix, input.FileID).Version
	*allowed = false
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.PinOn(h.ctx, tx, input); return err }), errOperationForbidden)
	*allowed = true
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.PinOn(h.ctx, tx, input); return err }))
	h.assertAudit(t, 2)
	input.ID++
	input.Slot = "blocked"
	input.ExpectedFileVersion = treeNode(t, h.db, h.tree.prefix, input.FileID).Version
	q := query.Bind(h.db, h.tree.prefix)
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{ID: 8000, SpaceID: 1, NodeID: input.FileID, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "download", Applies: "self", CreatedBy: 70}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.PinOn(h.ctx, tx, input); return err }), errOperationForbidden)
	h.assertAudit(t, 2)
}

func TestPinnedResourceInspectUsesCurrentFileACL(t *testing.T) {
	h, provider, input, _ := pinnedFixture(t)
	require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
		candidate, err := provider.InspectOn(h.ctx, tx, input.FileID)
		require.NoError(t, err)
		require.Equal(t, input.ExpectedFileVersion, candidate.NodeVersion)
		require.Equal(t, *input.VersionID, candidate.VersionID)
		require.Equal(t, input.SpaceID, candidate.SpaceID)
		return nil
	}))
	q := query.Bind(h.db, h.tree.prefix)
	require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{ID: 8000, SpaceID: 1, NodeID: input.FileID, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "download", Applies: "self", CreatedBy: 70}))
	require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.InspectOn(h.ctx, tx, input.FileID); return err }), errOperationForbidden)
	h.assertAudit(t, 0)
}

func TestPinnedResourceInspectManagedCandidateRequiresReadableActiveBinding(t *testing.T) {
	for _, denied := range []string{"resource", "inactive", "expired", "acl"} {
		t.Run(denied, func(t *testing.T) {
			h, provider, input, allowed := pinnedFixture(t)
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.PinOn(h.ctx, tx, input); return err }))
			require.NoError(t, h.db.Transaction(func(tx *gorm.DB) error {
				candidate, err := provider.InspectOn(h.ctx, tx, input.FileID)
				require.NoError(t, err)
				require.Equal(t, *input.VersionID, candidate.VersionID)
				require.Equal(t, treeNode(t, tx, h.tree.prefix, input.FileID).Version, candidate.NodeVersion)
				return nil
			}))
			_, err := h.service.downloadFile(h.ctx, input.FileID)
			require.ErrorIs(t, err, errOperationForbidden)
			q := query.Bind(h.db, h.tree.prefix)
			switch denied {
			case "resource":
				*allowed = false
			case "inactive":
				_, err = q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.ID.Eq(input.ID)).UpdateSimple(q.ResourceBinding.State.Value("inactive"), q.ResourceBinding.LiveSlot.Null())
				require.NoError(t, err)
			case "expired":
				_, err = q.ResourceBinding.WithContext(t.Context()).Where(q.ResourceBinding.ID.Eq(input.ID)).Update(q.ResourceBinding.ExpiresAt, time.Now().Add(-time.Second).UnixMilli())
				require.NoError(t, err)
			case "acl":
				require.NoError(t, q.NodeACL.WithContext(t.Context()).Create(&model.NodeACL{ID: 8000, SpaceID: 1, NodeID: input.FileID, SubjectKind: "principal", SubjectID: 70, Effect: "deny", Action: "download", Applies: "self", CreatedBy: 70}))
			}
			require.ErrorIs(t, h.db.Transaction(func(tx *gorm.DB) error { _, err := provider.InspectOn(h.ctx, tx, input.FileID); return err }), errOperationForbidden)
			h.assertAudit(t, 1)
		})
	}
}
