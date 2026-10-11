package webhook

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"7link.com/platform/internal/iam/webhook/model"
	"7link.com/platform/internal/iam/webhook/query"
	"7link.com/platform/internal/infra/guid"
	"7link.com/platform/pkg/appenv"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type (
	subscriptionRow = model.Subscription
	deliveryRow     = model.Delivery
)

var errRepositoryUnavailable = errors.New("webhook storage unavailable")

// Repository resolves its datasource per operation rather than holding one, so a
// datasource that is replaced underneath it is picked up on the next statement.
type Repository struct {
	runtime *appenv.Runtime
	db      *gorm.DB
}

func NewRepository(runtime *appenv.Runtime) *Repository {
	return &Repository{runtime: runtime}
}

func (r *Repository) withDB(db *gorm.DB) *Repository {
	return &Repository{runtime: r.runtime, db: db}
}

func (r *Repository) writer() *gorm.DB {
	if r.db != nil {
		return r.db
	}
	if r.runtime == nil || r.runtime.Database() == nil {
		return nil
	}
	return r.runtime.Database().Write()
}

func (r *Repository) names() query.TableNames {
	prefix := ""
	if r.runtime != nil {
		prefix = r.runtime.TablePrefix()
	}
	return query.BindNames(prefix)
}

func (r *Repository) runInTx(ctx context.Context, fn func(*Repository) error) error {
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(r.withDB(tx))
	})
}

func (r *Repository) insertSubscription(ctx context.Context, row *subscriptionRow) error {
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	err := db.WithContext(ctx).Table(r.names().Subscriptions.String()).Create(row).Error
	if err != nil && isUniqueViolation(err) {
		return ErrSubscriptionNameTaken
	}
	return err
}

func (r *Repository) updateSubscription(ctx context.Context, row *subscriptionRow) error {
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	result := db.WithContext(ctx).Table(r.names().Subscriptions.String()).
		Where("tenant_id = ? AND id = ?", row.TenantID, row.ID).
		Select("name", "url", "secret_ciphertext", "event_types", "status", "updated_at").Updates(row)
	if result.Error != nil {
		if isUniqueViolation(result.Error) {
			return ErrSubscriptionNameTaken
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

func (r *Repository) getSubscription(ctx context.Context, tenantID, id guid.ID) (subscriptionRow, error) {
	db := r.writer()
	if db == nil {
		return subscriptionRow{}, errRepositoryUnavailable
	}
	var row subscriptionRow
	err := db.WithContext(ctx).Table(r.names().Subscriptions.String()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return subscriptionRow{}, ErrSubscriptionNotFound
	}
	return row, err
}

func (r *Repository) listSubscriptions(ctx context.Context, tenantID guid.ID, offset, limit int) ([]subscriptionRow, int64, error) {
	db := r.writer()
	if db == nil {
		return nil, 0, errRepositoryUnavailable
	}
	table := r.names().Subscriptions.String()
	var total int64
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count webhook subscriptions: %w", err)
	}
	rows := []subscriptionRow{}
	if err := db.WithContext(ctx).Table(table).Where("tenant_id = ?", tenantID).
		Order("name, id").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list webhook subscriptions: %w", err)
	}
	return rows, total, nil
}

func (r *Repository) deleteSubscription(ctx context.Context, tenantID, id guid.ID) error {
	db := r.writer()
	if db == nil {
		return errRepositoryUnavailable
	}
	result := db.WithContext(ctx).Table(r.names().Subscriptions.String()).
		Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&subscriptionRow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// activeSubscriptions lists up to limit active subscriptions ordered after the
// key (afterTenant, afterID), so consecutive passes walk the whole set rather
// than catching up the same front of it every time.
func (r *Repository) activeSubscriptions(ctx context.Context, afterTenant, afterID guid.ID, limit int) ([]subscriptionRow, error) {
	db := r.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	rows := []subscriptionRow{}
	err := db.WithContext(ctx).Table(r.names().Subscriptions.String()).
		Where("status = ?", SubscriptionActive).
		Where("tenant_id > ? OR (tenant_id = ? AND id > ?)", afterTenant, afterTenant, afterID).
		Order("tenant_id, id").Limit(limit).Find(&rows).Error
	return rows, err
}

// enqueue records the work owed for one event and advances the cursor in the same
// transaction. Advancing it anywhere else would let a crash between the two
// either lose an event or offer it twice.
func (r *Repository) enqueue(ctx context.Context, subscription subscriptionRow, rows []deliveryRow, throughSequence int64, now time.Time) error {
	return r.runInTx(ctx, func(tx *Repository) error {
		db := tx.writer()
		if len(rows) > 0 {
			// A pass that already ran inserted some of these; the uniqueness over
			// (subscription, event) is what makes repeating it harmless.
			if err := db.WithContext(ctx).Table(tx.names().Deliveries.String()).
				Clauses(onConflictDoNothing()).Create(&rows).Error; err != nil {
				return fmt.Errorf("enqueue webhook deliveries: %w", err)
			}
		}
		result := db.WithContext(ctx).Table(tx.names().Subscriptions.String()).
			Where("tenant_id = ? AND id = ? AND after_sequence = ?", subscription.TenantID, subscription.ID, subscription.AfterSequence).
			Updates(map[string]any{"after_sequence": throughSequence, "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("advance webhook cursor: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			// Another replica moved the cursor first. Its work is the same work,
			// so yielding is correct rather than an error.
			return nil
		}
		return nil
	})
}

// claimDelivery takes a lease on one pending delivery. The attempt count is part
// of the predicate so two replicas cannot both claim the same row, and the count
// it leaves behind is what fences the claimant's outcome write.
func (r *Repository) claimDelivery(ctx context.Context, id guid.ID, attempts int, now time.Time) (bool, error) {
	db := r.writer()
	if db == nil {
		return false, errRepositoryUnavailable
	}
	result := db.WithContext(ctx).Table(r.names().Deliveries.String()).
		Where("id = ? AND status = ? AND attempts = ?", id, DeliveryPending, attempts).
		Updates(map[string]any{
			"status": DeliveryDelivering, "attempts": attempts + 1,
			"locked_at": now, "updated_at": now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}

// resetStaleDeliveries returns claims whose lease began before staleBefore to
// pending. A claimant that crashed, was killed, or overran its lease never writes
// an outcome, so without this its row would stay claimed forever. The attempt is
// kept: it was made, and the outcome fence relies on the count only growing.
func (r *Repository) resetStaleDeliveries(ctx context.Context, staleBefore, now time.Time) (int64, error) {
	db := r.writer()
	if db == nil {
		return 0, errRepositoryUnavailable
	}
	result := db.WithContext(ctx).Table(r.names().Deliveries.String()).
		Where("status = ? AND (locked_at IS NULL OR locked_at < ?)", DeliveryDelivering, staleBefore).
		Updates(map[string]any{"status": DeliveryPending, "locked_at": nil, "updated_at": now})
	return result.RowsAffected, result.Error
}

func (r *Repository) dueDeliveries(ctx context.Context, now time.Time, limit int) ([]deliveryRow, error) {
	db := r.writer()
	if db == nil {
		return nil, errRepositoryUnavailable
	}
	rows := []deliveryRow{}
	err := db.WithContext(ctx).Table(r.names().Deliveries.String()).
		Where("status = ? AND available_at <= ?", DeliveryPending, now).
		Order("available_at, id").Limit(limit).Find(&rows).Error
	return rows, err
}

// deliveryBacklog counts what is owed now: deliveries that are due, and claims
// that have not yet recorded an outcome.
func (r *Repository) deliveryBacklog(ctx context.Context, now time.Time) (int64, error) {
	db := r.writer()
	if db == nil {
		return 0, errRepositoryUnavailable
	}
	var count int64
	err := db.WithContext(ctx).Table(r.names().Deliveries.String()).
		Where("(status = ? AND available_at <= ?) OR status = ?", DeliveryPending, now, DeliveryDelivering).
		Count(&count).Error
	return count, err
}

// markDelivered records a success for the claim that left attempts on the row.
// It reports false when that claim is no longer in force -- the lease was
// reclaimed and possibly claimed again -- and then writes nothing, so a late
// outcome cannot overwrite the claim that replaced it.
func (r *Repository) markDelivered(ctx context.Context, id guid.ID, attempts int, now time.Time) (bool, error) {
	db := r.writer()
	if db == nil {
		return false, errRepositoryUnavailable
	}
	result := db.WithContext(ctx).Table(r.names().Deliveries.String()).
		Where("id = ? AND status = ? AND attempts = ?", id, DeliveryDelivering, attempts).
		Updates(map[string]any{
			"status": DeliveryDelivered, "delivered_at": now, "locked_at": nil,
			"last_error": "", "updated_at": now,
		})
	return result.RowsAffected == 1, result.Error
}

// markAttemptFailed records a failed attempt under the same fence as
// markDelivered, and reports false when the claim was superseded.
func (r *Repository) markAttemptFailed(ctx context.Context, id guid.ID, attempts int, status, reason string, retryAt, now time.Time) (bool, error) {
	db := r.writer()
	if db == nil {
		return false, errRepositoryUnavailable
	}
	if len(reason) > 512 {
		reason = reason[:512]
	}
	result := db.WithContext(ctx).Table(r.names().Deliveries.String()).
		Where("id = ? AND status = ? AND attempts = ?", id, DeliveryDelivering, attempts).
		Updates(map[string]any{
			"status": status, "available_at": retryAt, "locked_at": nil,
			"last_error": reason, "updated_at": now,
		})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) listDeliveries(ctx context.Context, tenantID, subscriptionID guid.ID, offset, limit int) ([]deliveryRow, int64, error) {
	db := r.writer()
	if db == nil {
		return nil, 0, errRepositoryUnavailable
	}
	table := r.names().Deliveries.String()
	where := func(statement *gorm.DB) *gorm.DB {
		return statement.Where("tenant_id = ? AND subscription_id = ?", tenantID, subscriptionID)
	}
	var total int64
	if err := where(db.WithContext(ctx).Table(table)).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count webhook deliveries: %w", err)
	}
	rows := []deliveryRow{}
	if err := where(db.WithContext(ctx).Table(table)).
		Order("event_sequence DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list webhook deliveries: %w", err)
	}
	return rows, total, nil
}

// onConflictDoNothing makes re-inserting a delivery a no-op rather than an
// error, which is what lets a fan-out pass that was interrupted simply run again.
func onConflictDoNothing() clause.OnConflict {
	return clause.OnConflict{Columns: []clause.Column{{Name: "subscription_id"}, {Name: "event_id"}}, DoNothing: true}
}

// isUniqueViolation recognizes a duplicate across the three dialects without
// naming a driver: every one of them says so in the message, and the alternative
// is importing three driver packages into this module.
func isUniqueViolation(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}

// subscriptionOwner resolves which tenant owns a subscription, which is what the
// authorization layer needs before it will admit a request that names one by id.
func (r *Repository) subscriptionOwner(ctx context.Context, id guid.ID) (guid.ID, error) {
	db := r.writer()
	if db == nil {
		return 0, errRepositoryUnavailable
	}
	var row subscriptionRow
	err := db.WithContext(ctx).Table(r.names().Subscriptions.String()).
		Select("tenant_id").Where("id = ?", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrSubscriptionNotFound
	}
	return row.TenantID, err
}

// nameTaken reports whether another subscription in the tenant already answers to
// a name. The unique constraint is the race-safe backstop; asking first is what
// turns a driver message into the error an operator can act on.
func (r *Repository) nameTaken(ctx context.Context, tenantID guid.ID, name string, excluding guid.ID) (bool, error) {
	db := r.writer()
	if db == nil {
		return false, errRepositoryUnavailable
	}
	statement := db.WithContext(ctx).Table(r.names().Subscriptions.String()).
		Where("tenant_id = ? AND name = ?", tenantID, name)
	if !excluding.Zero() {
		statement = statement.Where("id <> ?", excluding)
	}
	var count int64
	if err := statement.Count(&count).Error; err != nil {
		return false, fmt.Errorf("check webhook subscription name: %w", err)
	}
	return count > 0, nil
}
