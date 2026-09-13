package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/governance/model"
	"7link.com/platform/internal/iam/governance/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
)

var errRepositoryUnavailable = errors.New("governance repository unavailable")

type Repository struct {
	runtime   *appenv.Runtime
	ids       guid.Source
	revisions *core.RevisionStore
	db        *gorm.DB
	inTx      bool
	// foreign holds the names of the tables this repository reads but does
	// not own. It is a pointer so a transactional clone reads the same
	// publication as the repository it came from.
	foreign *foreignTables
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source) *Repository {
	if runtime == nil || ids == nil {
		panic("governance repository requires runtime and id generator")
	}
	revisions, err := core.NewRevisionStore(runtime)
	if err != nil {
		panic(err)
	}
	return &Repository{runtime: runtime, ids: ids, revisions: revisions, foreign: &foreignTables{}}
}

func (r *Repository) writer() *gorm.DB {
	if r == nil {
		return nil
	}
	if r.db != nil {
		return r.db
	}
	if r.runtime == nil || r.runtime.Database() == nil {
		return nil
	}
	return r.runtime.Database().Write()
}

func (r *Repository) prefix() string {
	if r == nil || r.runtime == nil {
		return ""
	}
	return r.runtime.TablePrefix()
}

func (r *Repository) tables(db *gorm.DB) *query.Tables { return query.Bind(db, r.prefix()) }

func (r *Repository) withDB(db *gorm.DB, inTx bool) *Repository {
	clone := *r
	clone.db, clone.inTx = db, inTx
	return &clone
}

func (r *Repository) pinWriter() (*Repository, error) {
	if r == nil {
		return nil, errRepositoryUnavailable
	}
	if r.db != nil {
		return r, nil
	}
	db := r.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	return r.withDB(db, false), nil
}

func (r *Repository) runInTx(ctx context.Context, fn func(*Repository) error) error {
	if r == nil || fn == nil {
		return errors.New("invalid governance transaction")
	}
	if r.inTx {
		return fn(r)
	}
	pinned, err := r.pinWriter()
	if err != nil {
		return err
	}
	return pinned.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(pinned.withDB(tx, true))
	})
}

func (r *Repository) nextID() (guid.ID, error) {
	if r == nil || r.ids == nil {
		return 0, errRepositoryUnavailable
	}
	id, err := r.ids.Next()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("generated governance ID must be positive")
	}
	return guid.ID(id), nil
}

func (r *Repository) lockPolicy(ctx context.Context, tenantID guid.ID) error {
	if r == nil || r.revisions == nil || r.writer() == nil {
		return errRepositoryUnavailable
	}
	return r.revisions.Lock(ctx, r.writer(), tenantID)
}

func (r *Repository) bumpPolicy(ctx context.Context, tenantID guid.ID, at time.Time) error {
	if r == nil || r.revisions == nil || r.writer() == nil {
		return errRepositoryUnavailable
	}
	return r.revisions.Bump(ctx, r.writer(), tenantID, at)
}

func (r *Repository) getAccessRequest(ctx context.Context, tenantID, id guid.ID) (model.AccessRequest, error) {
	var row model.AccessRequest
	db := r.writer()
	if db == nil {
		return row, errRepositoryUnavailable
	}
	err := db.WithContext(ctx).Table(r.tables(db).AccessRequests.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get access request: %w", err)
	}
	return row, nil
}

func (r *Repository) getReview(ctx context.Context, tenantID, id guid.ID) (model.AccessReview, error) {
	var row model.AccessReview
	db := r.writer()
	if db == nil {
		return row, errRepositoryUnavailable
	}
	err := db.WithContext(ctx).Table(r.tables(db).AccessReviews.TableName()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrReviewNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get access review: %w", err)
	}
	return row, nil
}

func (r *Repository) getReviewItem(ctx context.Context, tenantID, reviewID, id guid.ID) (model.AccessReviewItem, error) {
	var row model.AccessReviewItem
	db := r.writer()
	if db == nil {
		return row, errRepositoryUnavailable
	}
	err := db.WithContext(ctx).Table(r.tables(db).AccessReviewItems.TableName()).
		Where("tenant_id = ? AND review_id = ? AND id = ?", tenantID, reviewID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrReviewItemNotFound
	}
	if err != nil {
		return row, fmt.Errorf("get access review item: %w", err)
	}
	return row, nil
}
