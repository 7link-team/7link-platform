package files

import (
	"context"
	"database/sql/driver"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

// move is an internal transaction-bound primitive. Budgets bound subtree reads
// and total removed/inserted closure rows; they are supplied by the operation
// planner. Conflict resolution and authorization must precede this primitive.
func (t directoryTree) move(ctx context.Context, tx *gorm.DB, spaceID guid.ID, source, destination nodeRevision, expectedSpace int64, input string, nodeBudget, closureBudget int, now time.Time) error {
	if source.ID <= 0 || destination.ID <= 0 || source.Version <= 0 || destination.Version <= 0 || nodeBudget < 1 || nodeBudget == int(^uint(0)>>1) || closureBudget < 1 {
		return errHierarchy
	}
	name, key, err := PortableName(input)
	if err != nil {
		return err
	}
	q, err := t.lockMutation(ctx, tx, spaceID, expectedSpace)
	if err != nil {
		return err
	}
	sourcePath, err := t.pathNodes(ctx, tx, spaceID, source.ID)
	if err != nil {
		return err
	}
	row := sourcePath[len(sourcePath)-1]
	if row.ParentID == nil || row.Version != source.Version {
		return errTreeConflict
	}
	destinationPath, err := t.pathNodes(ctx, tx, spaceID, destination.ID)
	if err != nil {
		return err
	}
	parent := destinationPath[len(destinationPath)-1]
	if parent.Kind != "directory" || parent.Version != destination.Version {
		return errTreeConflict
	}
	for _, ancestor := range destinationPath {
		if ancestor.ID == source.ID {
			return errHierarchy
		}
	}
	node := q.Node
	conflicts, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ParentID.Eq(destination.ID), node.NameKey.Eq(key), node.LiveSlot.Eq(1), node.ID.Neq(source.ID)).Count()
	if err != nil {
		return err
	}
	if conflicts != 0 {
		return errTreeConflict
	}
	if *row.ParentID != destination.ID {
		closure := q.NodeClosure
		// Include older trash descendants: moving their active ancestor also
		// changes their ancestry, but never restores their visibility or content.
		subtree, err := closure.WithContext(ctx).Where(closure.SpaceID.Eq(spaceID), closure.AncestorID.Eq(source.ID)).Order(closure.Depth.Asc(), closure.DescendantID.Asc()).Limit(nodeBudget + 1).Find()
		if err != nil {
			return err
		}
		if len(subtree) == 0 || len(subtree) > nodeBudget || subtree[0].DescendantID != source.ID || subtree[0].Depth != 0 {
			return errHierarchy
		}
		ancestorCount := len(sourcePath) - 1 + len(destinationPath)
		if len(subtree) > closureBudget/ancestorCount {
			return errHierarchy
		}
		for _, descendant := range subtree {
			if descendant.Depth < 0 || descendant.Depth > maxNodeDepth-len(destinationPath) {
				return errHierarchy
			}
		}
		oldAncestors := make([]driver.Valuer, 0, len(sourcePath)-1)
		for _, ancestor := range sourcePath[:len(sourcePath)-1] {
			oldAncestors = append(oldAncestors, ancestor.ID)
		}
		for start := 0; start < len(subtree); start += 100 {
			end := min(start+100, len(subtree))
			descendants := make([]driver.Valuer, 0, end-start)
			for _, descendant := range subtree[start:end] {
				descendants = append(descendants, descendant.DescendantID)
			}
			result, err := closure.WithContext(ctx).Where(closure.SpaceID.Eq(spaceID), closure.AncestorID.In(oldAncestors...), closure.DescendantID.In(descendants...)).Delete()
			if err != nil {
				return err
			}
			if result.RowsAffected != int64(len(oldAncestors)*(end-start)) {
				return errHierarchy
			}
		}
		links := make([]*model.NodeClosure, 0, 100)
		for index, ancestor := range destinationPath {
			for _, descendant := range subtree {
				links = append(links, &model.NodeClosure{SpaceID: spaceID, AncestorID: ancestor.ID, DescendantID: descendant.DescendantID, Depth: len(destinationPath) - index + descendant.Depth})
				if len(links) == 100 {
					if err := closure.WithContext(ctx).Create(links...); err != nil {
						return err
					}
					links = links[:0]
				}
			}
		}
		if len(links) > 0 {
			if err := closure.WithContext(ctx).Create(links...); err != nil {
				return err
			}
		}
	}
	ext := ""
	if row.Kind == "file" {
		ext = Extension(name)
	}
	result, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ID.Eq(source.ID), node.State.Eq("active"), node.Version.Eq(source.Version)).UpdateSimple(node.ParentID.Value(destination.ID), node.Name.Value(name), node.NameKey.Value(key), node.Ext.Value(ext), node.Version.Add(1), node.UpdatedAt.Value(now.UnixMilli()))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return errTreeConflict
	}
	return nil
}
