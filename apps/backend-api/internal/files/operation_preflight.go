package files

import (
	"context"
	"errors"
	"sort"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

type operationSlot struct {
	parent guid.ID
	key    string
}

// operationSimulation is a bounded, lazy overlay, not a second filesystem. The
// same policy walker handles simulation and execution; only persistence differs.
// Negative IDs exist only here and never cross a database, IAM or JSON boundary.
type operationSimulation struct {
	nextID   guid.ID
	nodes    map[guid.ID]*model.Node
	slots    map[operationSlot]guid.ID
	children map[guid.ID]map[guid.ID]bool
	loaded   map[operationSlot]bool
}

type operationCost struct {
	Nodes       int `json:"nodes"`
	ClosureRows int `json:"closure_rows"`
	Created     int `json:"created"`
	Moved       int `json:"moved"`
	Overwritten int `json:"overwritten"`
	Skipped     int `json:"skipped"`
	Recycled    int `json:"recycled"`
}

func (t directoryTree) preflightTargets(ctx context.Context, tx *gorm.DB, operation *model.Operation, request operationRequest, roots []guid.ID, access operationAccess) (operationCost, error) {
	view := &operationSimulation{nodes: make(map[guid.ID]*model.Node), slots: make(map[operationSlot]guid.ID), children: make(map[guid.ID]map[guid.ID]bool), loaded: make(map[operationSlot]bool)}
	e := operationExecution{tree: t, ctx: ctx, tx: tx, q: query.Bind(tx, t.prefix), operation: operation, request: request, access: access, simulation: view}
	for _, rootID := range roots {
		if _, err := e.adopt(rootID, operation.DestinationID); err != nil {
			return operationCost{}, err
		}
	}
	return e.cost(), nil
}

func (e *operationExecution) cost() operationCost {
	return operationCost{e.usedNodes, e.usedClosures, e.result.Created, e.result.Moved, e.result.Overwritten, e.result.Skipped, e.result.Recycled}
}

func (s *operationSimulation) remember(e *operationExecution, row *model.Node) error {
	if _, exists := s.nodes[row.ID]; exists {
		return nil
	}
	// Budget source/target views, rename probes and ancestor reads; never load an
	// unrelated whole space merely because few nodes will eventually be changed.
	if len(s.nodes) >= 2*e.operation.NodeBudget+maxNodeDepth+1 {
		return errHierarchy
	}
	cloned := *row
	s.nodes[row.ID] = &cloned
	if row.ParentID != nil {
		parent := *row.ParentID
		if s.children[parent] == nil {
			s.children[parent] = make(map[guid.ID]bool)
		}
		s.children[parent][row.ID] = true
		if row.State == "active" {
			slot := operationSlot{parent, row.NameKey}
			if _, known := s.slots[slot]; !known {
				s.slots[slot] = row.ID
			}
		}
	}
	return nil
}

func (e *operationExecution) operationNode(id guid.ID) (*model.Node, error) {
	if e.simulation != nil {
		if row, known := e.simulation.nodes[id]; known {
			return row, nil
		}
		if id < 0 {
			return nil, gorm.ErrRecordNotFound
		}
	}
	node := e.q.Node
	row, err := node.WithContext(e.ctx).Where(node.SpaceID.Eq(e.operation.SpaceID), node.ID.Eq(id)).Take()
	if err != nil || e.simulation == nil {
		return row, err
	}
	if err := e.simulation.remember(e, row); err != nil {
		return nil, err
	}
	return e.simulation.nodes[id], nil
}

func (e *operationExecution) operationTarget(parentID guid.ID, key string) (*model.Node, error) {
	slot := operationSlot{parentID, key}
	if e.simulation != nil {
		if id, known := e.simulation.slots[slot]; known {
			if id == 0 {
				return nil, gorm.ErrRecordNotFound
			}
			return e.simulation.nodes[id], nil
		}
		if parentID < 0 {
			return nil, gorm.ErrRecordNotFound
		}
	}
	node := e.q.Node
	row, err := node.WithContext(e.ctx).Where(node.SpaceID.Eq(e.operation.SpaceID), node.ParentID.Eq(parentID), node.NameKey.Eq(key), node.LiveSlot.Eq(1)).Take()
	if e.simulation == nil {
		return row, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	if err := e.simulation.remember(e, row); err != nil {
		return nil, err
	}
	return e.simulation.nodes[row.ID], nil
}

func (e *operationExecution) operationPath(id guid.ID) ([]*model.Node, error) {
	if e.simulation == nil {
		return e.tree.pathNodes(e.ctx, e.tx, e.operation.SpaceID, id)
	}
	rows := make([]*model.Node, 0, maxNodeDepth+1)
	for {
		if len(rows) > maxNodeDepth {
			return nil, errHierarchy
		}
		row, err := e.operationNode(id)
		if err != nil {
			return nil, err
		}
		if row.State != "active" {
			return nil, ErrNotFound
		}
		rows = append(rows, row)
		if row.ParentID == nil {
			break
		}
		id = *row.ParentID
	}
	for start, end := 0, len(rows)-1; start < end; start, end = start+1, end-1 {
		rows[start], rows[end] = rows[end], rows[start]
	}
	return rows, nil
}

func (s *operationSimulation) loadChildren(e *operationExecution, parent guid.ID, includeTrash bool) error {
	if parent < 0 {
		return nil
	}
	// The state/ID cursor is indexed. Include old trash for subtree moves, but
	// source adoption itself only visits active direct children.
	node := e.q.Node
	for _, state := range []string{"active", "trashed"} {
		cacheKey := operationSlot{parent, state}
		if s.loaded[cacheKey] || (state == "trashed" && !includeTrash) {
			continue
		}
		var after guid.ID
		for {
			rows, err := node.WithContext(e.ctx).Where(node.SpaceID.Eq(e.operation.SpaceID), node.ParentID.Eq(parent), node.State.Eq(state), node.ID.Gt(after)).Order(node.ID.Asc()).Limit(100).Find()
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err := s.remember(e, row); err != nil {
					return err
				}
			}
			if len(rows) < 100 {
				break
			}
			after = rows[len(rows)-1].ID
		}
		s.loaded[cacheKey] = true
	}
	return nil
}

func prospectiveAfter(id, cursor guid.ID) bool {
	if id < 0 {
		return cursor >= 0 || id < cursor
	}
	return cursor >= 0 && id > cursor
}

func (e *operationExecution) operationChildren(parentID, after guid.ID) ([]*model.Node, error) {
	node := e.q.Node
	if e.simulation == nil {
		return node.WithContext(e.ctx).Where(node.SpaceID.Eq(e.operation.SpaceID), node.ParentID.Eq(parentID), node.State.Eq("active"), node.ID.Gt(after)).Order(node.ID.Asc()).Limit(100).Find()
	}
	if err := e.simulation.loadChildren(e, parentID, false); err != nil {
		return nil, err
	}
	rows := make([]*model.Node, 0)
	for id := range e.simulation.children[parentID] {
		row := e.simulation.nodes[id]
		if row.State == "active" && prospectiveAfter(id, after) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return prospectiveAfter(rows[j].ID, rows[i].ID) })
	return rows[:min(100, len(rows))], nil
}

func (s *operationSimulation) subtree(e *operationExecution, root guid.ID, includeTrash bool) ([]*model.Node, error) {
	rows := make([]*model.Node, 0)
	var walk func(guid.ID, int) error
	walk = func(id guid.ID, depth int) error {
		if err := e.ctx.Err(); err != nil {
			return err
		}
		if depth > maxNodeDepth || len(rows) >= e.operation.NodeBudget {
			return errHierarchy
		}
		row, err := e.operationNode(id)
		if err != nil {
			return err
		}
		rows = append(rows, row)
		if row.Kind != "directory" {
			return nil
		}
		if err := s.loadChildren(e, id, includeTrash); err != nil {
			return err
		}
		ids := make([]guid.ID, 0, len(s.children[id]))
		for child := range s.children[id] {
			if includeTrash || s.nodes[child].State == "active" {
				ids = append(ids, child)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return prospectiveAfter(ids[j], ids[i]) })
		for _, child := range ids {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	return rows, nil
}

func (e *operationExecution) operationSubtreeCount(root guid.ID) (int64, error) {
	if e.simulation != nil {
		rows, err := e.simulation.subtree(e, root, true)
		return int64(len(rows)), err
	}
	closure := e.q.NodeClosure
	return closure.WithContext(e.ctx).Where(closure.SpaceID.Eq(e.operation.SpaceID), closure.AncestorID.Eq(root)).Count()
}

func (s *operationSimulation) move(e *operationExecution, source *model.Node, parentID guid.ID, input string) error {
	name, key, err := PortableName(input)
	if err != nil {
		return err
	}
	parentPath, err := e.operationPath(parentID)
	if err != nil {
		return err
	}
	for _, ancestor := range parentPath {
		if ancestor.ID == source.ID {
			return errHierarchy
		}
	}
	rows, err := s.subtree(e, source.ID, true)
	if err != nil {
		return err
	}
	oldParent, oldName := source.ParentID, source.NameKey
	source.ParentID = &parentID
	for _, row := range rows {
		// Deleted descendants still need a valid depth after moving an ancestor.
		depth, current := 0, row
		for current.ID != source.ID {
			depth++
			if depth > maxNodeDepth || current.ParentID == nil {
				source.ParentID = oldParent
				return errHierarchy
			}
			current = s.nodes[*current.ParentID]
			if current == nil {
				source.ParentID = oldParent
				return errHierarchy
			}
		}
		if depth > maxNodeDepth-len(parentPath) {
			source.ParentID = oldParent
			return errHierarchy
		}
	}
	if oldParent != nil {
		delete(s.children[*oldParent], source.ID)
		s.slots[operationSlot{*oldParent, oldName}] = 0
	}
	if s.children[parentID] == nil {
		s.children[parentID] = make(map[guid.ID]bool)
	}
	s.children[parentID][source.ID] = true
	source.Name, source.NameKey = name, key
	if e.immediate != nil {
		source.Version++
	}
	s.slots[operationSlot{parentID, key}] = source.ID
	return nil
}

func (s *operationSimulation) recycle(e *operationExecution, rootID guid.ID, replacement bool) error {
	if replacement && e.immediate != nil {
		selected, err := e.containsSelectedSource(rootID)
		if err != nil {
			return err
		}
		if selected {
			return errHierarchy
		}
	}
	rows, err := s.subtree(e, rootID, false)
	if err != nil {
		return err
	}
	count := 0
	for _, row := range rows {
		if replacement && row.ID > 0 && e.immediate == nil {
			item := e.q.OperationItem
			selected, err := item.WithContext(e.ctx).Where(item.OperationID.Eq(e.operation.ID), item.NodeID.Eq(row.ID)).Count()
			if err != nil {
				return err
			}
			if selected != 0 {
				return errHierarchy
			}
		}
		if row.State == "active" {
			count++
			if err := e.allow(row.ID, "delete"); err != nil {
				return err
			}
		}
	}
	if count == 0 {
		return errHierarchy
	}
	if replacement {
		if err := e.consumeNodes(count); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if row.State == "active" {
			row.State = "trashed"
			if e.immediate != nil {
				row.Version++
			}
			if row.ParentID != nil {
				s.slots[operationSlot{*row.ParentID, row.NameKey}] = 0
			}
		}
	}
	e.result.Recycled += count
	if e.immediate != nil {
		e.spaceRevision++
	}
	if e.immediate != nil && !replacement {
		e.immediate.adopted[rootID] = true
	}
	return nil
}
