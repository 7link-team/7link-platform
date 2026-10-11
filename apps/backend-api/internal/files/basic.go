package files

import (
	"context"
	"errors"
	"io"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/extension/authn"
	"gorm.io/gorm"
)

const defaultUploadLifetime = 30 * time.Minute

func storageActorID(ctx context.Context, fallback guid.ID) guid.ID {
	if actor := authn.ActorIDFromContext(ctx); !actor.Zero() {
		return actor
	}
	return fallback
}

type fileTrashResult struct {
	BatchID guid.ID `json:"batch_id"`
}

type fileDownload struct {
	Name   string
	MIME   string
	Size   int64
	Reader io.ReadCloser
}

func (s *operationService) ensureOwnedRoot(ctx context.Context) (fileEntryView, error) {
	var result fileEntryView
	claims, err := s.basicClaims(ctx)
	if err != nil {
		return result, err
	}
	// The caller's own space belongs to no tenant, so a request about it
	// arrives without one: personalContext drops the selection at the route.
	// There is one such space per person, so there is nothing to select and no
	// scope to pass -- the client's own "scope" field is validated where the
	// request is parsed, not here.
	if claims.TenantID != 0 || claims.EntityID != 0 {
		return result, errOperationForbidden
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		binding := rootBinding{scope: ownerAccount, ownerID: claims.PrincipalID}

		space, _, err := tree.ensureSpaceOn(workContext, tx, claims.PrincipalID, 0, s.nextID, time.Now())
		if err != nil {
			return err
		}
		if space.ScopeKind != spaceAccount || space.PrincipalID == nil || *space.PrincipalID != claims.PrincipalID {
			return errOperationForbidden
		}
		q, space, err := tree.lockOperationSpace(workContext, tx, space.ID)
		if err != nil {
			return err
		}
		node := q.Node
		existing, err := node.WithContext(workContext).Where(
			node.SpaceID.Eq(space.ID), node.RootScope.Eq(ownerAccount),
			node.RootOwnerID.Eq(claims.PrincipalID), node.State.Eq("active"),
		).Take()
		if err == nil {
			if err := authorizer.access(workContext, tx, space.ID, claims.PrincipalID, existing.ID, "traverse"); err != nil {
				return err
			}
			if err := authorizer.access(workContext, tx, space.ID, claims.PrincipalID, existing.ID, "list"); err != nil {
				return err
			}
			result, err = fileEntryViewOf(workContext, q, existing)
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		physicalRoot, err := node.WithContext(workContext).Where(node.SpaceID.Eq(space.ID), node.ParentID.IsNull(), node.RootSlot.Eq(1), node.State.Eq("active")).Take()
		if err != nil {
			return err
		}
		root := physicalRoot
		space, err = q.Space.WithContext(workContext).Where(q.Space.ID.Eq(space.ID)).Take()
		if err != nil {
			return err
		}
		if err := tree.bindRoot(workContext, tx, space.ID, nodeRevision{ID: root.ID, Version: root.Version}, space.Version, binding, time.Now()); err != nil {
			return err
		}
		root, err = node.WithContext(workContext).Where(node.ID.Eq(root.ID)).Take()
		if err != nil {
			return err
		}
		space, err = q.Space.WithContext(workContext).Where(q.Space.ID.Eq(space.ID)).Take()
		if err != nil {
			return err
		}
		rules := []nodeACLRule{{
			subject: aclSubject{kind: "owner", id: claims.PrincipalID}, effect: "allow",
			actions: []string{"list", "traverse", "read", "download", "create", "update", "rename", "move", "delete", "restore", "purge", "share", "manage_acl"},
			applies: "self_and_descendants",
		}}
		if err := tree.replaceNodeACL(workContext, tx, space.ID, nodeRevision{ID: root.ID, Version: root.Version}, space.Version, true, claims.PrincipalID, rules, time.Now(), s.ids.Next); err != nil {
			return err
		}
		root, err = node.WithContext(workContext).Where(node.ID.Eq(root.ID)).Take()
		if err != nil {
			return err
		}
		event := audit.NewDomainEvent(workContext, claims.TenantID, "file_root_created", "file_node", root.ID)
		if claims.EntityID > 0 {
			event.Detail["entity_id"] = claims.EntityID
		}
		event.Detail["scope"] = ownerAccount
		if err := s.appender(workContext, tx, event); err != nil {
			return err
		}
		result, err = fileEntryViewOf(workContext, q, root)
		return err
	})
	if err != nil {
		return fileEntryView{}, err
	}
	return result, nil
}

func (s *operationService) createDirectory(ctx context.Context, parentID guid.ID, name string) (fileEntryView, error) {
	var result fileEntryView
	err := s.mutateNode(ctx, parentID, "create", func(workContext context.Context, tx *gorm.DB, claims *authn.Claims, tree directoryTree, parent *model.Node, _ *nodeAuthorizer) error {
		id, err := s.nextID()
		if err != nil {
			return err
		}
		row, err := tree.createNodeOn(workContext, tx, parent.SpaceID, id, parent.ID, "directory", name, time.Now())
		if err != nil {
			return err
		}
		result, err = fileEntryViewOf(workContext, query.Bind(tx, tree.prefix), row)
		if err != nil {
			return err
		}
		return s.appendNodeAudit(workContext, tx, claims, "file_directory_created", row.ID, map[string]any{"parent_id": parent.ID})
	})
	return result, err
}

func (s *operationService) renameNode(ctx context.Context, nodeID guid.ID, revision int64, name string) (fileEntryView, error) {
	var result fileEntryView
	err := s.mutateNode(ctx, nodeID, "rename", func(workContext context.Context, tx *gorm.DB, claims *authn.Claims, tree directoryTree, current *model.Node, _ *nodeAuthorizer) error {
		row, err := tree.renameNodeOn(workContext, tx, current.SpaceID, current.ID, revision, name, time.Now())
		if err != nil {
			return err
		}
		result, err = fileEntryViewOf(workContext, query.Bind(tx, tree.prefix), row)
		if err != nil {
			return err
		}
		return s.appendNodeAudit(workContext, tx, claims, "file_renamed", row.ID, map[string]any{"name": row.Name})
	})
	return result, err
}

func (s *operationService) trashNode(ctx context.Context, nodeID guid.ID, revision int64) (fileTrashResult, error) {
	var result fileTrashResult
	err := s.mutateNode(ctx, nodeID, "delete", func(workContext context.Context, tx *gorm.DB, claims *authn.Claims, tree directoryTree, current *model.Node, authorizer *nodeAuthorizer) error {
		if revision <= 0 || current.Version != revision || current.ParentID == nil || current.RootScope != nil {
			return errTreeConflict
		}
		q, space, err := tree.lockOperationSpace(workContext, tx, current.SpaceID)
		if err != nil {
			return err
		}
		closure, node := q.NodeClosure, q.Node
		rows, err := node.WithContext(workContext).Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.DescendantID.EqCol(node.ID)).Where(
			closure.SpaceID.Eq(current.SpaceID), closure.AncestorID.Eq(current.ID), node.State.Eq("active"),
		).Order(closure.Depth.Asc(), node.ID.Asc()).Limit(s.budget.Nodes + 1).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 || len(rows) > s.budget.Nodes {
			return errHierarchy
		}
		for _, row := range rows {
			if err := authorizer.access(workContext, tx, current.SpaceID, claims.PrincipalID, row.ID, "delete"); err != nil {
				return err
			}
		}
		batchID, err := s.nextID()
		if err != nil {
			return err
		}
		if err := tree.trash(workContext, tx, current.SpaceID, batchID, claims.PrincipalID, space.Version, []nodeRevision{{ID: current.ID, Version: revision}}, s.budget.Nodes, time.Now()); err != nil {
			return err
		}
		result.BatchID = batchID
		return s.appendNodeAudit(workContext, tx, claims, "file_trashed", current.ID, map[string]any{"batch_id": batchID})
	})
	return result, err
}

func (s *operationService) restoreNode(ctx context.Context, nodeID guid.ID, revision int64) (fileEntryView, error) {
	return s.restoreNodeAs(ctx, nodeID, revision, "")
}

func (s *operationService) restoreNodeAs(ctx context.Context, nodeID guid.ID, revision int64, name string) (fileEntryView, error) {
	var result fileEntryView
	if nodeID <= 0 || revision <= 0 {
		return result, errOperationInput
	}
	claims, err := s.browserClaims(ctx)
	if err != nil {
		return result, err
	}
	if s.appender == nil || s.budget.Nodes < 1 {
		return result, errOperationUnavailable
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		q := query.Bind(tx, tree.prefix)
		row, err := q.Node.WithContext(workContext).Where(q.Node.ID.Eq(nodeID), q.Node.State.Eq("trashed")).Take()
		if err != nil {
			return err
		}
		if row.Version != revision || row.TrashBatchID == nil {
			return errTreeConflict
		}
		if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, "restore"); err != nil {
			return err
		}
		_, space, err := tree.lockOperationSpace(workContext, tx, row.SpaceID)
		if err != nil {
			return err
		}
		batchID := *row.TrashBatchID
		if err := tree.restoreOriginalAs(workContext, tx, row.SpaceID, batchID, claims.PrincipalID, space.Version, s.budget.Nodes, nodeID, name, time.Now()); err != nil {
			return err
		}
		restored, err := activeNode(workContext, tx, tree.prefix, nodeID)
		if err != nil {
			return err
		}
		result, err = fileEntryViewOf(workContext, q, restored)
		if err != nil {
			return err
		}
		detail := map[string]any{"batch_id": batchID}
		if name != "" {
			detail["name"] = result.Name
		}
		return s.appendNodeAudit(workContext, tx, claims, "file_restored", nodeID, detail)
	})
	return result, err
}

func (s *operationService) uploadFile(ctx context.Context, parentID guid.ID, name, declaredMIME string, source io.Reader, size int64) (fileEntryView, error) {
	var result fileEntryView
	claims, err := s.basicClaims(ctx)
	if err != nil || source == nil || size < 0 || size > s.maxUploadBytes {
		return result, errors.Join(errOperationInput, err)
	}
	name, _, err = PortableName(name)
	if err != nil {
		return result, err
	}
	if err := s.checkNodeAccess(ctx, parentID, "create"); err != nil {
		return result, err
	}
	objectID, err := s.nextID()
	if err != nil {
		return result, err
	}
	workContext, cancel := context.WithTimeout(ctx, s.uploadTimeout)
	defer cancel()
	prepared, err := s.objects.Stage(workContext, s.runtime.Database().Write(), objectID, claims.PrincipalID, "objects/"+objectID.String(), source, size, time.Now().Add(defaultUploadLifetime))
	if err != nil {
		return result, err
	}
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		parent, err := activeNode(workContext, tx, tree.prefix, parentID)
		if err != nil {
			return err
		}
		if err := authorizer.access(workContext, tx, parent.SpaceID, claims.PrincipalID, parent.ID, "traverse"); err != nil {
			return err
		}
		if err := authorizer.access(workContext, tx, parent.SpaceID, claims.PrincipalID, parent.ID, "create"); err != nil {
			return err
		}
		nodeID, err := s.nextID()
		if err != nil {
			return err
		}
		row, err := tree.createNodeOn(workContext, tx, parent.SpaceID, nodeID, parent.ID, "file", name, time.Now())
		if err != nil {
			return err
		}
		object, err := s.objects.attachFileObject(workContext, tx, prepared, claims.PrincipalID)
		if err != nil {
			return err
		}
		versionID, err := s.nextID()
		if err != nil {
			return err
		}
		version := model.Version{ID: versionID, SpaceID: row.SpaceID, NodeID: row.ID, Number: 1, ObjectID: object.ID, Ext: row.Ext, DeclaredMIME: declaredMIME, CreatedBy: claims.PrincipalID}
		if err := tree.appendVersion(workContext, tx, version, row.Version, time.Now()); err != nil {
			return err
		}
		row, err = query.Bind(tx, tree.prefix).Node.WithContext(workContext).Where(query.Bind(tx, tree.prefix).Node.ID.Eq(row.ID)).Take()
		if err != nil {
			return err
		}
		result, err = fileEntryViewOf(workContext, query.Bind(tx, tree.prefix), row)
		if err != nil {
			return err
		}
		return s.appendNodeAudit(workContext, tx, claims, "file_uploaded", row.ID, map[string]any{"parent_id": parent.ID, "size": size})
	})
	if err != nil {
		return fileEntryView{}, err
	}
	return result, nil
}

func (s *operationService) downloadFile(ctx context.Context, nodeID guid.ID) (fileDownload, error) {
	var result fileDownload
	claims, err := s.basicClaims(ctx)
	if err != nil {
		return result, err
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	err = s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		row, err := activeNode(workContext, tx, tree.prefix, nodeID)
		if err != nil {
			return err
		}
		if row.Kind != "file" || row.CurrentVersionID == nil {
			return ErrNotFound
		}
		if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, "traverse"); err != nil {
			return err
		}
		if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, "download"); err != nil {
			return err
		}
		q := query.Bind(tx, tree.prefix)
		version, err := q.Version.WithContext(workContext).Where(q.Version.ID.Eq(*row.CurrentVersionID), q.Version.NodeID.Eq(row.ID), q.Version.SpaceID.Eq(row.SpaceID)).Take()
		if err != nil {
			return err
		}
		object, err := q.Object.WithContext(workContext).Where(q.Object.ID.Eq(version.ObjectID), q.Object.State.Eq("verified")).Take()
		if err != nil {
			return err
		}
		reader, err := s.driver.Open(ctx, object.StorageKey)
		if err != nil {
			return err
		}
		result = fileDownload{Name: row.Name, MIME: object.MIME, Size: object.Size, Reader: reader}
		if err := s.appendNodeAudit(workContext, tx, claims, "file_downloaded", row.ID, map[string]any{"size": object.Size}); err != nil {
			_ = reader.Close()
			result = fileDownload{}
			return err
		}
		return nil
	})
	if err != nil {
		if result.Reader != nil {
			_ = result.Reader.Close()
		}
		return fileDownload{}, err
	}
	return result, nil
}

func (s *operationService) mutateNode(ctx context.Context, nodeID guid.ID, action string, mutation func(context.Context, *gorm.DB, *authn.Claims, directoryTree, *model.Node, *nodeAuthorizer) error) error {
	claims, err := s.basicClaims(ctx)
	if err != nil || nodeID <= 0 || mutation == nil {
		return errors.Join(errOperationInput, err)
	}
	workContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	tree := directoryTree{prefix: s.runtime.TablePrefix()}
	return s.runtime.Database().Write().WithContext(workContext).Transaction(func(tx *gorm.DB) error {
		authorizer, err := s.authorize(workContext, tx, claims, tree)
		if err != nil {
			return err
		}
		row, err := activeNode(workContext, tx, tree.prefix, nodeID)
		if err != nil {
			return err
		}
		if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, "traverse"); err != nil {
			return err
		}
		if err := authorizer.access(workContext, tx, row.SpaceID, claims.PrincipalID, row.ID, action); err != nil {
			return err
		}
		return mutation(workContext, tx, claims, tree, row, authorizer)
	})
}

func (s *operationService) checkNodeAccess(ctx context.Context, nodeID guid.ID, action string) error {
	return s.mutateNode(ctx, nodeID, action, func(context.Context, *gorm.DB, *authn.Claims, directoryTree, *model.Node, *nodeAuthorizer) error {
		return nil
	})
}

func (s *operationService) basicClaims(ctx context.Context) (*authn.Claims, error) {
	claims, err := s.browserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if s.ids == nil || s.appender == nil || s.objects == nil || s.driver == nil || s.uploadTimeout <= 0 || s.maxUploadBytes <= 0 {
		return nil, errOperationUnavailable
	}
	return claims, nil
}

func (s *operationService) nextID() (guid.ID, error) {
	id, err := s.ids.Next()
	if err != nil || id <= 0 {
		return 0, errors.Join(errOperationInput, err)
	}
	return guid.ID(id), nil
}

func activeNode(ctx context.Context, tx *gorm.DB, prefix string, nodeID guid.ID) (*model.Node, error) {
	if tx == nil || nodeID <= 0 {
		return nil, errOperationInput
	}
	node := query.Bind(tx, prefix).Node
	row, err := node.WithContext(ctx).Where(node.ID.Eq(nodeID), node.State.Eq("active")).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return row, err
}

func (s *operationService) appendNodeAudit(ctx context.Context, tx *gorm.DB, claims *authn.Claims, eventType string, nodeID guid.ID, detail map[string]any) error {
	event := audit.NewDomainEvent(ctx, claims.TenantID, eventType, "file_node", nodeID)
	event.Detail["entity_id"] = claims.EntityID
	for key, value := range detail {
		event.Detail[key] = value
	}
	return s.appender(ctx, tx, event)
}

func rootDisplayName(row *model.Node) string {
	if row == nil {
		return ""
	}
	if row.RootScope == nil {
		return browserNodeName(row)
	}
	switch *row.RootScope {
	case ownerAccount:
		return "Account files"
	case ownerTenant:
		return "Tenant files"
	case ownerEntity:
		return "Entity files"
	case ownerDepartment:
		return "Department files"
	default:
		return browserNodeName(row)
	}
}
