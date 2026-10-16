package dlock

import (
	"context"
	"unicode/utf8"

	"7link.com/platform/internal/infra/dlock/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) transactionGuard(ctx context.Context, tx *gorm.DB, name string) error {
	if tx == nil || tx.Statement == nil || name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 179 {
		return ErrInvalidTransaction
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return ErrInvalidTransaction
	}
	locker := m.Locker()
	if locker == nil {
		return ErrDatabaseUnavailable
	}
	locker.db = tx
	locks := locker.locks()
	// Separate persistent transaction mutexes from ordinary renewable lease names.
	key := "transaction:" + name
	row := model.LockRow{Name: key, Owner: "", ExpiresAt: 0}
	if err := locks.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row); err != nil {
		return err
	}
	// A write (including an unchanged value) holds the row until commit on all
	// engines. SQLite already owns the writer lock; no unsupported FOR UPDATE.
	_, err := locks.WithContext(ctx).Where(locks.Name.Eq(key)).UpdateSimple(locks.ExpiresAt.Value(0))
	return err
}
