package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

type operationResult struct {
	Created     int       `json:"created"`
	Moved       int       `json:"moved"`
	Overwritten int       `json:"overwritten"`
	Skipped     int       `json:"skipped"`
	Recycled    int       `json:"recycled"`
	Roots       []guid.ID `json:"roots"`
}

type operationExecution struct {
	tree          directoryTree
	ctx           context.Context
	tx            *gorm.DB
	q             *query.Query
	operation     *model.Operation
	request       operationRequest
	next          func() (int64, error)
	access        operationAccess
	now           time.Time
	spaceRevision int64
	usedNodes     int
	usedClosures  int
	result        operationResult
	simulation    *operationSimulation
	immediate     *immediateOperationState
}

// executeOperation performs no storage I/O and never commits independently.
// The caller must roll back EVERY error and persist its audit in this transaction.
// A succeeded replay returns the stored result, never repeats tree mutations.
func (t directoryTree) executeOperation(ctx context.Context, tx *gorm.DB, spaceID, id, actorID guid.ID, revision int64, now time.Time, next func() (int64, error), access operationAccess) (operationResult, error) {
	var result operationResult
	if tx == nil || spaceID <= 0 || id <= 0 || actorID <= 0 || revision <= 0 || next == nil || access == nil {
		return result, errOperationInput
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return result, ErrAccountTransaction
	}
	q, _, err := t.lockOperationSpace(ctx, tx, spaceID)
	if err != nil {
		return result, err
	}
	op := q.Operation
	operation, err := op.WithContext(ctx).Where(op.SpaceID.Eq(spaceID), op.ID.Eq(id), op.ActorID.Eq(actorID)).Take()
	if err != nil {
		return result, err
	}
	if operation.State == "succeeded" {
		return replayOperationResult(ctx, tx, operation, access)
	}
	if operation.State != "ready" || operation.Revision != revision || operation.ExpiresAt <= now.UnixMilli() {
		return result, errOperationState
	}
	var planned operationCost
	if err := json.Unmarshal([]byte(operation.PlanJSON), &planned); err != nil || planned.Nodes < 1 || planned.Nodes > operation.NodeBudget || planned.ClosureRows < 0 {
		return result, errOperationState
	}
	if _, err := t.fencePlan(ctx, tx, operation, "ready"); err != nil {
		return result, err
	}
	if err := access(ctx, tx, spaceID, actorID, operation.DestinationID, "create"); err != nil {
		return result, err
	}
	if err := validateOperationPins(ctx, q, operation, tx, access); err != nil {
		return result, err
	}
	var request operationRequest
	var roots []guid.ID
	if err := json.Unmarshal([]byte(operation.RequestJSON), &request); err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(operation.RootsJSON), &roots); err != nil {
		return result, err
	}
	if request.SpaceID != spaceID || request.Action != operation.Action || request.DestinationID != operation.DestinationID || len(roots) == 0 || len(roots) > operation.NodeBudget {
		return result, errOperationInput
	}
	_, _, hash, err := operationCanonical(request, operation.IdempotencyKey, operationBudget{operation.NodeBudget, operation.ClosureBudget})
	if err != nil || hash != operation.RequestHash {
		return result, errOperationInput
	}
	selectedRoots := make(map[guid.ID]bool, len(roots))
	requestedRoots := make(map[guid.ID]bool, len(request.Sources))
	for _, sourceID := range request.Sources {
		requestedRoots[sourceID] = true
	}
	for _, rootID := range roots {
		if !requestedRoots[rootID] || selectedRoots[rootID] {
			return result, errOperationInput
		}
		selectedRoots[rootID] = true
	}
	destination, err := t.pathNodes(ctx, tx, spaceID, operation.DestinationID)
	if err != nil {
		return result, err
	}
	parent := destination[len(destination)-1]
	if parent.Kind != "directory" || parent.Version != operation.DestinationVersion {
		return result, errTreeConflict
	}
	if _, err := op.WithContext(ctx).Where(op.ID.Eq(id)).UpdateSimple(op.State.Value("committing")); err != nil {
		return result, err
	}
	execution := operationExecution{tree: t, ctx: ctx, tx: tx, q: q, operation: operation, request: request, next: next, access: access, now: now, spaceRevision: operation.SpaceRevision}
	execution.result.Roots = make([]guid.ID, 0, len(roots))
	for _, rootID := range roots {
		adopted, err := execution.adopt(rootID, operation.DestinationID)
		if err != nil {
			return result, err
		}
		if adopted > 0 {
			execution.result.Roots = append(execution.result.Roots, adopted)
		}
	}
	if execution.cost() != planned {
		return result, errOperationState
	}
	data, err := json.Marshal(execution.result)
	if err != nil {
		return result, err
	}
	if err := releaseOperationPins(ctx, q, id); err != nil {
		return result, err
	}
	if _, err := op.WithContext(ctx).Where(op.ID.Eq(id)).UpdateSimple(op.State.Value("succeeded"), op.ResultJSON.Value(string(data)), op.CompletedAt.Value(now.UnixMilli()), op.Revision.Add(1)); err != nil {
		return result, err
	}
	return execution.result, nil
}

func validateOperationPins(ctx context.Context, q *query.Query, operation *model.Operation, tx *gorm.DB, access operationAccess) error {
	item, node := q.OperationItem, q.Node
	after, count := 0, 0
	for {
		rows, err := item.WithContext(ctx).Where(item.OperationID.Eq(operation.ID), item.Ordinal.Gt(after)).Order(item.Ordinal.Asc()).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			count++
			if count > operation.NodeBudget || row.Ordinal != count || row.SpaceID != operation.SpaceID || row.PinnedNodeID == nil || *row.PinnedNodeID != row.NodeID {
				return errOperationState
			}
			if err := access(ctx, tx, operation.SpaceID, operation.ActorID, row.NodeID, operation.Action); err != nil {
				return err
			}
			current, err := node.WithContext(ctx).Where(node.SpaceID.Eq(operation.SpaceID), node.ID.Eq(row.NodeID)).Take()
			if err != nil {
				return err
			}
			data, err := json.Marshal(current)
			if err != nil {
				return err
			}
			if string(data) != row.SnapshotJSON {
				return errTreeConflict
			}
			if (current.CurrentVersionID == nil) != (row.PinnedVersionID == nil) || (current.CurrentVersionID != nil && *current.CurrentVersionID != *row.PinnedVersionID) {
				return errOperationState
			}
		}
		after = rows[len(rows)-1].Ordinal
	}
	if count == 0 || count != operation.ItemCount {
		return errOperationState
	}
	return nil
}

func (e *operationExecution) allow(id guid.ID, action string) error {
	// Prospective nodes have no persisted IAM identity yet. Their real identity
	// and inherited authorization are independently checked during execution.
	if e.simulation != nil && id < 0 {
		return nil
	}
	return e.access(e.ctx, e.tx, e.operation.SpaceID, e.operation.ActorID, id, action)
}

func (e *operationExecution) newID() (guid.ID, error) {
	if e.simulation != nil {
		e.simulation.nextID--
		return e.simulation.nextID, nil
	}
	id, err := e.next()
	if err != nil || id <= 0 {
		return 0, errors.Join(errOperationInput, err)
	}
	return guid.ID(id), nil
}

func (e *operationExecution) consumeNodes(count int) error {
	if count < 0 || count > e.operation.NodeBudget-e.usedNodes {
		return errHierarchy
	}
	e.usedNodes += count
	return nil
}

func (e *operationExecution) adopt(sourceID, parentID guid.ID) (guid.ID, error) {
	if err := e.consumeNodes(1); err != nil {
		return 0, err
	}
	source, err := e.operationNode(sourceID)
	if err != nil {
		return 0, err
	}
	if source.State != "active" {
		return 0, ErrNotFound
	}
	if err := e.allow(sourceID, e.request.Action); err != nil {
		return 0, err
	}
	if err := e.allow(parentID, "create"); err != nil {
		return 0, err
	}
	target, err := e.operationTarget(parentID, source.NameKey)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return e.place(source, parentID, source.Name)
	}
	if err != nil {
		return 0, err
	}
	policy := e.request.TypePolicy
	if source.Kind == target.Kind {
		if source.Kind == "file" {
			policy = e.request.FilePolicy
		} else {
			policy = e.request.DirectoryPolicy
		}
	}
	selfCopy := target.ID == source.ID && e.request.Action == "copy"
	if selfCopy && policy == "rename" {
		name, err := e.uniqueName(parentID, source)
		if err != nil {
			return 0, err
		}
		return e.place(source, parentID, name)
	}
	// Refuse selected-source targets and ancestors of a source: a replacement
	// must not destroy another input or cause an overlapping directory merge.
	if target.ID > 0 && !(selfCopy && policy == "ask" && e.immediate != nil) {
		selected, err := e.selectedSourceTarget(target.ID)
		if err != nil {
			return 0, err
		}
		closure := e.q.NodeClosure
		ancestor, err := closure.WithContext(e.ctx).Where(closure.SpaceID.Eq(e.operation.SpaceID), closure.AncestorID.Eq(target.ID), closure.DescendantID.Eq(source.ID)).Count()
		if err != nil {
			return 0, err
		}
		if selected || ancestor != 0 {
			return 0, errHierarchy
		}
	}
	if err := e.allow(target.ID, "read"); err != nil {
		return 0, err
	}
	if e.immediate != nil && policy == "ask" {
		policy, err = e.resolveImmediateConflict(source, target, parentID)
		if err != nil {
			return 0, err
		}
	}
	switch policy {
	case "skip":
		e.result.Skipped++
		return 0, nil
	case "rename":
		name, err := e.uniqueName(parentID, source)
		if err != nil {
			return 0, err
		}
		return e.place(source, parentID, name)
	case "replace":
		if err := e.recycle(target.ID, true); err != nil {
			return 0, err
		}
		return e.place(source, parentID, source.Name)
	case "overwrite":
		if source.Kind != "file" || target.Kind != "file" {
			return 0, errOperationInput
		}
		if err := e.consumeNodes(1); err != nil {
			return 0, err
		}
		if err := e.appendContent(source, target); err != nil {
			return 0, err
		}
		e.result.Overwritten++
		if e.request.Action == "move" {
			if err := e.recycle(source.ID, false); err != nil {
				return 0, err
			}
		}
		return target.ID, nil
	case "merge":
		if source.Kind != "directory" || target.Kind != "directory" {
			return 0, errOperationInput
		}
		if err := e.walkChildren(source.ID, target.ID); err != nil {
			return 0, err
		}
		if e.request.Action == "move" {
			children, err := e.operationChildren(source.ID, 0)
			if err != nil {
				return 0, err
			}
			if len(children) == 0 {
				if err := e.recycle(source.ID, false); err != nil {
					return 0, err
				}
			}
		}
		return target.ID, nil
	default:
		return 0, errOperationInput
	}
}

func (e *operationExecution) walkChildren(sourceID, targetID guid.ID) error {
	var after guid.ID
	for {
		rows, err := e.operationChildren(sourceID, after)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		// Keyset pagination remains correct when earlier children leave the source.
		after = rows[len(rows)-1].ID
		for _, row := range rows {
			if _, err := e.adopt(row.ID, targetID); err != nil {
				return err
			}
		}
	}
}

func (e *operationExecution) place(source *model.Node, parentID guid.ID, name string) (guid.ID, error) {
	if e.request.Action == "move" {
		return e.moveSource(source, parentID, name)
	}
	created, err := e.createNode(source.Kind, parentID, name)
	if err != nil {
		return 0, err
	}
	if e.immediate != nil {
		e.immediate.createdFrom[created.ID] = source.ID
	}
	if source.Kind == "file" {
		if err := e.appendContent(source, created); err != nil {
			return 0, err
		}
	} else if err := e.walkChildren(source.ID, created.ID); err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (e *operationExecution) moveSource(source *model.Node, parentID guid.ID, name string) (guid.ID, error) {
	if err := e.authorizeImmediateMove(source.ID); err != nil {
		return 0, err
	}
	sourcePath, err := e.operationPath(source.ID)
	if err != nil {
		return 0, err
	}
	parentPath, err := e.operationPath(parentID)
	if err != nil {
		return 0, err
	}
	count, err := e.operationSubtreeCount(source.ID)
	if err != nil {
		return 0, err
	}
	if count < 1 || count-1 > int64(e.operation.NodeBudget-e.usedNodes) {
		return 0, errHierarchy
	}
	if err := e.consumeNodes(int(count - 1)); err != nil {
		return 0, err
	}
	cost := 0
	if source.ParentID == nil || *source.ParentID != parentID {
		ancestors := len(sourcePath) - 1 + len(parentPath)
		if count > int64((e.operation.ClosureBudget-e.usedClosures)/ancestors) {
			return 0, errHierarchy
		}
		cost = int(count) * ancestors
	}
	parent := parentPath[len(parentPath)-1]
	if e.simulation != nil {
		if err := e.simulation.move(e, source, parent.ID, name); err != nil {
			return 0, err
		}
	} else {
		if err := e.tree.move(e.ctx, e.tx, e.operation.SpaceID, nodeRevision{source.ID, source.Version}, nodeRevision{parent.ID, parent.Version}, e.spaceRevision, name, int(count), max(1, cost), e.now); err != nil {
			return 0, err
		}
	}
	e.spaceRevision++
	e.usedClosures += cost
	e.result.Moved += int(count)
	if e.immediate != nil {
		e.immediate.adopted[source.ID] = true
	}
	return source.ID, nil
}

func (e *operationExecution) createNode(kind string, parentID guid.ID, input string) (*model.Node, error) {
	name, key, err := PortableName(input)
	if err != nil {
		return nil, err
	}
	path, err := e.operationPath(parentID)
	if err != nil {
		return nil, err
	}
	if len(path) > maxNodeDepth || len(path)+1 > e.operation.ClosureBudget-e.usedClosures {
		return nil, errHierarchy
	}
	if err := e.allow(parentID, "create"); err != nil {
		return nil, err
	}
	id, err := e.newID()
	if err != nil {
		return nil, err
	}
	if e.simulation == nil {
		if _, err := e.tree.lockMutation(e.ctx, e.tx, e.operation.SpaceID, e.spaceRevision); err != nil {
			return nil, err
		}
	}
	e.spaceRevision++
	slot := 1
	row := &model.Node{ID: id, SpaceID: e.operation.SpaceID, ParentID: &parentID, ParentKind: "directory", LiveSlot: &slot, Kind: kind, Name: name, NameKey: key, State: "active", Version: 1, CreatedAt: e.now.UnixMilli(), UpdatedAt: e.now.UnixMilli()}
	if kind == "file" {
		row.Ext = Extension(name)
	}
	if e.simulation != nil {
		if err := e.simulation.remember(e, row); err != nil {
			return nil, err
		}
		row = e.simulation.nodes[id]
		e.simulation.slots[operationSlot{parentID, key}] = id
	} else {
		if err := e.q.Node.WithContext(e.ctx).Create(row); err != nil {
			return nil, err
		}
		links := make([]*model.NodeClosure, 0, len(path)+1)
		links = append(links, &model.NodeClosure{SpaceID: e.operation.SpaceID, AncestorID: id, DescendantID: id, Depth: 0})
		for index, ancestor := range path {
			links = append(links, &model.NodeClosure{SpaceID: e.operation.SpaceID, AncestorID: ancestor.ID, DescendantID: id, Depth: len(path) - index})
		}
		if err := e.q.NodeClosure.WithContext(e.ctx).Create(links...); err != nil {
			return nil, err
		}
	}
	e.usedClosures += len(path) + 1
	e.result.Created++
	return row, nil
}

func (e *operationExecution) appendContent(source, target *model.Node) error {
	if err := e.allow(target.ID, "update"); err != nil {
		return err
	}
	version := e.q.Version
	content, err := e.effectiveContent(source)
	if err != nil {
		return err
	}
	if e.simulation != nil {
		// Resolve immutable source content without persisting prospective versions
		// or references in the read-only overlay.
		if e.immediate != nil {
			target.Version++
			e.immediate.content[target.ID] = content.ID
			e.spaceRevision++
		}
		return nil
	}
	number := int64(1)
	if target.CurrentVersionID != nil {
		current, err := version.WithContext(e.ctx).Where(version.SpaceID.Eq(e.operation.SpaceID), version.NodeID.Eq(target.ID), version.ID.Eq(*target.CurrentVersionID)).Take()
		if err != nil {
			return err
		}
		number = current.Number + 1
	}
	id, err := e.newID()
	if err != nil {
		return err
	}
	input := model.Version{ID: id, SpaceID: e.operation.SpaceID, NodeID: target.ID, Number: number, ObjectID: content.ObjectID, Ext: content.Ext, DeclaredMIME: content.DeclaredMIME, CreatedBy: e.operation.ActorID, SourceVersionID: &content.ID}
	if err := e.tree.appendVersion(e.ctx, e.tx, input, target.Version, e.now); err != nil {
		return err
	}
	e.spaceRevision++
	if e.immediate != nil {
		e.immediate.content[target.ID] = content.ID
	}
	return nil
}

func (e *operationExecution) recycle(rootID guid.ID, replacement bool) error {
	if e.simulation != nil {
		return e.simulation.recycle(e, rootID, replacement)
	}
	node, closure := e.q.Node, e.q.NodeClosure
	if replacement {
		selected, err := e.containsSelectedSource(rootID)
		if err != nil {
			return err
		}
		if selected {
			return errHierarchy
		}
	}
	var after guid.ID
	count := 0
	for {
		rows, err := node.WithContext(e.ctx).Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.DescendantID.EqCol(node.ID)).Where(closure.SpaceID.Eq(e.operation.SpaceID), closure.AncestorID.Eq(rootID), node.State.Eq("active"), closure.DescendantID.Gt(after)).Order(closure.DescendantID.Asc()).Limit(100).Find()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		count += len(rows)
		if count > e.operation.NodeBudget {
			return errHierarchy
		}
		for _, row := range rows {
			if err := e.allow(row.ID, "delete"); err != nil {
				return err
			}
		}
		after = rows[len(rows)-1].ID
	}
	if replacement {
		if err := e.consumeNodes(count); err != nil {
			return err
		}
	}
	root, err := node.WithContext(e.ctx).Where(node.SpaceID.Eq(e.operation.SpaceID), node.ID.Eq(rootID)).Take()
	if err != nil {
		return err
	}
	batchID, err := e.newID()
	if err != nil {
		return err
	}
	if err := e.tree.trash(e.ctx, e.tx, e.operation.SpaceID, batchID, e.operation.ActorID, e.spaceRevision, []nodeRevision{{root.ID, root.Version}}, e.operation.NodeBudget, e.now); err != nil {
		return err
	}
	e.spaceRevision++
	e.result.Recycled += count
	if e.immediate != nil && !replacement {
		e.immediate.adopted[rootID] = true
	}
	return nil
}

func (e *operationExecution) uniqueName(parentID guid.ID, source *model.Node) (string, error) {
	ext := ""
	if source.Kind == "file" {
		ext = path.Ext(source.Name)
	}
	stem := strings.TrimSuffix(source.Name, ext)
	for index := 1; index <= e.operation.NodeBudget; index++ {
		suffix := fmt.Sprintf(" (%d)%s", index, ext)
		base := stem
		for len(base)+len(suffix) > 255 && len(base) > 0 {
			_, size := utf8.DecodeLastRuneInString(base)
			base = base[:len(base)-size]
		}
		name, key, err := PortableName(base + suffix)
		if err != nil {
			return "", err
		}
		_, err = e.operationTarget(parentID, key)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return name, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errHierarchy
}
