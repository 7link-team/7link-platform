package files

import (
	"context"
	"database/sql/driver"
	"errors"
	"sort"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var (
	errTreeConflict        = errors.New("files: tree changed or restore placement is unavailable")
	errRestoreNameConflict = errors.Join(errTreeConflict, errors.New("files: restore destination name is occupied"))
)

type nodeRevision struct {
	ID      guid.ID
	Version int64
}

// lockMutation requires the operation's transaction and a pinned space revision.
// Callers must roll back on any error, including errors after this write lock.
func (t directoryTree) lockMutation(ctx context.Context, tx *gorm.DB, spaceID guid.ID, expected int64) (*query.Query, error) {
	if tx == nil || spaceID <= 0 || expected <= 0 {
		return nil, errHierarchy
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, ErrAccountTransaction
	}
	q := query.Bind(tx, t.prefix)
	result, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID), q.Space.Version.Eq(expected)).UpdateSimple(q.Space.Version.Add(1))
	if err != nil {
		return nil, err
	}
	if result.RowsAffected != 1 {
		return nil, errTreeConflict
	}
	return q, nil
}

// trash hides all active descendants in one transaction. Previously trashed
// descendants keep their original batch, so restoring a parent cannot revive them.
// This is persistence only; the operation layer must authorize every affected node.
func (t directoryTree) trash(ctx context.Context, tx *gorm.DB, spaceID, batchID, actorID guid.ID, expectedSpace int64, selected []nodeRevision, budget int, now time.Time) error {
	if batchID <= 0 || actorID <= 0 || budget < 1 || budget == int(^uint(0)>>1) || len(selected) == 0 || len(selected) > budget {
		return errHierarchy
	}
	q, err := t.lockMutation(ctx, tx, spaceID, expectedSpace)
	if err != nil {
		return err
	}
	selection := make(map[guid.ID]int64, len(selected))
	for _, item := range selected {
		if item.ID <= 0 || item.Version <= 0 {
			return errHierarchy
		}
		if previous, exists := selection[item.ID]; exists && previous != item.Version {
			return errTreeConflict
		}
		selection[item.ID] = item.Version
	}
	ids := make([]guid.ID, 0, len(selection))
	for id := range selection {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	roots := make([]*model.TrashRoot, 0, len(ids))
	for _, id := range ids {
		path, err := t.pathNodes(ctx, tx, spaceID, id)
		if err != nil {
			return err
		}
		row := path[len(path)-1]
		if row.ParentID == nil || row.Version != selection[id] {
			return errTreeConflict
		}
		covered := false
		for _, ancestor := range path[:len(path)-1] {
			if _, exists := selection[ancestor.ID]; exists {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, &model.TrashRoot{SpaceID: spaceID, BatchID: batchID, NodeID: id, ParentID: *row.ParentID, ParentKind: "directory", Name: row.Name, NameKey: row.NameKey, Ext: row.Ext})
		}
	}
	node, closure := q.Node, q.NodeClosure
	affected := make([]driver.Valuer, 0)
	for _, root := range roots {
		rows, err := node.WithContext(ctx).Select(node.ID).Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.DescendantID.EqCol(node.ID)).Where(closure.SpaceID.Eq(spaceID), closure.AncestorID.Eq(root.NodeID), node.State.Eq("active")).Order(node.ID.Asc()).Limit(budget - len(affected) + 1).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 || len(rows) > budget-len(affected) {
			return errHierarchy
		}
		for _, row := range rows {
			affected = append(affected, row.ID)
		}
	}
	batch := model.TrashBatch{ID: batchID, SpaceID: spaceID, DeletedBy: actorID, DeletedAt: now.UnixMilli(), State: "active", NodeCount: len(affected), RootCount: len(roots)}
	if err := q.TrashBatch.WithContext(ctx).Create(&batch); err != nil {
		return err
	}
	for start := 0; start < len(roots); start += 100 {
		end := min(start+100, len(roots))
		if err := q.TrashRoot.WithContext(ctx).Create(roots[start:end]...); err != nil {
			return err
		}
	}
	for start := 0; start < len(affected); start += 100 {
		end := min(start+100, len(affected))
		result, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ID.In(affected[start:end]...), node.State.Eq("active")).UpdateSimple(node.State.Value("trashed"), node.LiveSlot.Null(), node.TrashBatchID.Value(batchID), node.Version.Add(1), node.UpdatedAt.Value(now.UnixMilli()))
		if err != nil {
			return err
		}
		if result.RowsAffected != int64(end-start) {
			return errTreeConflict
		}
	}
	return nil
}

// restoreOriginal restores a whole batch only when its original placements remain
// available. An explicit replacement name may resolve one root collision; destinations,
// merging, and replacement remain operation-engine concerns. Closure and data stay untouched.
func (t directoryTree) restoreOriginal(ctx context.Context, tx *gorm.DB, spaceID, batchID, actorID guid.ID, expectedSpace int64, budget int, now time.Time) error {
	return t.restoreOriginalAs(ctx, tx, spaceID, batchID, actorID, expectedSpace, budget, 0, "", now)
}

func (t directoryTree) restoreOriginalAs(ctx context.Context, tx *gorm.DB, spaceID, batchID, actorID guid.ID, expectedSpace int64, budget int, renamedNodeID guid.ID, requestedName string, now time.Time) error {
	if batchID <= 0 || actorID <= 0 || budget < 1 || budget == int(^uint(0)>>1) {
		return errHierarchy
	}
	var renamedName, renamedKey, renamedExt string
	renameRequested := requestedName != ""
	if renameRequested {
		if renamedNodeID <= 0 {
			return errHierarchy
		}
		var err error
		renamedName, renamedKey, err = PortableName(requestedName)
		if err != nil {
			return err
		}
		renamedExt = Extension(renamedName)
	}
	q, err := t.lockMutation(ctx, tx, spaceID, expectedSpace)
	if err != nil {
		return err
	}
	batch, err := q.TrashBatch.WithContext(ctx).Where(q.TrashBatch.ID.Eq(batchID), q.TrashBatch.SpaceID.Eq(spaceID), q.TrashBatch.State.Eq("active")).Take()
	if err != nil {
		return err
	}
	if batch.NodeCount > budget {
		return errHierarchy
	}
	root := q.TrashRoot
	roots, err := root.WithContext(ctx).Where(root.SpaceID.Eq(spaceID), root.BatchID.Eq(batchID)).Order(root.NodeID.Asc()).Limit(budget + 1).Find()
	if err != nil {
		return err
	}
	if len(roots) != batch.RootCount {
		return errHierarchy
	}
	node := q.Node
	renamedRootFound := renamedNodeID == 0
	for _, placement := range roots {
		parent, err := t.pathNodes(ctx, tx, spaceID, placement.ParentID)
		if err != nil {
			return err
		}
		if parent[len(parent)-1].Kind != "directory" {
			return errTreeConflict
		}
		row, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ID.Eq(placement.NodeID), node.TrashBatchID.Eq(batchID), node.State.Eq("trashed")).Take()
		if err != nil {
			return err
		}
		if row.ParentID == nil || *row.ParentID != placement.ParentID || row.Name != placement.Name || row.NameKey != placement.NameKey || row.Ext != placement.Ext {
			return errTreeConflict
		}
		nameKey := placement.NameKey
		if renameRequested && placement.NodeID == renamedNodeID {
			nameKey = renamedKey
			if row.Kind == "directory" {
				renamedExt = ""
			}
		}
		if placement.NodeID == renamedNodeID {
			renamedRootFound = true
		}
		conflicts, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ParentID.Eq(placement.ParentID), node.NameKey.Eq(nameKey), node.LiveSlot.Eq(1)).Count()
		if err != nil {
			return err
		}
		if conflicts != 0 {
			if placement.NodeID == renamedNodeID {
				return errRestoreNameConflict
			}
			return errTreeConflict
		}
	}
	if !renamedRootFound {
		return errTreeConflict
	}
	rows, err := node.WithContext(ctx).Select(node.ID).Where(node.SpaceID.Eq(spaceID), node.TrashBatchID.Eq(batchID), node.State.Eq("trashed")).Order(node.ID.Asc()).Limit(budget + 1).Find()
	if err != nil {
		return err
	}
	if len(rows) != batch.NodeCount {
		return errHierarchy
	}
	if renameRequested {
		result, err := node.WithContext(ctx).Where(
			node.SpaceID.Eq(spaceID), node.ID.Eq(renamedNodeID), node.TrashBatchID.Eq(batchID), node.State.Eq("trashed"),
		).UpdateSimple(node.Name.Value(renamedName), node.NameKey.Value(renamedKey), node.Ext.Value(renamedExt))
		if err != nil {
			return err
		}
		if result.RowsAffected != 1 {
			return errTreeConflict
		}
	}
	for start := 0; start < len(rows); start += 100 {
		end := min(start+100, len(rows))
		ids := make([]driver.Valuer, 0, end-start)
		for _, row := range rows[start:end] {
			ids = append(ids, row.ID)
		}
		result, err := node.WithContext(ctx).Where(node.SpaceID.Eq(spaceID), node.ID.In(ids...), node.TrashBatchID.Eq(batchID), node.State.Eq("trashed")).UpdateSimple(node.State.Value("active"), node.LiveSlot.Value(1), node.TrashBatchID.Null(), node.Version.Add(1), node.UpdatedAt.Value(now.UnixMilli()))
		if err != nil {
			return err
		}
		if result.RowsAffected != int64(end-start) {
			return errTreeConflict
		}
	}
	result, err := q.TrashBatch.WithContext(ctx).Where(q.TrashBatch.ID.Eq(batchID), q.TrashBatch.SpaceID.Eq(spaceID), q.TrashBatch.State.Eq("active")).UpdateSimple(q.TrashBatch.State.Value("restored"), q.TrashBatch.RestoredBy.Value(actorID), q.TrashBatch.RestoredAt.Value(now.UnixMilli()))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return errTreeConflict
	}
	return nil
}
