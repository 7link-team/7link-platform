package files

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"gorm.io/gorm"
)

var errVersionConflict = errors.New("files: stale or invalid file version")

// appendVersion is an internal committing primitive, not an authorization API.
// Its caller owns the entire operation transaction and rolls back on any error.
// It does no storage I/O, and locks the object before creating a content reference.
func (t directoryTree) appendVersion(ctx context.Context, tx *gorm.DB, input model.Version, expectedNodeVersion int64, now time.Time) error {
	if tx == nil || input.ID <= 0 || input.SpaceID <= 0 || input.NodeID <= 0 || input.ObjectID <= 0 || input.CreatedBy <= 0 || input.Number <= 0 || expectedNodeVersion <= 0 || len(input.Ext) > 255 || len(input.DeclaredMIME) > 128 || (input.SourceVersionID != nil && *input.SourceVersionID == input.ID) {
		return errVersionConflict
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrAccountTransaction
	}
	q := query.Bind(tx, t.prefix)
	result, err := q.Space.WithContext(ctx).Where(q.Space.ID.Eq(input.SpaceID)).UpdateSimple(q.Space.Version.Add(1))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	node := q.Node
	row, err := node.WithContext(ctx).Where(node.ID.Eq(input.NodeID), node.SpaceID.Eq(input.SpaceID), node.Kind.Eq("file"), node.State.Eq("active"), node.Version.Eq(expectedNodeVersion)).Take()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errVersionConflict
	}
	if err != nil {
		return err
	}
	var previous int64
	if row.CurrentVersionID != nil {
		version := q.Version
		current, err := version.WithContext(ctx).Where(version.ID.Eq(*row.CurrentVersionID), version.SpaceID.Eq(input.SpaceID), version.NodeID.Eq(input.NodeID)).Take()
		if err != nil {
			return err
		}
		previous = current.Number
	}
	if input.Number <= previous || input.Number-1 != previous {
		return errVersionConflict
	}
	object := q.Object
	result, err = object.WithContext(ctx).Where(object.ID.Eq(input.ObjectID), object.State.Eq("verified")).UpdateSimple(object.Version.Add(1))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	input.NodeKind, input.CreatedAt = "file", now.UnixMilli()
	if err := q.Version.WithContext(ctx).Create(&input); err != nil {
		return err
	}
	result, err = node.WithContext(ctx).Where(node.ID.Eq(input.NodeID), node.SpaceID.Eq(input.SpaceID), node.Version.Eq(expectedNodeVersion)).UpdateSimple(node.CurrentVersionID.Value(input.ID), node.Version.Add(1), node.UpdatedAt.Value(now.UnixMilli()))
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return errVersionConflict
	}
	return nil
}
