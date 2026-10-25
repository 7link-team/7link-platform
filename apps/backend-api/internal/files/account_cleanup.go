package files

import (
	"context"
	"errors"
	"time"

	"7link.com/platform/internal/files/model"
	"7link.com/platform/internal/files/query"
	"7link.com/platform/internal/infra/guid"
	"gorm.io/gorm"
)

var errAccountReferenced = errors.New("files: account object still referenced")

// CollectAccountObjects expires temporary references and reclaims a bounded page
// of account-owned objects. Return zero at the end to restart the next sweep.
// An uncertain abandoned write stays a tombstone: retries cannot revive it.
func (a *AccountObjects) CollectAccountObjects(ctx context.Context, db *gorm.DB, now time.Time, after guid.ID, limit int) (guid.ID, error) {
	if db == nil || limit < 1 || limit > 1000 {
		return after, ErrInvalidKey
	}
	q := query.Bind(db, a.prefix)
	ref := q.AccountReference
	if _, err := ref.WithContext(ctx).Where(ref.State.Eq("active"), ref.ExpiresAt.Lte(now.UnixMilli())).Update(ref.State, "expired"); err != nil {
		return after, err
	}
	intent := q.AccountPreparation
	rows, err := intent.WithContext(ctx).Where(intent.BackendID.In(a.readableBackends()...), intent.State.In("writing", "ready", "attached", "releasing", "abandoned"), intent.ID.Gt(after)).Order(intent.ID.Asc()).Limit(limit).Find()
	if err != nil {
		return after, err
	}
	var errs []error
	for _, row := range rows {
		if err := a.collectAccountObject(ctx, db, row, now); err != nil {
			errs = append(errs, err)
		}
	}
	if len(rows) < limit {
		return 0, errors.Join(errs...)
	}
	return rows[len(rows)-1].ID, errors.Join(errs...)
}

func (a *AccountObjects) collectAccountObject(ctx context.Context, db *gorm.DB, intent *model.AccountPreparation, now time.Time) error {
	state := intent.State
	if (state == "writing" || state == "ready") && intent.ExpiresAt > now.UnixMilli() {
		return nil
	}
	if state != "releasing" && state != "abandoned" {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			q := query.Bind(tx, a.prefix)
			preparation := q.AccountPreparation
			next := "releasing"
			if state == "writing" {
				next = "abandoned"
			}
			condition := preparation.WithContext(ctx).Where(preparation.ID.Eq(intent.ID), preparation.State.Eq(state))
			if state == "writing" || state == "ready" {
				condition = condition.Where(preparation.ExpiresAt.Lte(now.UnixMilli()))
			}
			result, err := condition.Update(preparation.State, next)
			if err != nil {
				return err
			}
			if result.RowsAffected != 1 {
				return errAccountReferenced
			}
			if state != "writing" {
				object := q.Object
				result, err = object.WithContext(ctx).Where(object.ID.Eq(intent.ID), object.State.In("verified", "staged")).Update(object.State, "deleting")
				if err != nil {
					return err
				}
				if result.RowsAffected != 1 {
					return errAccountReferenced
				}
				ref := q.AccountReference
				count, err := ref.WithContext(ctx).Where(ref.ObjectID.Eq(intent.ID), ref.State.Eq("active")).Count()
				if err != nil {
					return err
				}
				if count != 0 {
					return errAccountReferenced
				}
				count, err = q.Version.WithContext(ctx).Where(q.Version.ObjectID.Eq(intent.ID)).Count()
				if err != nil {
					return err
				}
				if count != 0 {
					return errAccountReferenced
				}
				count, err = q.OperationItem.WithContext(ctx).Where(q.OperationItem.PinnedObjectID.Eq(intent.ID)).Count()
				if err != nil {
					return err
				}
				if count != 0 {
					return errAccountReferenced
				}
			}
			state = next
			return nil
		})
		if errors.Is(err, errAccountReferenced) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	// Network/storage I/O is outside the claiming database transaction. Both
	// registration and reads reject the claimed state before bytes are removed.
	remove := a.driver.Delete
	if reclaimer, ok := a.driver.(IncompleteWriteReclaimer); state == "abandoned" && ok {
		remove = reclaimer.ReclaimWrite
	}
	if err := remove(ctx, intent.StorageKey); err != nil {
		return err
	}
	if state == "abandoned" {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := query.Bind(tx, a.prefix)
		if _, err := q.Object.WithContext(ctx).Where(q.Object.ID.Eq(intent.ID), q.Object.State.Eq("deleting")).Update(q.Object.State, "deleted"); err != nil {
			return err
		}
		_, err := q.AccountPreparation.WithContext(ctx).Where(q.AccountPreparation.ID.Eq(intent.ID), q.AccountPreparation.State.Eq("releasing")).Update(q.AccountPreparation.State, "released")
		return err
	})
}
