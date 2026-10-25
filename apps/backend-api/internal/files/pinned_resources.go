package files

import (
	"context"
	"io"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"7link.com/platform/pkg/module"
	"gorm.io/gorm"
)

// Exported classifications let business adapters map pin failures without
// depending on private Files implementation errors.
var (
	ErrPinnedForbidden   = errOperationForbidden
	ErrPinnedConflict    = errResourceBindingConflict
	ErrPinnedUnavailable = errOperationUnavailable
	ErrPinnedInvalid     = errResourceBinding
)

// PinnedResources attaches exact immutable file versions in the caller's transaction.
// Registered purpose callbacks must authorize the current actor against the owning
// resource on every invocation, including reads, and verify its ResourceOwner.
type PinnedResources interface {
	InspectOn(context.Context, *gorm.DB, guid.ID) (PinnedCandidate, error)
	PinOn(context.Context, *gorm.DB, ResourceBindingInput) (PinnedResource, error)
	OpenOn(context.Context, *gorm.DB, guid.ID) (PinnedResourceRead, error)
}

// PinnedResource exposes content identity without leaking storage keys or rows.
type PinnedResource struct {
	BindingID, SpaceID, FileID, VersionID, ResourceID guid.ID
	Purpose, ResourceKind, Name, MIME, SHA256         string
	Size                                              int64
}

type PinnedResourceRead struct {
	PinnedResource
	Reader io.ReadCloser
}

// PinnedCandidate is an authorized current file revision suitable for pin input.
type PinnedCandidate struct {
	PinnedResource
	NodeVersion int64
}

type pinnedResources struct{ module *Module }

func (p pinnedResources) InspectOn(ctx context.Context, tx *gorm.DB, fileID guid.ID) (PinnedCandidate, error) {
	claims, err := p.ready(ctx, tx)
	if err != nil {
		return PinnedCandidate{}, err
	}
	tree := directoryTree{prefix: p.module.bindings.prefix}
	authorizer, err := p.module.operations.authorize(ctx, tx, claims, tree)
	if err != nil {
		return PinnedCandidate{}, err
	}
	file, err := activeNode(ctx, tx, tree.prefix, fileID)
	if err != nil {
		return PinnedCandidate{}, err
	}
	if file.Kind != "file" || file.CurrentVersionID == nil {
		return PinnedCandidate{}, ErrNotFound
	}
	owner, err := tree.resourceOwner(ctx, tx, file.SpaceID, file.ID)
	if err != nil {
		return PinnedCandidate{}, err
	}
	if owner.Scope != ownerEntity || owner.ID != claims.EntityID {
		return PinnedCandidate{}, errOperationForbidden
	}
	if file.ManagementMode == "resource" {
		q := query.Bind(tx, tree.prefix)
		bindings, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.SpaceID.Eq(file.SpaceID), q.ResourceBinding.FileID.Eq(file.ID), q.ResourceBinding.State.Eq("active")).Find()
		if err != nil {
			return PinnedCandidate{}, err
		}
		readable := false
		now := time.Now().UnixMilli()
		for _, binding := range bindings {
			purpose, ok := p.module.bindings.purpose(binding.Purpose)
			if !ok || purpose.VersionMode != "pinned" || purpose.ResourceKind != binding.ResourceKind || binding.VersionID == nil || (binding.ExpiresAt != nil && *binding.ExpiresAt <= now) {
				continue
			}
			if purpose.ValidateResource(ctx, tx, binding.ResourceID, owner) == nil {
				readable = true
				break
			}
		}
		if !readable {
			return PinnedCandidate{}, errOperationForbidden
		}
		if err := pinnedResourceAccess(ctx, tx, authorizer, file.SpaceID, file.ID); err != nil {
			return PinnedCandidate{}, err
		}
	} else {
		for _, action := range []string{"traverse", "download"} {
			if err := authorizer.access(ctx, tx, file.SpaceID, claims.PrincipalID, file.ID, action); err != nil {
				return PinnedCandidate{}, err
			}
		}
	}
	content, _, err := p.content(ctx, tx, file.SpaceID, file.ID, *file.CurrentVersionID)
	if err != nil {
		return PinnedCandidate{}, err
	}
	return PinnedCandidate{PinnedResource: content, NodeVersion: file.Version}, nil
}

func (p pinnedResources) ready(ctx context.Context, tx *gorm.DB) (*authn.Claims, error) {
	if tx == nil || tx.Statement == nil {
		return nil, ErrAccountTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrAccountTransaction
	}
	claims, ok := authn.FromContext(ctx)
	if !ok || claims == nil || claims.PrincipalID <= 0 || claims.TenantID <= 0 || claims.EntityID <= 0 {
		return nil, errOperationForbidden
	}
	if p.module == nil || p.module.bindings == nil || p.module.operations == nil || p.module.operations.appender == nil || p.module.driver == nil {
		return nil, errOperationUnavailable
	}
	return claims, nil
}

func (p pinnedResources) PinOn(ctx context.Context, tx *gorm.DB, input ResourceBindingInput) (PinnedResource, error) {
	claims, err := p.ready(ctx, tx)
	if err != nil {
		return PinnedResource{}, err
	}
	if input.ActorID != claims.PrincipalID || input.VersionID == nil || *input.VersionID <= 0 {
		return PinnedResource{}, errOperationForbidden
	}
	tree := directoryTree{prefix: p.module.bindings.prefix}
	if _, _, err := tree.lockOperationSpace(ctx, tx, input.SpaceID); err != nil {
		return PinnedResource{}, err
	}
	authorizer, err := p.module.operations.authorize(ctx, tx, claims, tree)
	if err != nil {
		return PinnedResource{}, err
	}
	owner, err := tree.resourceOwner(ctx, tx, input.SpaceID, input.FileID)
	if err != nil {
		return PinnedResource{}, err
	}
	if owner.Scope != ownerEntity || owner.ID != claims.EntityID {
		return PinnedResource{}, errOperationForbidden
	}
	file, err := activeNode(ctx, tx, tree.prefix, input.FileID)
	if err != nil {
		return PinnedResource{}, err
	}
	if file.SpaceID != input.SpaceID || file.Kind != "file" {
		return PinnedResource{}, errOperationForbidden
	}
	if file.ManagementMode == "resource" {
		if err := p.module.authorizeTerminalRepinOn(ctx, tx, input.SpaceID, input.FileID); err != nil {
			return PinnedResource{}, err
		}
		// A new authorized resource can reference an already managed version.
		// Validate the target resource before crossing the management boundary.
		purpose, ok := p.module.bindings.purpose(input.Purpose)
		if !ok || purpose.VersionMode != "pinned" || purpose.ResourceKind != input.ResourceKind {
			return PinnedResource{}, errResourcePurpose
		}
		if err := purpose.ValidateResource(ctx, tx, input.ResourceID, owner); err != nil {
			return PinnedResource{}, err
		}
		if err := pinnedResourceAccess(ctx, tx, authorizer, input.SpaceID, input.FileID); err != nil {
			return PinnedResource{}, err
		}
	} else {
		for _, action := range []string{"traverse", "download"} {
			if err := authorizer.access(ctx, tx, input.SpaceID, claims.PrincipalID, input.FileID, action); err != nil {
				return PinnedResource{}, err
			}
		}
	}
	result, _, err := p.content(ctx, tx, input.SpaceID, input.FileID, *input.VersionID)
	if err != nil {
		return PinnedResource{}, err
	}
	binding, err := p.module.bindings.Bind(ctx, tx, input, time.Now())
	if err != nil {
		return PinnedResource{}, err
	}
	result.BindingID, result.ResourceID, result.Purpose, result.ResourceKind = binding.ID, binding.ResourceID, binding.Purpose, binding.ResourceKind
	if err := p.module.operations.appendNodeAudit(ctx, tx, claims, "file_resource_pinned", input.FileID, map[string]any{"binding_id": binding.ID, "version_id": result.VersionID, "purpose": binding.Purpose, "resource_id": binding.ResourceID}); err != nil {
		return PinnedResource{}, err
	}
	return result, nil
}

func (p pinnedResources) content(ctx context.Context, tx *gorm.DB, spaceID, fileID, versionID guid.ID) (PinnedResource, *model.Object, error) {
	q := query.Bind(tx, p.module.bindings.prefix)
	file, err := q.Node.WithContext(ctx).Where(q.Node.SpaceID.Eq(spaceID), q.Node.ID.Eq(fileID), q.Node.Kind.Eq("file"), q.Node.State.Eq("active")).Take()
	if err != nil {
		return PinnedResource{}, nil, err
	}
	version, err := q.Version.WithContext(ctx).Where(q.Version.SpaceID.Eq(spaceID), q.Version.NodeID.Eq(fileID), q.Version.ID.Eq(versionID)).Take()
	if err != nil {
		return PinnedResource{}, nil, err
	}
	object, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(version.ObjectID), q.Object.State.Eq("verified")).Take()
	if err != nil {
		return PinnedResource{}, nil, err
	}
	return PinnedResource{SpaceID: spaceID, FileID: fileID, VersionID: versionID, Name: file.Name, MIME: object.MIME, SHA256: object.SHA256, Size: object.Size}, object, nil
}

func (p pinnedResources) OpenOn(ctx context.Context, tx *gorm.DB, bindingID guid.ID) (PinnedResourceRead, error) {
	claims, err := p.ready(ctx, tx)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	q := query.Bind(tx, p.module.bindings.prefix)
	binding, err := q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(bindingID)).Take()
	if err != nil {
		return PinnedResourceRead{}, err
	}
	tree := directoryTree{prefix: p.module.bindings.prefix}
	if _, _, err := tree.lockOperationSpace(ctx, tx, binding.SpaceID); err != nil {
		return PinnedResourceRead{}, err
	}
	binding, err = q.ResourceBinding.WithContext(ctx).Where(q.ResourceBinding.ID.Eq(bindingID), q.ResourceBinding.State.Eq("active")).Take()
	if err != nil {
		return PinnedResourceRead{}, err
	}
	if binding.VersionID == nil || (binding.ExpiresAt != nil && *binding.ExpiresAt <= time.Now().UnixMilli()) {
		return PinnedResourceRead{}, errResourceBinding
	}
	purpose, ok := p.module.bindings.purpose(binding.Purpose)
	if !ok || purpose.VersionMode != "pinned" || purpose.ResourceKind != binding.ResourceKind {
		return PinnedResourceRead{}, errResourcePurpose
	}
	authorizer, err := p.module.operations.authorize(ctx, tx, claims, tree)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	owner, err := tree.resourceOwner(ctx, tx, binding.SpaceID, binding.FileID)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	if owner.Scope != ownerEntity || owner.ID != claims.EntityID {
		return PinnedResourceRead{}, errOperationForbidden
	}
	if err := purpose.ValidateResource(ctx, tx, binding.ResourceID, owner); err != nil {
		return PinnedResourceRead{}, err
	}
	if err := pinnedResourceAccess(ctx, tx, authorizer, binding.SpaceID, binding.FileID); err != nil {
		return PinnedResourceRead{}, err
	}
	result, object, err := p.content(ctx, tx, binding.SpaceID, binding.FileID, *binding.VersionID)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	result.BindingID, result.ResourceID, result.Purpose, result.ResourceKind = binding.ID, binding.ResourceID, binding.Purpose, binding.ResourceKind
	reader, err := openStoredObject(ctx, p.module.driver, object)
	if err != nil {
		return PinnedResourceRead{}, err
	}
	if err := p.module.operations.appendNodeAudit(ctx, tx, claims, "file_resource_downloaded", binding.FileID, map[string]any{"binding_id": binding.ID, "version_id": result.VersionID, "size": result.Size}); err != nil {
		_ = reader.Close()
		return PinnedResourceRead{}, err
	}
	return PinnedResourceRead{PinnedResource: result, Reader: reader}, nil
}

// Only an already-authorized resource operation can cross the management
// boundary. Reuse the normal IAM/root/ACL evaluator without changing stored
// nodes or granting ordinary file routes access to managed resources.
func pinnedResourceAccess(ctx context.Context, tx *gorm.DB, a *nodeAuthorizer, spaceID, fileID guid.ID) error {
	permissions, failures, err := a.tree.loadNodePermissionsBatch(ctx, tx, spaceID, []guid.ID{fileID})
	if err != nil {
		return err
	}
	if err := failures[fileID]; err != nil {
		return err
	}
	permission, ok := permissions[fileID]
	if !ok || permission.managementMode != "resource" {
		return errOperationForbidden
	}
	permission.managementMode = "user"
	for _, action := range []string{"traverse", "download"} {
		if !a.tokenAllows(action) {
			return errOperationForbidden
		}
		constraint, err := a.constraint(ctx, tx, filePermissionPrefix+action)
		if err != nil {
			return err
		}
		management, err := a.constraint(ctx, tx, fileReadAllPermission)
		if err != nil {
			return err
		}
		allowed, err := a.authorizeLoaded(ctx, tx, permission, constraint, management, action)
		if err != nil {
			return err
		}
		if !allowed {
			return errOperationForbidden
		}
	}
	return nil
}

func (m *Module) providePinnedResources(services *module.Services) error {
	return module.Provide[PinnedResources](services, pinnedResources{module: m})
}
