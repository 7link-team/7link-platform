package query

import (
	"7link.com/platform/internal/infra/events/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"
	"log/slog"
	"time"
)

type Events = eventRow
type TableNames struct{ Events, Cursors, Receipts, Parked gormx.TableName }

func BindNames(prefix string) TableNames {
	return TableNames{Events: gormx.NewTableName(prefix, model.EventRow{}.TableName()), Cursors: gormx.NewTableName(prefix, model.Cursor{}.TableName()), Receipts: gormx.NewTableName(prefix, model.Receipt{}.TableName()), Parked: gormx.NewTableName(prefix, model.Parked{}.TableName())}
}

func BindCursors(db *gorm.DB, names TableNames) *cursor {
	return Use(db).Cursor.Table(names.Cursors.String())
}
func BindWithNames(db *gorm.DB, names TableNames) *Events {
	return Use(db).EventRow.Table(names.Events.String())
}

// Notify wakes a PostgreSQL sequencer only after the caller commits. Other
// engines use polling. The channel uses the same resolved deployment name.
func Notify(ctx context.Context, tx *gorm.DB, names TableNames) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.WithContext(ctx).Exec("SELECT pg_notify(?, '')", names.Events.String()).Error
}

func NotifySequenced(ctx context.Context, tx *gorm.DB, names TableNames) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	return tx.WithContext(ctx).Exec("SELECT pg_notify(?, '')", names.Events.String()+"_seq").Error
}

// Listen uses a dedicated PostgreSQL connection, so waiting never reserves a
// writer-pool slot. Notification loss only affects latency; polling still tails
// the durable stream. Connection credentials remain inside the driver config.
func Listen(ctx context.Context, db *gorm.DB, names TableNames, sequenced bool) (<-chan struct{}, error) {
	if db.Dialector.Name() != "postgres" {
		return nil, nil
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	raw, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var config *pgx.ConnConfig
	err = raw.Raw(func(driver any) error {
		conn, ok := driver.(*stdlib.Conn)
		if !ok {
			return errors.New("events: PostgreSQL notification driver unavailable")
		}
		config = conn.Conn().Config()
		return nil
	})
	closeErr := raw.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	channel := names.Events.String()
	if sequenced {
		channel += "_seq"
	}
	signals := make(chan struct{}, 1)
	go func() {
		defer close(signals)
		for ctx.Err() == nil {
			conn, err := pgx.ConnectConfig(ctx, config)
			if err == nil {
				_, err = conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize())
				for err == nil {
					_, err = conn.WaitForNotification(ctx)
					if err == nil {
						select {
						case signals <- struct{}{}:
						default:
						}
					}
				}
				closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = conn.Close(closeCtx)
				cancel()
			}
			if ctx.Err() != nil {
				return
			}
			slog.WarnContext(ctx, "domain event notification listener reconnecting", "error", err)
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return signals, nil
}

func BindReceipts(db *gorm.DB, names TableNames) *receipt {
	return Use(db).Receipt.Table(names.Receipts.String())
}
func BindParked(db *gorm.DB, names TableNames) *parked {
	return Use(db).Parked.Table(names.Parked.String())
}
