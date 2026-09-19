package files

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var errImmediateConflict = errors.New("files: explicit conflict decisions are required")

type immediateDecision struct {
	ConflictKey string `json:"conflict_key"`
	Policy      string `json:"policy"`
}

type immediateOperationRequest struct {
	operationRequest
	ObservedRevision int64               `json:"observed_revision"`
	Decisions        []immediateDecision `json:"decisions,omitempty"`
}

type immediateNodeIdentity struct {
	NodeID        guid.ID `json:"node_id,omitempty"`
	CreatedFromID guid.ID `json:"created_from_id,omitempty"`
}

type immediateConflictNode struct {
	immediateNodeIdentity
	Kind             string  `json:"kind"`
	Name             string  `json:"name"`
	Revision         int64   `json:"revision"`
	ContentVersionID guid.ID `json:"content_version_id,omitempty"`
	Size             *int64  `json:"size,omitempty"`
	ContentCreatedAt int64   `json:"content_created_at,omitempty"`
	DetectedMIME     string  `json:"detected_mime,omitempty"`
}

type immediateConflict struct {
	Key      string                `json:"key,omitempty"`
	Kind     string                `json:"kind"`
	Source   immediateConflictNode `json:"source"`
	Target   immediateConflictNode `json:"target"`
	Parent   immediateNodeIdentity `json:"parent"`
	Policies []string              `json:"policies"`
}

// immediateConflicts is a transient response, never a persisted pending task.
// Skipped frontiers can reveal additional nested conflicts after confirmation.
type immediateConflicts struct {
	SpaceRevision int64               `json:"space_revision"`
	Conflicts     []immediateConflict `json:"conflicts"`
}

func (*immediateConflicts) Error() string { return errImmediateConflict.Error() }
func (*immediateConflicts) Unwrap() error { return errImmediateConflict }

type immediateOperationState struct {
	roots       []guid.ID
	decisions   map[string]string
	seen        map[string]bool
	conflicts   []immediateConflict
	createdFrom map[guid.ID]guid.ID
	content     map[guid.ID]guid.ID
	adopted     map[guid.ID]bool
}

func operationPolicies(kind string) []string {
	switch kind {
	case "file":
		return []string{"overwrite", "skip", "rename"}
	case "directory":
		return []string{"merge", "replace", "skip", "rename"}
	case "type":
		return []string{"replace", "skip", "rename"}
	default:
		return nil
	}
}

func operationPolicyAllowed(kind, policy string) bool {
	return slices.Contains(operationPolicies(kind), policy)
}

func canonicalImmediateRequest(input immediateOperationRequest, key string, budget operationBudget) (immediateOperationRequest, string, string, error) {
	request, _, _, err := canonicalOperationRequest(input.operationRequest, key, budget, true)
	if err != nil || input.ObservedRevision < 0 || len(input.Decisions) > budget.Nodes || (len(input.Decisions) > 0 && input.ObservedRevision == 0) {
		return input, "", "", errors.Join(errOperationInput, err)
	}
	input.operationRequest = request
	input.Decisions = slices.Clone(input.Decisions)
	slices.SortFunc(input.Decisions, func(a, b immediateDecision) int { return strings.Compare(a.ConflictKey, b.ConflictKey) })
	for index, decision := range input.Decisions {
		decoded, err := hex.DecodeString(decision.ConflictKey)
		if err != nil || len(decoded) != sha256.Size || decision.ConflictKey != strings.ToLower(decision.ConflictKey) || !slices.Contains([]string{"overwrite", "merge", "replace", "skip", "rename"}, decision.Policy) || (index > 0 && input.Decisions[index-1].ConflictKey == decision.ConflictKey) {
			return input, "", "", errOperationInput
		}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return input, "", "", err
	}
	digest := sha256.Sum256(data)
	return input, string(data), hex.EncodeToString(digest[:]), nil
}

func newImmediateState(roots []guid.ID, decisions []immediateDecision) *immediateOperationState {
	state := &immediateOperationState{roots: roots, decisions: make(map[string]string), seen: make(map[string]bool), createdFrom: make(map[guid.ID]guid.ID), content: make(map[guid.ID]guid.ID), adopted: make(map[guid.ID]bool)}
	for _, decision := range decisions {
		state.decisions[decision.ConflictKey] = decision.Policy
	}
	return state
}

func (e *operationExecution) immediateIdentity(id guid.ID) immediateNodeIdentity {
	if source, ok := e.immediate.createdFrom[id]; ok {
		return immediateNodeIdentity{CreatedFromID: source}
	}
	return immediateNodeIdentity{NodeID: id}
}

func (e *operationExecution) effectiveContent(node *model.Node) (*model.Version, error) {
	if node.CurrentVersionID == nil && (e.immediate == nil || e.immediate.content[node.ID] == 0) {
		return nil, errVersionConflict
	}
	var id guid.ID
	if node.CurrentVersionID != nil {
		id = *node.CurrentVersionID
	}
	if e.immediate != nil && e.immediate.content[node.ID] != 0 {
		id = e.immediate.content[node.ID]
	}
	version := e.q.Version
	read := version.WithContext(e.ctx).Where(version.SpaceID.Eq(e.operation.SpaceID), version.ID.Eq(id))
	if e.immediate == nil || e.immediate.content[node.ID] == 0 {
		read = read.Where(version.NodeID.Eq(node.ID))
	}
	return read.Take()
}

func (e *operationExecution) immediateSnapshot(node *model.Node) (immediateConflictNode, error) {
	snapshot := immediateConflictNode{immediateNodeIdentity: e.immediateIdentity(node.ID), Kind: node.Kind, Name: node.Name, Revision: node.Version}
	if node.Kind != "file" {
		return snapshot, nil
	}
	content, err := e.effectiveContent(node)
	if err != nil {
		return snapshot, err
	}
	object := e.q.Object
	physical, err := object.WithContext(e.ctx).Where(object.ID.Eq(content.ObjectID), object.State.Eq("verified")).Take()
	if err != nil {
		return snapshot, err
	}
	snapshot.ContentVersionID, snapshot.ContentCreatedAt = content.ID, content.CreatedAt
	snapshot.Size, snapshot.DetectedMIME = &physical.Size, physical.MIME
	return snapshot, nil
}

func (e *operationExecution) resolveImmediateConflict(source, target *model.Node, parentID guid.ID) (string, error) {
	conflict := immediateConflict{Kind: "type", Parent: e.immediateIdentity(parentID)}
	if source.Kind == target.Kind {
		conflict.Kind = source.Kind
	}
	conflict.Policies = operationPolicies(conflict.Kind)
	if source.ID == target.ID && e.request.Action == "copy" {
		conflict.Policies = []string{"rename"}
	}
	var err error
	conflict.Source, err = e.immediateSnapshot(source)
	if err != nil {
		return "", err
	}
	conflict.Target, err = e.immediateSnapshot(target)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(conflict)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	conflict.Key = hex.EncodeToString(digest[:])
	if policy, ok := e.immediate.decisions[conflict.Key]; ok {
		if !slices.Contains(conflict.Policies, policy) {
			return "", errOperationInput
		}
		e.immediate.seen[conflict.Key] = true
		return policy, nil
	}
	if e.simulation == nil {
		return "", errImmediateConflict
	}
	// Skipping only in the overlay gathers a bounded frontier without publishing
	// partial results. A later merge confirmation may reveal a deeper frontier.
	e.immediate.conflicts = append(e.immediate.conflicts, conflict)
	return "skip", nil
}

func (e *operationExecution) authorizeImmediateMove(root guid.ID) error {
	if e.immediate == nil {
		return nil
	}
	if e.simulation != nil {
		rows, err := e.simulation.subtree(e, root, true)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := e.allow(row.ID, "move"); err != nil {
				return err
			}
		}
		return nil
	}
	node, closure := e.q.Node, e.q.NodeClosure
	var after guid.ID
	count := 0
	for {
		rows, err := node.WithContext(e.ctx).Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.DescendantID.EqCol(node.ID)).Where(closure.SpaceID.Eq(e.operation.SpaceID), closure.AncestorID.Eq(root), closure.DescendantID.Gt(after)).Order(closure.DescendantID.Asc()).Limit(100).Find()
		if err != nil {
			return err
		}
		count += len(rows)
		if count > e.operation.NodeBudget {
			return errHierarchy
		}
		for _, row := range rows {
			if err := e.allow(row.ID, "move"); err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

// executeImmediateOperation simulates and executes synchronously in the caller's
// transaction. EVERY error must roll back. Persist audit with the same transaction.
// Only final success uses the existing journal for replay; conflicts/failures
// create no operation rows, source pins, leases or pending decision records.
func (t directoryTree) executeImmediateOperation(ctx context.Context, tx *gorm.DB, id, actorID guid.ID, key string, input immediateOperationRequest, budget operationBudget, expiresAt, now time.Time, next func() (int64, error), access operationAccess) (operationResult, error) {
	var result operationResult
	if tx == nil || id <= 0 || actorID <= 0 || next == nil || access == nil || expiresAt.UnixMilli() <= now.UnixMilli() {
		return result, errOperationInput
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return result, ErrAccountTransaction
	}
	request, encoded, hash, err := canonicalImmediateRequest(input, key, budget)
	if err != nil {
		return result, err
	}
	q, space, err := t.lockOperationSpace(ctx, tx, request.SpaceID)
	if err != nil {
		return result, err
	}
	op := q.Operation
	previous, err := op.WithContext(ctx).Where(op.SpaceID.Eq(space.ID), op.ActorID.Eq(actorID), op.IdempotencyKey.Eq(key)).Take()
	if err == nil {
		if previous.RequestHash != hash {
			return result, errOperationKey
		}
		if previous.State != "succeeded" {
			return result, errOperationState
		}
		return replayOperationResult(ctx, tx, previous, access)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if err := access(ctx, tx, space.ID, actorID, request.DestinationID, "create"); err != nil {
		return result, err
	}
	if request.ObservedRevision > 0 && request.ObservedRevision != space.Version {
		return result, errTreeConflict
	}
	roots, destination, count, err := t.immediateRoots(ctx, tx, request.operationRequest, budget)
	if err != nil {
		return result, err
	}
	operation := &model.Operation{ID: id, SpaceID: space.ID, ActorID: actorID, IdempotencyKey: key, RequestHash: hash, RequestJSON: encoded, Action: request.Action, Revision: 1, SpaceRevision: space.Version, DestinationID: destination.ID, DestinationVersion: destination.Version, ItemCount: count, NodeBudget: budget.Nodes, ClosureBudget: budget.ClosureRows, CreatedAt: now.UnixMilli(), ExpiresAt: expiresAt.UnixMilli()}
	view := &operationSimulation{nodes: make(map[guid.ID]*model.Node), slots: make(map[operationSlot]guid.ID), children: make(map[guid.ID]map[guid.ID]bool), loaded: make(map[operationSlot]bool)}
	preflight := operationExecution{tree: t, ctx: ctx, tx: tx, q: q, operation: operation, request: request.operationRequest, now: now, access: access, simulation: view, immediate: newImmediateState(roots, request.Decisions), spaceRevision: space.Version}
	if err := preflight.adoptImmediateRoots(roots); err != nil {
		return result, err
	}
	if len(preflight.immediate.seen) != len(request.Decisions) {
		return result, errTreeConflict
	}
	if len(preflight.immediate.conflicts) > 0 {
		return result, &immediateConflicts{SpaceRevision: space.Version, Conflicts: preflight.immediate.conflicts}
	}
	execution := operationExecution{tree: t, ctx: ctx, tx: tx, q: q, operation: operation, request: request.operationRequest, now: now, next: next, access: access, immediate: newImmediateState(roots, request.Decisions), spaceRevision: space.Version}
	if err := execution.adoptImmediateRoots(roots); err != nil {
		return result, err
	}
	if execution.cost() != preflight.cost() || execution.spaceRevision != preflight.spaceRevision || len(execution.immediate.seen) != len(request.Decisions) {
		return result, errOperationState
	}
	rootJSON, err := json.Marshal(roots)
	if err != nil {
		return result, err
	}
	resultJSON, err := json.Marshal(execution.result)
	if err != nil {
		return result, err
	}
	completedAt := now.UnixMilli()
	operation.RootsJSON, operation.ResultJSON = string(rootJSON), string(resultJSON)
	operation.State, operation.CompletedAt = "succeeded", &completedAt
	if err := op.WithContext(ctx).Create(operation); err != nil {
		return result, err
	}
	return execution.result, nil
}

func (e *operationExecution) adoptImmediateRoots(roots []guid.ID) error {
	e.result.Roots = make([]guid.ID, 0, len(roots))
	for _, rootID := range roots {
		adopted, err := e.adopt(rootID, e.operation.DestinationID)
		if err != nil {
			return err
		}
		if adopted > 0 {
			e.result.Roots = append(e.result.Roots, adopted)
		}
	}
	return nil
}

func (t directoryTree) immediateRoots(ctx context.Context, tx *gorm.DB, request operationRequest, budget operationBudget) ([]guid.ID, *model.Node, int, error) {
	path, err := t.pathNodes(ctx, tx, request.SpaceID, request.DestinationID)
	if err != nil {
		return nil, nil, 0, err
	}
	destination := path[len(path)-1]
	if destination.Kind != "directory" {
		return nil, nil, 0, errHierarchy
	}
	selected := make(map[guid.ID]bool, len(request.Sources))
	for _, id := range request.Sources {
		selected[id] = true
	}
	for _, ancestor := range path {
		if selected[ancestor.ID] {
			return nil, nil, 0, errHierarchy
		}
	}
	q := query.Bind(tx, t.prefix)
	count := 0
	roots := make([]guid.ID, 0, len(request.Sources))
	for _, id := range request.Sources {
		path, err := t.pathNodes(ctx, tx, request.SpaceID, id)
		if err != nil {
			return nil, nil, 0, err
		}
		if len(path) < 2 {
			return nil, nil, 0, errHierarchy
		}
		covered := false
		for _, ancestor := range path[:len(path)-1] {
			covered = covered || selected[ancestor.ID]
		}
		if covered {
			continue
		}
		closure, node := q.NodeClosure, q.Node
		rows := closure.WithContext(ctx).Where(closure.SpaceID.Eq(request.SpaceID), closure.AncestorID.Eq(id))
		if request.Action == "copy" {
			rows = rows.Join(node, node.SpaceID.EqCol(closure.SpaceID), node.ID.EqCol(closure.DescendantID)).Where(node.State.Eq("active"))
		}
		size, err := rows.Count()
		if err != nil {
			return nil, nil, 0, err
		}
		if size < 1 || size > int64(budget.Nodes-count) {
			return nil, nil, 0, errHierarchy
		}
		count += int(size)
		roots = append(roots, id)
	}
	return roots, destination, count, nil
}

func replayOperationResult(ctx context.Context, tx *gorm.DB, operation *model.Operation, access operationAccess) (operationResult, error) {
	var result operationResult
	if err := access(ctx, tx, operation.SpaceID, operation.ActorID, operation.DestinationID, "read"); err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(operation.ResultJSON), &result); err != nil {
		return result, err
	}
	for _, rootID := range result.Roots {
		if err := access(ctx, tx, operation.SpaceID, operation.ActorID, rootID, "read"); err != nil {
			return operationResult{}, err
		}
	}
	return result, nil
}

func (e *operationExecution) selectedSourceTarget(id guid.ID) (bool, error) {
	if e.immediate == nil {
		item := e.q.OperationItem
		count, err := item.WithContext(e.ctx).Where(item.OperationID.Eq(e.operation.ID), item.NodeID.Eq(id)).Count()
		return count != 0, err
	}
	path, err := e.operationPath(id)
	if err != nil {
		return false, err
	}
	for _, root := range e.immediate.roots {
		if !e.immediate.adopted[root] {
			for _, ancestor := range path {
				if ancestor.ID == root {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (e *operationExecution) containsSelectedSource(id guid.ID) (bool, error) {
	closure := e.q.NodeClosure
	if e.immediate == nil {
		item := e.q.OperationItem
		count, err := item.WithContext(e.ctx).Join(closure, closure.SpaceID.EqCol(item.SpaceID), closure.DescendantID.EqCol(item.NodeID)).Where(item.OperationID.Eq(e.operation.ID), closure.AncestorID.Eq(id)).Count()
		return count != 0, err
	}
	if id < 0 {
		return false, nil
	}
	for start := 0; start < len(e.immediate.roots); start += 100 {
		ids := make([]driver.Valuer, 0, 100)
		for _, root := range e.immediate.roots[start:min(start+100, len(e.immediate.roots))] {
			if !e.immediate.adopted[root] {
				ids = append(ids, root)
			}
		}
		if len(ids) == 0 {
			continue
		}
		count, err := closure.WithContext(e.ctx).Where(closure.SpaceID.Eq(e.operation.SpaceID), closure.AncestorID.Eq(id), closure.DescendantID.In(ids...)).Count()
		if err != nil || count != 0 {
			return count != 0, err
		}
	}
	return false, nil
}
