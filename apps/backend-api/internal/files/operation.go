package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errOperationInput = errors.New("files: invalid or unsupported operation request")
	errOperationKey   = errors.New("files: idempotency key belongs to another request")
	errOperationState = errors.New("files: operation is not ready or its plan expired")
)

type operationRequest struct {
	SpaceID         guid.ID   `json:"space_id"`
	Action          string    `json:"action"`
	DestinationID   guid.ID   `json:"destination_id"`
	Sources         []guid.ID `json:"sources"`
	FilePolicy      string    `json:"file_policy"`
	DirectoryPolicy string    `json:"directory_policy"`
	TypePolicy      string    `json:"type_policy"`
}

type operationBudget struct{ Nodes, ClosureRows int }

const operationCancelled = "cancelled" //nolint:misspell // Persisted migration state; preserve compatibility.

// operationAccess must use the caller's transaction-bound IAM evaluator, not a
// preparation-time grant or network call. No public file API exposes this engine.
type operationAccess func(context.Context, *gorm.DB, guid.ID, guid.ID, guid.ID, string) error

func operationCanonical(input operationRequest, key string, budget operationBudget) (operationRequest, string, string, error) {
	return canonicalOperationRequest(input, key, budget, false)
}

func canonicalOperationRequest(input operationRequest, key string, budget operationBudget, allowAsk bool) (operationRequest, string, string, error) {
	validPolicy := func(policy, kind string) bool {
		return (allowAsk && policy == "ask") || operationPolicyAllowed(kind, policy)
	}
	if (input.Action != "move" && input.Action != "copy") || input.SpaceID <= 0 || input.DestinationID <= 0 || len(key) == 0 || len(key) > 128 || budget.Nodes < 1 || budget.Nodes > (int(^uint(0)>>1)-maxNodeDepth-1)/2 || budget.ClosureRows < 1 || len(input.Sources) == 0 || len(input.Sources) > budget.Nodes || !validPolicy(input.FilePolicy, "file") || !validPolicy(input.DirectoryPolicy, "directory") || !validPolicy(input.TypePolicy, "type") {
		return input, "", "", errOperationInput
	}
	input.Sources = slices.Clone(input.Sources)
	slices.Sort(input.Sources)
	input.Sources = slices.Compact(input.Sources)
	if input.Sources[0] <= 0 {
		return input, "", "", errOperationInput
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return input, "", "", err
	}
	digest := sha256.Sum256(encoded)
	return input, string(encoded), hex.EncodeToString(digest[:]), nil
}

// prepareOperation saves bounded source pages without mutating the user's tree.
// A revision fence on every page and seal prevents publishing a mixed snapshot.
// An interrupted preparing record is durable and can be expired, never executed.
func (t directoryTree) prepareOperation(ctx context.Context, db *gorm.DB, id, actorID guid.ID, key string, input operationRequest, budget operationBudget, expiresAt, now time.Time, next func() (int64, error), access operationAccess) (*model.Operation, error) {
	if db == nil || id <= 0 || actorID <= 0 || !expiresAt.After(now) || next == nil || access == nil {
		return nil, errOperationInput
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return nil, ErrAccountTransaction
	}
	request, encoded, hash, err := operationCanonical(input, key, budget)
	if err != nil {
		return nil, err
	}
	var operation *model.Operation
	var roots []guid.ID
	existing := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		spaceID := request.SpaceID
		q, space, err := t.lockOperationSpace(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if err := access(ctx, tx, spaceID, actorID, request.DestinationID, "create"); err != nil {
			return err
		}
		operation, err = q.Operation.WithContext(ctx).Where(q.Operation.SpaceID.Eq(spaceID), q.Operation.ActorID.Eq(actorID), q.Operation.IdempotencyKey.Eq(key)).Take()
		if err == nil {
			existing = true
			if operation.RequestHash != hash {
				return errOperationKey
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		path, err := t.pathNodes(ctx, tx, spaceID, request.DestinationID)
		if err != nil {
			return err
		}
		destination := path[len(path)-1]
		if destination.Kind != "directory" {
			return errHierarchy
		}
		selected := make(map[guid.ID]bool, len(request.Sources))
		for _, sourceID := range request.Sources {
			selected[sourceID] = true
		}
		for _, ancestor := range path {
			if selected[ancestor.ID] {
				return errHierarchy
			}
		}
		for _, sourceID := range request.Sources {
			path, err := t.pathNodes(ctx, tx, spaceID, sourceID)
			if err != nil {
				return err
			}
			if len(path) < 2 {
				return errHierarchy
			}
			covered := false
			for _, ancestor := range path[:len(path)-1] {
				covered = covered || selected[ancestor.ID]
			}
			if !covered {
				roots = append(roots, sourceID)
			}
		}
		rootJSON, err := json.Marshal(roots)
		if err != nil {
			return err
		}
		operation = &model.Operation{ID: id, SpaceID: spaceID, ActorID: actorID, IdempotencyKey: key, RequestHash: hash, RequestJSON: encoded, RootsJSON: string(rootJSON), Action: request.Action, State: "preparing", Revision: 1, SpaceRevision: space.Version, DestinationID: destination.ID, DestinationVersion: destination.Version, NodeBudget: budget.Nodes, ClosureBudget: budget.ClosureRows, ResultJSON: "{}", CreatedAt: now.UnixMilli(), ExpiresAt: expiresAt.UnixMilli()}
		return q.Operation.WithContext(ctx).Create(operation)
	})
	if err != nil || existing {
		return operation, err
	}
	for _, rootID := range roots {
		var after guid.ID
		for {
			pageCount := 0
			err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				q, err := t.fencePlan(ctx, tx, operation, "preparing")
				if err != nil {
					return err
				}
				node, closure := q.Node, q.NodeClosure
				rowsQuery := node.WithContext(ctx).Join(closure, closure.SpaceID.EqCol(node.SpaceID), closure.DescendantID.EqCol(node.ID)).Where(closure.SpaceID.Eq(operation.SpaceID), closure.AncestorID.Eq(rootID), closure.DescendantID.Gt(after))
				if operation.Action == "copy" {
					rowsQuery = rowsQuery.Where(node.State.Eq("active"))
				}
				rows, err := rowsQuery.Order(closure.DescendantID.Asc()).Limit(min(100, budget.Nodes-operation.ItemCount+1)).Find()
				if err != nil {
					return err
				}
				if len(rows) > budget.Nodes-operation.ItemCount {
					return errHierarchy
				}
				items := make([]*model.OperationItem, 0, len(rows))
				for index, row := range rows {
					if row.Kind == "file" && row.State == "active" && row.CurrentVersionID == nil {
						return errVersionConflict
					}
					if err := access(ctx, tx, operation.SpaceID, actorID, row.ID, operation.Action); err != nil {
						return err
					}
					itemID, err := next()
					if err != nil || itemID <= 0 {
						return errors.Join(errOperationInput, err)
					}
					data, err := json.Marshal(row)
					if err != nil {
						return err
					}
					item := &model.OperationItem{ID: guid.ID(itemID), SpaceID: operation.SpaceID, OperationID: id, Ordinal: operation.ItemCount + index + 1, NodeID: row.ID, SnapshotJSON: string(data), PinnedNodeID: &row.ID, PinnedVersionID: row.CurrentVersionID}
					if row.CurrentVersionID != nil {
						version, err := q.Version.WithContext(ctx).Where(q.Version.SpaceID.Eq(operation.SpaceID), q.Version.NodeID.Eq(row.ID), q.Version.ID.Eq(*row.CurrentVersionID)).Take()
						if err != nil {
							return err
						}
						item.PinnedObjectID = &version.ObjectID
						result, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(version.ObjectID), q.Object.State.Eq("verified")).UpdateSimple(q.Object.Version.Add(1))
						if err != nil {
							return err
						}
						if result.RowsAffected != 1 {
							return ErrNotFound
						}
					}
					items = append(items, item)
				}
				if len(items) == 0 {
					return nil
				}
				if err := q.OperationItem.WithContext(ctx).Create(items...); err != nil {
					return err
				}
				if _, err := q.Operation.WithContext(ctx).Where(q.Operation.ID.Eq(id)).UpdateSimple(q.Operation.ItemCount.Add(len(items))); err != nil {
					return err
				}
				pageCount = len(items)
				after = rows[len(rows)-1].ID
				return nil
			})
			if err != nil {
				cleanupErr := db.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
					return t.finishOperation(context.WithoutCancel(ctx), tx, operation.ID, actorID, "failed", "plan_failed", now)
				})
				return operation, errors.Join(err, cleanupErr)
			}
			operation.ItemCount += pageCount
			if pageCount == 0 {
				break
			}
		}
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q, err := t.fencePlan(ctx, tx, operation, "preparing")
		if err != nil {
			return err
		}
		if operation.ItemCount < 1 {
			return errHierarchy
		}
		cost, err := t.preflightTargets(ctx, tx, operation, request, roots, access)
		if err != nil {
			return err
		}
		plan, err := json.Marshal(cost)
		if err != nil {
			return err
		}
		operation.PlanJSON = string(plan)
		_, err = q.Operation.WithContext(ctx).Where(q.Operation.ID.Eq(id)).UpdateSimple(q.Operation.State.Value("ready"), q.Operation.PlanJSON.Value(operation.PlanJSON))
		return err
	})
	if err != nil {
		cleanupErr := db.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
			return t.finishOperation(context.WithoutCancel(ctx), tx, id, actorID, "failed", "plan_failed", now)
		})
		return operation, errors.Join(err, cleanupErr)
	}
	operation.State = "ready"
	return operation, nil
}

func (t directoryTree) lockOperationSpace(ctx context.Context, tx *gorm.DB, spaceID guid.ID) (*query.Query, *model.Space, error) {
	q := query.Bind(tx, t.prefix)
	read := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(spaceID))
	if tx.Dialector.Name() == "sqlite" {
		// A deferred SQLite transaction must become a writer BEFORE its first
		// read. A no-op write locks, but changed-row counts do not prove existence.
		if _, err := read.UpdateSimple(q.Space.Version.Add(0)); err != nil {
			return nil, nil, err
		}
	} else {
		read = read.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
	}
	space, err := read.Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrNotFound
	}
	return q, space, err
}

func (t directoryTree) fencePlan(ctx context.Context, tx *gorm.DB, operation *model.Operation, state string) (*query.Query, error) {
	q, space, err := t.lockOperationSpace(ctx, tx, operation.SpaceID)
	if err != nil {
		return nil, err
	}
	if space.Version != operation.SpaceRevision {
		return nil, errTreeConflict
	}
	read := q.Operation.WithContext(ctx).Where(q.Operation.ID.Eq(operation.ID), q.Operation.State.Eq(state), q.Operation.Revision.Eq(operation.Revision))
	if tx.Dialector.Name() != "sqlite" {
		read = read.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
	}
	if _, err := read.Take(); errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errOperationState
	} else if err != nil {
		return nil, err
	}
	return q, nil
}

// finishOperation releases preparation pins on failure/cancel/expiry atomically.
// Success is recorded by executeOperation together with the actual tree mutation.
func (t directoryTree) finishOperation(ctx context.Context, tx *gorm.DB, id, actorID guid.ID, state, code string, now time.Time) error {
	if tx == nil || id <= 0 || actorID <= 0 || (state != "failed" && state != operationCancelled) || len(code) > 64 {
		return errOperationInput
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountTransaction
	}
	q := query.Bind(tx, t.prefix)
	operation := q.Operation
	condition := operation.WithContext(ctx).Where(operation.ID.Eq(id), operation.ActorID.Eq(actorID), operation.State.In("queued", "preparing", "awaiting_decision", "ready"))
	if code == "plan_expired" {
		condition = condition.Where(operation.ExpiresAt.Lte(now.UnixMilli()))
	}
	result, err := condition.UpdateSimple(operation.State.Value(state), operation.ErrorCode.Value(code), operation.CompletedAt.Value(now.UnixMilli()), operation.Revision.Add(1))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return errOperationState
	}
	return releaseOperationPins(ctx, q, id)
}

func releaseOperationPins(ctx context.Context, q *query.Query, id guid.ID) error {
	item := q.OperationItem
	_, err := item.WithContext(ctx).Where(item.OperationID.Eq(id)).UpdateSimple(item.PinnedNodeID.Null(), item.PinnedVersionID.Null(), item.PinnedObjectID.Null())
	return err
}

// expireOperations is a bounded reconciliation page. It cannot turn an expired
// or interrupted plan back into ready, and it never expires committed success.
func (t directoryTree) expireOperations(ctx context.Context, db *gorm.DB, now time.Time, after guid.ID, limit int) (guid.ID, error) {
	if db == nil || after < 0 || limit < 1 || limit > 200 {
		return after, errOperationInput
	}
	q := query.Bind(db, t.prefix)
	op := q.Operation
	rows, err := op.WithContext(ctx).Where(op.ID.Gt(after), op.ExpiresAt.Lte(now.UnixMilli()), op.State.In("queued", "preparing", "awaiting_decision", "ready")).Order(op.ID.Asc()).Limit(limit).Find()
	if err != nil {
		return after, err
	}
	for _, row := range rows {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return t.finishOperation(ctx, tx, row.ID, row.ActorID, "failed", "plan_expired", now)
		})
		if err != nil && !errors.Is(err, errOperationState) {
			return after, err
		}
		after = row.ID
	}
	if len(rows) < limit {
		return 0, nil
	}
	return after, nil
}
