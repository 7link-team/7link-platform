package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authz"
	"gorm.io/gorm"
)

func (m *Module) resolveFileOwner(ctx context.Context, fileID guid.ID) (authz.ResourceOwner, bool, error) {
	if m == nil || m.runtime == nil || m.runtime.Database() == nil || m.runtime.Database().Write() == nil ||
		m.entityOwner == nil || m.deptOwner == nil || fileID <= 0 {
		return authz.ResourceOwner{}, false, errOperationUnavailable
	}
	db := m.runtime.Database().Write().WithContext(ctx)
	tree := directoryTree{prefix: m.runtime.TablePrefix()}
	q := query.Bind(db, tree.prefix)
	node, err := q.Node.WithContext(ctx).Where(q.Node.ID.Eq(fileID)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return authz.ResourceOwner{}, false, nil
	}
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	space, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(node.SpaceID)).Take()
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	owner, err := tree.resourceOwner(ctx, db, node.SpaceID, node.ID)
	if err != nil {
		return authz.ResourceOwner{}, false, err
	}
	switch owner.Scope {
	case ownerSystem:
		if space.ScopeKind != spaceSystem || space.SystemSlot == nil || *space.SystemSlot != 1 || space.TenantID != nil || space.PrincipalID != nil || owner.ID != 0 {
			return authz.ResourceOwner{}, false, errFileACL
		}
		return authz.ResourceOwner{ScopeType: "system"}, true, nil
	case ownerAccount:
		if owner.ID <= 0 || space.PrincipalID == nil || *space.PrincipalID != owner.ID {
			return authz.ResourceOwner{}, false, errFileACL
		}
		return authz.ResourceOwner{PrincipalID: owner.ID}, true, nil
	case ownerTenant:
		if space.TenantID == nil || *space.TenantID != owner.ID {
			return authz.ResourceOwner{}, false, errFileACL
		}
		return authz.ResourceOwner{TenantID: owner.ID}, true, nil
	case ownerEntity:
		resolved, found, err := m.entityOwner(ctx, owner.ID)
		if err != nil || !found {
			return authz.ResourceOwner{}, found, err
		}
		if space.TenantID == nil || resolved.TenantID != *space.TenantID || resolved.EntityID != owner.ID {
			return authz.ResourceOwner{}, false, errFileACL
		}
		return resolved, true, nil
	case ownerDepartment:
		resolved, found, err := m.deptOwner(ctx, owner.ID)
		if err != nil || !found {
			return authz.ResourceOwner{}, found, err
		}
		if space.TenantID == nil || resolved.TenantID != *space.TenantID || resolved.EntityID <= 0 {
			return authz.ResourceOwner{}, false, errFileACL
		}
		return resolved, true, nil
	default:
		return authz.ResourceOwner{}, false, errFileACL
	}
}

func (m *Module) provisionOrganizationRoot(
	ctx context.Context,
	tx *gorm.DB,
	input core.FileRootProvisionInput,
	now time.Time,
) (guid.ID, error) {
	if m == nil || tx == nil || m.runtime == nil || m.ids == nil || m.appender == nil || m.references == nil {
		return 0, errOperationUnavailable
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return 0, ErrAccountTransaction
	}
	if input.TenantID <= 0 || input.OwnerID <= 0 {
		return 0, errOperationInput
	}

	var references core.AuthorizationReferences
	switch input.Scope {
	case ownerTenant:
		if input.OwnerID != input.TenantID || input.EntityID != 0 {
			return 0, errOperationInput
		}
	case ownerEntity:
		if input.EntityID != 0 {
			return 0, errOperationInput
		}
		references.EntityID = input.OwnerID
	case ownerDepartment:
		if input.EntityID <= 0 {
			return 0, errOperationInput
		}
		references.EntityID, references.DepartmentID = input.EntityID, input.OwnerID
	default:
		return 0, errOperationInput
	}
	if references.EntityID > 0 || references.DepartmentID > 0 {
		valid, err := m.references(ctx, tx, input.TenantID, references)
		if err != nil {
			return 0, err
		}
		if !valid {
			return 0, errOperationForbidden
		}
	}

	tree := directoryTree{prefix: m.runtime.TablePrefix()}
	space, _, err := tree.ensureSpaceOn(ctx, tx, 0, input.TenantID, m.nextFileID, now)
	if err != nil {
		return 0, err
	}
	if space.ScopeKind != spaceTenant || space.TenantID == nil || *space.TenantID != input.TenantID || space.PrincipalID != nil {
		return 0, errOperationForbidden
	}

	q, space, err := tree.lockOperationSpace(ctx, tx, space.ID)
	if err != nil {
		return 0, err
	}
	binding := rootBinding{scope: input.Scope, ownerID: input.OwnerID}
	node := q.Node
	existing, err := node.WithContext(ctx).Where(
		node.SpaceID.Eq(space.ID), node.RootScope.Eq(input.Scope),
		node.RootOwnerID.Eq(input.OwnerID), node.State.Eq("active"),
	).Take()
	if err == nil {
		if !validRootBinding(space, existing, binding) {
			return 0, errFileACL
		}
		return existing.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	physicalRoot, err := node.WithContext(ctx).Where(
		node.SpaceID.Eq(space.ID), node.ParentID.IsNull(), node.RootSlot.Eq(1), node.State.Eq("active"),
	).Take()
	if err != nil {
		return 0, err
	}
	rootID, err := m.nextFileID()
	if err != nil {
		return 0, err
	}
	root, err := tree.createNodeOn(ctx, tx, space.ID, rootID, physicalRoot.ID, "directory", fmt.Sprintf("%s-%s", input.Scope, input.OwnerID), now)
	if err != nil {
		return 0, err
	}
	space, err = q.Space.WithContext(ctx).Where(q.Space.ID.Eq(space.ID)).Take()
	if err != nil {
		return 0, err
	}
	if err := tree.bindRoot(ctx, tx, space.ID, nodeRevision{ID: root.ID, Version: root.Version}, space.Version, binding, now); err != nil {
		return 0, err
	}
	event := audit.NewDomainEvent(ctx, input.TenantID, "file_root_created", "file_node", root.ID)
	event.Detail["scope"], event.Detail["owner_id"] = input.Scope, input.OwnerID.String()
	if input.EntityID > 0 {
		event.Detail["entity_id"] = input.EntityID.String()
	}
	if err := m.appender(ctx, tx, event); err != nil {
		return 0, err
	}
	return root.ID, nil
}

func (m *Module) nextFileID() (guid.ID, error) {
	id, err := m.ids.Next()
	if err != nil || id <= 0 {
		return 0, errors.Join(errOperationInput, err)
	}
	return guid.ID(id), nil
}

func (m *Module) organizationRootHasContent(
	ctx context.Context,
	tx *gorm.DB,
	input core.FileRootProvisionInput,
) (bool, error) {
	if m == nil || tx == nil || m.runtime == nil || input.TenantID <= 0 || input.OwnerID <= 0 {
		return false, errOperationInput
	}
	if _, active := tx.Statement.ConnPool.(gorm.TxCommitter); !active {
		return false, ErrAccountTransaction
	}
	switch input.Scope {
	case ownerEntity:
		if input.EntityID != 0 {
			return false, errOperationInput
		}
	case ownerDepartment:
		if input.EntityID <= 0 {
			return false, errOperationInput
		}
	default:
		return false, errOperationInput
	}

	tree := directoryTree{prefix: m.runtime.TablePrefix()}
	q := query.Bind(tx, tree.prefix)
	space, err := q.Space.WithContext(ctx).Where(
		q.Space.ScopeKind.Eq(spaceTenant), q.Space.TenantID.Eq(input.TenantID),
	).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	q, _, err = tree.lockOperationSpace(ctx, tx, space.ID)
	if err != nil {
		return false, err
	}
	root, err := q.Node.WithContext(ctx).Where(
		q.Node.SpaceID.Eq(space.ID), q.Node.RootScope.Eq(input.Scope),
		q.Node.RootOwnerID.Eq(input.OwnerID), q.Node.State.Eq("active"),
	).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !validRootBinding(space, root, rootBinding{scope: input.Scope, ownerID: input.OwnerID}) {
		return false, errFileACL
	}
	closure := q.NodeClosure
	count, err := closure.WithContext(ctx).Where(
		closure.SpaceID.Eq(space.ID), closure.AncestorID.Eq(root.ID), closure.DescendantID.Neq(root.ID),
	).Limit(1).Count()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
