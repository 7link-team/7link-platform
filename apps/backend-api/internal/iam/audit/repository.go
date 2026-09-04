package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"7link.com/platform/internal/iam/audit/model"
	"7link.com/platform/internal/iam/audit/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errRepositoryUnavailable = errors.New("audit repository unavailable")

type Repository struct {
	runtime *appenv.Runtime
	ids     guid.Source
	db      *gorm.DB
	inTx    bool
}

func NewRepository(runtime *appenv.Runtime, ids guid.Source) *Repository {
	if runtime == nil || ids == nil {
		panic("audit repository requires runtime and id generator")
	}
	return &Repository{runtime: runtime, ids: ids}
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
		return errors.New("invalid audit transaction")
	}
	if r.inTx {
		return fn(r)
	}
	pinned, err := r.pinWriter()
	if err != nil {
		return err
	}
	return pinned.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(pinned.withDB(tx, true)) })
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
		return 0, errors.New("generated audit ID must be positive")
	}
	return guid.ID(id), nil
}

func (r *Repository) appendTo(ctx context.Context, db *gorm.DB, input EventInput, detail string, now time.Time) (model.Event, error) {
	if r == nil || db == nil {
		return model.Event{}, errRepositoryUnavailable
	}
	id, err := r.nextID()
	if err != nil {
		return model.Event{}, fmt.Errorf("generate audit id: %w", err)
	}
	tables := r.tables(db)
	head := model.Head{TenantID: input.TenantID, UpdatedAt: now}
	if err := db.WithContext(ctx).Table(tables.Heads.TableName()).Clauses(clause.OnConflict{DoNothing: true}).Create(&head).Error; err != nil {
		return model.Event{}, fmt.Errorf("ensure audit head: %w", err)
	}
	read := db.WithContext(ctx).Table(tables.Heads.TableName()).Where("tenant_id = ?", input.TenantID)
	if db.Dialector.Name() != "sqlite" {
		read = read.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate})
	}
	if err := read.Take(&head).Error; err != nil {
		return model.Event{}, fmt.Errorf("lock audit head: %w", err)
	}
	head.EventHash = normalizeStoredHash(head.EventHash)
	row := model.Event{
		ID: id, TenantID: input.TenantID, PrincipalID: idPointer(input.PrincipalID),
		EventType: input.EventType, TargetType: input.TargetType, TargetID: idPointer(input.TargetID),
		Outcome: input.Outcome, IPAddress: input.IPAddress, UserAgent: input.UserAgent, Detail: detail,
		CreatedAt: now, Sequence: head.Sequence + 1, PreviousHash: head.EventHash,
	}
	row.EventHash = hash(row)
	if err := db.WithContext(ctx).Table(tables.Events.TableName()).Create(&row).Error; err != nil {
		return model.Event{}, fmt.Errorf("append audit event: %w", err)
	}
	result := db.WithContext(ctx).Table(tables.Heads.TableName()).
		Where("tenant_id = ? AND sequence = ?", row.TenantID, head.Sequence).
		Updates(map[string]any{"sequence": row.Sequence, "event_hash": row.EventHash, "updated_at": row.CreatedAt})
	if result.Error != nil {
		return model.Event{}, fmt.Errorf("advance audit head: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return model.Event{}, errors.New("audit head changed concurrently")
	}
	return row, nil
}

func (r *Repository) list(ctx context.Context, filter Filter) ([]model.Event, int64, error) {
	db := r.writer()
	if db == nil {
		return nil, 0, errRepositoryUnavailable
	}
	table := r.tables(db).Events.TableName()
	base := applyFilter(db.WithContext(ctx).Table(table).Where("tenant_id = ?", filter.TenantID), filter)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit events: %w", err)
	}
	var rows []model.Event
	if err := applyFilter(db.WithContext(ctx).Table(table).Where("tenant_id = ?", filter.TenantID), filter).
		Order("created_at DESC, id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list audit events: %w", err)
	}
	return rows, total, nil
}

func (r *Repository) get(ctx context.Context, tenantID, id guid.ID) (model.Event, error) {
	db := r.writer()
	if db == nil {
		return model.Event{}, errRepositoryUnavailable
	}
	var row model.Event
	err := db.WithContext(ctx).Table(r.tables(db).Events.TableName()).Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if err != nil {
		return model.Event{}, err
	}
	return row, nil
}

func (r *Repository) readHead(ctx context.Context, tenantID guid.ID) (model.Head, bool, error) {
	db := r.writer()
	if db == nil {
		return model.Head{}, false, errRepositoryUnavailable
	}
	var head model.Head
	err := db.WithContext(ctx).Table(r.tables(db).Heads.TableName()).Where("tenant_id = ?", tenantID).Take(&head).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Head{}, false, nil
	}
	if err != nil {
		return model.Head{}, false, fmt.Errorf("read audit head: %w", err)
	}
	return head, true, nil
}

func (r *Repository) readPrefix(ctx context.Context, tenantID guid.ID, maxSequence int64, offset, limit int) ([]model.Event, error) {
	db := r.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	var rows []model.Event
	err := db.WithContext(ctx).Table(r.tables(db).Events.TableName()).
		Where("tenant_id = ? AND sequence > 0 AND sequence <= ?", tenantID, maxSequence).
		Order("sequence ASC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *Repository) readExport(ctx context.Context, filter Filter, cursor, maxSequence int64, limit int) ([]model.Event, error) {
	db := r.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	var rows []model.Event
	query := db.WithContext(ctx).Table(r.tables(db).Events.TableName()).
		Where("tenant_id = ? AND sequence > ? AND sequence <= ?", filter.TenantID, cursor, maxSequence)
	if err := applyFilter(query, filter).Order("sequence ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read audit export: %w", err)
	}
	return rows, nil
}

func applyFilter(db *gorm.DB, filter Filter) *gorm.DB {
	if !filter.PrincipalID.Zero() {
		db = db.Where("principal_id = ?", filter.PrincipalID)
	}
	if filter.EventType != "" {
		db = db.Where("event_type = ?", filter.EventType)
	}
	if filter.Outcome != "" {
		db = db.Where("outcome = ?", filter.Outcome)
	}
	if filter.TargetType != "" {
		db = db.Where("target_type = ?", filter.TargetType)
	}
	if !filter.TargetID.Zero() {
		db = db.Where("target_id = ?", filter.TargetID)
	}
	if !filter.From.IsZero() {
		db = db.Where("created_at >= ?", filter.From.UTC())
	}
	if !filter.To.IsZero() {
		db = db.Where("created_at < ?", filter.To.UTC())
	}
	return db
}
