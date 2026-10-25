package gormx

import (
	"context"
	"database/sql"
	"sync"

	"gorm.io/gorm"
)

// sqlitePool admits one writer at a time before obtaining a SQL connection.
// Waiting writers therefore neither race SQLite's busy handler nor occupy the
// connections needed by WAL readers. Keep this at the pool boundary so every
// module's transactions and standalone Exec statements share the same queue.
type sqlitePool struct {
	*sql.DB
	writer chan struct{}
}

func (p *sqlitePool) GetDBConn() (*sql.DB, error) { return p.DB, nil }

func (p *sqlitePool) acquire(ctx context.Context) error {
	select {
	case p.writer <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-p.writer
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *sqlitePool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	// Own cancellation rollback below. Using the caller context here would race
	// database/sql's automatic rollback: ErrTxDone can precede the driver's
	// actual rollback, releasing admission before the write lock is released.
	tx, err := p.DB.BeginTx(context.WithoutCancel(ctx), opts)
	if err != nil {
		<-p.writer
		return nil, err
	}
	wrapped := &sqliteTx{Tx: tx, release: func() { <-p.writer }}
	// Roll back and release admission even when a caller forgets its final
	// Rollback after cancellation.
	wrapped.stop = context.AfterFunc(ctx, func() {
		_ = wrapped.finish(false)
	})
	if err := ctx.Err(); err != nil {
		_ = wrapped.Rollback()
		return nil, err
	}
	return wrapped, nil
}

func (p *sqlitePool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer func() { <-p.writer }()
	return p.DB.ExecContext(ctx, query, args...)
}

type sqliteTx struct {
	*sql.Tx
	mu      sync.Mutex
	done    bool
	release func()
	stop    func() bool
}

// SQLTx exposes identity for transaction-scoped bookkeeping without bypassing
// this wrapper's commit/rollback lifecycle.
func (tx *sqliteTx) SQLTx() *sql.Tx { return tx.Tx }

func (tx *sqliteTx) Commit() error {
	tx.stop()
	return tx.finish(true)
}

func (tx *sqliteTx) Rollback() error {
	tx.stop()
	return tx.finish(false)
}

func (tx *sqliteTx) finish(commit bool) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.done {
		return sql.ErrTxDone
	}
	var err error
	if commit {
		err = tx.Tx.Commit()
		if err != nil {
			_ = tx.Tx.Rollback()
		}
	} else {
		err = tx.Tx.Rollback()
	}
	tx.done = true
	tx.release()
	return err
}
