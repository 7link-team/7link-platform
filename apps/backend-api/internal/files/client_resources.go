package files

import (
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/module"
	"context"
	"gorm.io/gorm"
	"time"
)

// ClientCollaborationResources delegates reads to registered clientwork purposes.
// Pinning requires ordinary file ACL; reads require current project/client authorization.
// It exposes no storage key and always streams the immutable pinned version.
type ClientCollaborationResources struct {
	UnpinOn   func(context.Context, *gorm.DB, guid.ID, guid.ID) error
	InspectOn func(context.Context, *gorm.DB, guid.ID) (PinnedCandidate, error)
	PinOn     func(context.Context, *gorm.DB, ResourceBindingInput) (PinnedResource, error)
	OpenOn    func(context.Context, *gorm.DB, guid.ID) (PinnedResourceRead, error)
}

func (m *Module) provideClientResources(s *module.Services) error {
	return module.Provide[ClientCollaborationResources](s, ClientCollaborationResources{InspectOn: pinnedResources{module: m}.InspectOn, PinOn: pinnedResources{module: m}.PinOn, OpenOn: m.openClientCollaborationResourceOn, UnpinOn: m.unpinClientPackageResourceOn})
}
func (m *Module) openClientCollaborationResourceOn(ctx context.Context, tx *gorm.DB, bindingID guid.ID) (PinnedResourceRead, error) {
	p := pinnedResources{module: m}
	claims, err := p.ready(ctx, tx)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	q := query.Bind(tx, m.bindings.prefix)
	b, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(bindingID), q.ResourceBinding.Purpose.In("clientwork.preview", "clientwork.package", "delivery_bundle", "delivery_manifest", "test_report"), q.ResourceBinding.State.Eq("active")).First()
	if err != nil {
		return PinnedResourceRead{}, err
	}
	tree := directoryTree{prefix: m.bindings.prefix}
	if _, _, err = tree.lockOperationSpace(ctx, tx, b.SpaceID); err != nil {
		return PinnedResourceRead{}, err
	}
	// Re-read after the space lock so an unbind cannot race this download.
	b, err = q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(bindingID), q.ResourceBinding.Purpose.In("clientwork.preview", "clientwork.package", "delivery_bundle", "delivery_manifest", "test_report"), q.ResourceBinding.State.Eq("active")).First()
	if err != nil {
		return PinnedResourceRead{}, err
	}
	if b.VersionID == nil || (b.ExpiresAt != nil && *b.ExpiresAt <= time.Now().UnixMilli()) {
		return PinnedResourceRead{}, ErrNotFound
	}
	purpose, ok := m.bindings.purpose(b.Purpose)
	if !ok || purpose.VersionMode != "pinned" || purpose.ResourceKind != b.ResourceKind || purpose.ValidateResource == nil {
		return PinnedResourceRead{}, errResourcePurpose
	}
	owner, err := tree.resourceOwner(ctx, tx, b.SpaceID, b.FileID)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	if owner.Scope != ownerEntity || owner.ID != claims.EntityID {
		return PinnedResourceRead{}, errOperationForbidden
	}
	if err = purpose.ValidateResource(ctx, tx, b.ResourceID, owner); err != nil {
		return PinnedResourceRead{}, err
	}
	fact, obj, err := p.content(ctx, tx, b.SpaceID, b.FileID, *b.VersionID)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	reader, err := openStoredObject(ctx, m.driver, obj)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	if err = m.operations.appendNodeAudit(ctx, tx, claims, "file_resource_downloaded", b.FileID, map[string]any{"binding_id": b.ID, "version_id": *b.VersionID, "resource_id": b.ResourceID}); err != nil {
		_ = reader.Close()
		return PinnedResourceRead{}, err
	}
	fact.BindingID, fact.ResourceID, fact.Purpose, fact.ResourceKind = b.ID, b.ResourceID, b.Purpose, b.ResourceKind
	return PinnedResourceRead{PinnedResource: fact, Reader: reader}, nil
}

// Unpin only the named package's binding, under the same Space fence used by reads.
// The owning domain must authorize draft editing before using this capability.
func (m *Module) unpinClientPackageResourceOn(ctx context.Context, tx *gorm.DB, bindingID, packageID guid.ID) error {
	p := pinnedResources{module: m}
	claims, e := p.ready(ctx, tx)
	if e != nil {
		return e
	}
	q := query.Bind(tx, m.bindings.prefix)
	b, e := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(bindingID), q.ResourceBinding.ResourceID.Eq(packageID), q.ResourceBinding.Purpose.Eq("clientwork.package"), q.ResourceBinding.State.Eq("active")).First()
	if e != nil {
		return e
	}
	tree := directoryTree{prefix: m.bindings.prefix}
	if _, _, e = tree.lockOperationSpace(ctx, tx, b.SpaceID); e != nil {
		return e
	}
	authorizer, e := m.operations.authorize(ctx, tx, claims, tree)
	if e != nil {
		return e
	}
	owner, e := tree.resourceOwner(ctx, tx, b.SpaceID, b.FileID)
	if e != nil {
		return e
	}
	if owner.Scope != ownerEntity || owner.ID != claims.EntityID {
		return errOperationForbidden
	}
	purpose, ok := m.bindings.purpose(b.Purpose)
	if !ok || purpose.ResourceKind != b.ResourceKind || purpose.ValidateResource == nil {
		return errResourcePurpose
	}
	if e = purpose.ValidateResource(ctx, tx, packageID, owner); e != nil {
		return e
	}
	if e = pinnedResourceAccess(ctx, tx, authorizer, b.SpaceID, b.FileID); e != nil {
		return e
	}
	if e = m.bindings.Unbind(ctx, tx, b.SpaceID, b.ID, b.Version, time.Now()); e != nil {
		return e
	}
	return m.operations.appendNodeAudit(ctx, tx, claims, "file_resource_unbound", b.FileID, map[string]any{"binding_id": b.ID, "resource_id": packageID})
}
