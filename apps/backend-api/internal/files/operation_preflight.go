package files

import (
	"context"
	"errors"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// preflightTargets checks existing destination collisions without changing nodes,
// versions or closure rows. Execution still rechecks current authorization.
// This is not a cost plan: names created by earlier inputs need virtual reservations.
func (t directoryTree) preflightTargets(ctx context.Context, tx *gorm.DB, operation *model.Operation, request operationRequest, roots []guid.ID, access operationAccess) error {
	q := query.Bind(tx, t.prefix)
	node, closure, item := q.Node, q.NodeClosure, q.OperationItem
	visited := 0
	allow := func(id guid.ID, action string) error {
		return access(ctx, tx, operation.SpaceID, operation.ActorID, id, action)
	}
	var inspect func(guid.ID, guid.ID) error
	inspect = func(sourceID, parentID guid.ID) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > operation.NodeBudget {
			return errHierarchy
		}
		source, err := node.WithContext(ctx).Where(node.SpaceID.Eq(operation.SpaceID), node.ID.Eq(sourceID), node.State.Eq("active")).Take()
		if err != nil {
			return err
		}
		if err := allow(parentID, "create"); err != nil {
			return err
		}
		target, err := node.WithContext(ctx).Where(node.SpaceID.Eq(operation.SpaceID), node.ParentID.Eq(parentID), node.NameKey.Eq(source.NameKey), node.LiveSlot.Eq(1)).Take()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		policy := request.TypePolicy
		if source.Kind == target.Kind {
			policy = request.DirectoryPolicy
			if source.Kind == "file" {
				policy = request.FilePolicy
			}
		}
		if target.ID == source.ID && request.Action == "copy" && policy == "rename" {
			return nil
		}
		selected, err := item.WithContext(ctx).Where(item.OperationID.Eq(operation.ID), item.NodeID.Eq(target.ID)).Count()
		if err != nil {
			return err
		}
		ancestor, err := closure.WithContext(ctx).Where(closure.SpaceID.Eq(operation.SpaceID), closure.AncestorID.Eq(target.ID), closure.DescendantID.Eq(source.ID)).Count()
		if err != nil {
			return err
		}
		if selected != 0 || ancestor != 0 {
			return errHierarchy
		}
		if err := allow(target.ID, "read"); err != nil {
			return err
		}
		switch policy {
		case "skip", "rename":
			return nil
		case "overwrite":
			return allow(target.ID, "update")
		case "replace":
			selected, err := item.WithContext(ctx).Join(closure, closure.SpaceID.EqCol(item.SpaceID), closure.DescendantID.EqCol(item.NodeID)).Where(item.OperationID.Eq(operation.ID), closure.AncestorID.Eq(target.ID)).Count()
			if err != nil {
				return err
			}
			if selected != 0 {
				return errHierarchy
			}
			var after guid.ID
			count := 0
			for {
				rows, err := node.WithContext(ctx).Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.DescendantID.EqCol(node.ID)).Where(closure.SpaceID.Eq(operation.SpaceID), closure.AncestorID.Eq(target.ID), node.State.Eq("active"), closure.DescendantID.Gt(after)).Order(closure.DescendantID.Asc()).Limit(100).Find()
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					return nil
				}
				count += len(rows)
				if count > operation.NodeBudget {
					return errHierarchy
				}
				for _, row := range rows {
					if err := allow(row.ID, "delete"); err != nil {
						return err
					}
				}
				after = rows[len(rows)-1].ID
			}
		case "merge":
			var after guid.ID
			for {
				rows, err := node.WithContext(ctx).Where(node.SpaceID.Eq(operation.SpaceID), node.ParentID.Eq(source.ID), node.State.Eq("active"), node.ID.Gt(after)).Order(node.ID.Asc()).Limit(100).Find()
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					return nil
				}
				after = rows[len(rows)-1].ID
				for _, row := range rows {
					if err := inspect(row.ID, target.ID); err != nil {
						return err
					}
				}
			}
		default:
			return errOperationInput
		}
	}
	for _, rootID := range roots {
		if err := inspect(rootID, operation.DestinationID); err != nil {
			return err
		}
	}
	return nil
}
