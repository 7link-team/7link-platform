package files

import (
	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"io"
	"time"
)

// DeliveryBundleFiles stages immutable bytes outside the aggregate transaction.
// AttachOn rechecks the registered owning-domain authority and commits both file
// metadata and its pinned entity binding in that aggregate's transaction.
type DeliveryBundleFiles struct {
	Stage    func(context.Context, guid.ID, string, io.Reader, int64) (*PreparedDeliveryBundle, error)
	AttachOn func(context.Context, *gorm.DB, *PreparedDeliveryBundle, guid.ID) (PinnedResource, error)
	OpenOn   func(context.Context, *gorm.DB, guid.ID) (PinnedResourceRead, error)
}
type deliveryBundleWriteKey struct{}

// DeliveryBundleWrite identifies the Files-controlled staging/attachment path.
func DeliveryBundleWrite(ctx context.Context) bool {
	v, _ := ctx.Value(deliveryBundleWriteKey{}).(bool)
	return v
}

type PreparedDeliveryBundle struct {
	tenant, project, actor guid.ID
	purpose, name, slot    string
	object                 *PreparedAccountObject
}

func (m *Module) provideDeliveryBundleFiles(s *module.Services) error {
	return module.Provide[DeliveryBundleFiles](s, DeliveryBundleFiles{Stage: m.stageDeliveryBundle, AttachOn: m.attachDeliveryBundleOn, OpenOn: m.openClientCollaborationResourceOn})
}
func (m *Module) deliveryBundleReady(tx *gorm.DB) error {
	if m == nil || m.accounts == nil || m.bindings == nil || m.appender == nil || m.ids == nil || tx == nil {
		return errOperationUnavailable
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountTransaction
	}
	return nil
}
func (m *Module) authorizeDeliveryBundleOn(ctx context.Context, tx *gorm.DB, purpose string, id, project guid.ID) error {
	if purpose != "delivery_bundle" && purpose != "delivery_manifest" && purpose != "test_report" {
		return errResourcePurpose
	}
	p, ok := m.bindings.purpose(purpose)
	if !ok || p.ResourceKind != "delivery" || p.ValidateResource == nil {
		return errResourcePurpose
	}
	return p.ValidateResource(context.WithValue(ctx, deliveryBundleWriteKey{}, true), tx, id, ResourceOwner{Scope: "entity", ID: project})
}
func (m *Module) stageDeliveryBundle(ctx context.Context, project guid.ID, purpose string, r io.Reader, size int64) (*PreparedDeliveryBundle, error) {
	if m == nil || m.runtime == nil || m.accounts == nil || r == nil || size < 1 || size > 512<<20 {
		return nil, errOperationInput
	}
	tenant, actor := authn.TenantIDFromContext(ctx), authn.PrincipalIDFromContext(ctx)
	if tenant <= 0 || actor <= 0 || project != authn.EntityIDFromContext(ctx) {
		return nil, errOperationForbidden
	}
	db := m.runtime.Database().Write()
	if db == nil {
		return nil, errOperationUnavailable
	}
	if e := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := m.deliveryBundleReady(tx); e != nil {
			return e
		}
		return m.authorizeDeliveryBundleOn(ctx, tx, purpose, 0, project)
	}); e != nil {
		return nil, e
	}
	id, e := m.nextFileID()
	if e != nil {
		return nil, e
	}
	obj, e := m.accounts.Stage(ctx, db, id, actor, "delivery/"+id.String(), r, size, time.Now().Add(time.Hour))
	if e != nil {
		return nil, e
	}
	name := "bundle.zip"
	if purpose == "test_report" {
		name = "test-report.pdf"
	}
	if purpose == "delivery_manifest" {
		name = "manifest.json"
	}
	return &PreparedDeliveryBundle{tenant: tenant, project: project, actor: actor, purpose: purpose, name: name, slot: purpose + "-" + id.String(), object: obj}, nil
}
func (m *Module) attachDeliveryBundleOn(ctx context.Context, tx *gorm.DB, prepared *PreparedDeliveryBundle, resourceID guid.ID) (PinnedResource, error) {
	if err := m.deliveryBundleReady(tx); err != nil {
		return PinnedResource{}, err
	}
	if prepared == nil || prepared.object == nil || resourceID <= 0 || authn.PrincipalIDFromContext(ctx) != prepared.actor {
		return PinnedResource{}, errOperationForbidden
	}
	tenant, project := prepared.tenant, prepared.project
	if authn.TenantIDFromContext(ctx) != tenant || authn.EntityIDFromContext(ctx) != project {
		return PinnedResource{}, errOperationForbidden
	}
	ctx = context.WithValue(ctx, deliveryBundleWriteKey{}, true)
	if err := m.authorizeDeliveryBundleOn(ctx, tx, prepared.purpose, resourceID, project); err != nil {
		return PinnedResource{}, err
	}
	now := time.Now().UTC()
	rootID, err := m.provisionOrganizationRoot(ctx, tx, core.FileRootProvisionInput{TenantID: tenant, Scope: "entity", OwnerID: project}, now)
	if err != nil {
		return PinnedResource{}, err
	}
	tree := directoryTree{prefix: m.bindings.prefix}
	q := query.Bind(tx, tree.prefix)
	root, err := q.Node.WithContext(ctx).Where(q.Node.ID.Eq(rootID)).First()
	if err != nil {
		return PinnedResource{}, err
	}

	for _, name := range []string{"deliveries", resourceID.String()} {
		child, e := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(root.SpaceID), q.Node.ParentID.Eq(root.ID), q.Node.NameKey.Eq(name), q.Node.State.Eq("active")).Take()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			id, e2 := m.nextFileID()
			if e2 != nil {
				return PinnedResource{}, e2
			}
			child, e = tree.createNodeOn(ctx, tx, root.SpaceID, id, root.ID, "directory", name, now)
		}
		if e != nil {
			return PinnedResource{}, e
		}
		if child.Kind != "directory" {
			return PinnedResource{}, errHierarchy
		}
		root = child
	}
	fileID, err := m.nextFileID()
	if err != nil {
		return PinnedResource{}, err
	}
	versionID, err := m.nextFileID()
	if err != nil {
		return PinnedResource{}, err
	}
	bindingID, err := m.nextFileID()
	if err != nil {
		return PinnedResource{}, err
	}
	file, err := tree.createNodeOn(ctx, tx, root.SpaceID, fileID, root.ID, "file", fmt.Sprintf("%s-%s", fileID, prepared.name), now)
	if err != nil {
		return PinnedResource{}, err
	}
	object, err := m.accounts.attachFileObject(ctx, tx, prepared.object, prepared.actor)
	if err != nil {
		return PinnedResource{}, err
	}
	if err = tree.appendVersion(ctx, tx, model.Version{ID: versionID, SpaceID: root.SpaceID, NodeID: fileID, Number: 1, ObjectID: object.ID, Ext: file.Ext, DeclaredMIME: object.MIME, CreatedBy: prepared.actor}, file.Version, now); err != nil {
		return PinnedResource{}, err
	}
	file, err = q.Node.WithContext(ctx).Where(q.Node.ID.Eq(fileID)).First()
	if err != nil {
		return PinnedResource{}, err
	}
	binding, err := m.bindings.Bind(ctx, tx, ResourceBindingInput{ID: bindingID, SpaceID: root.SpaceID, FileID: fileID, ExpectedFileVersion: file.Version, VersionID: &versionID, Purpose: prepared.purpose, ResourceKind: "delivery", ResourceID: resourceID, Slot: prepared.slot, ActorID: prepared.actor}, now)
	if err != nil {
		return PinnedResource{}, err
	}
	event := audit.NewDomainEvent(ctx, tenant, "delivery_file_ingested", "file_node", fileID)
	event.Detail["delivery_id"], event.Detail["binding_id"], event.Detail["system_write"] = resourceID.String(), binding.ID.String(), true
	if err = m.appender(ctx, tx, event); err != nil {
		return PinnedResource{}, err
	}
	return PinnedResource{BindingID: binding.ID, SpaceID: root.SpaceID, FileID: fileID, VersionID: versionID, ResourceID: resourceID, Purpose: binding.Purpose, ResourceKind: binding.ResourceKind, Name: file.Name, MIME: object.MIME, SHA256: object.SHA256, Size: object.Size}, nil
}
