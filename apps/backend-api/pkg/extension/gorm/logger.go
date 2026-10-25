package gormx

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type sqlLogger struct {
	logger.Interface
	log   *slog.Logger
	level logger.LogLevel
}

func newSQLLogger(log *slog.Logger, _ bool) logger.Interface {
	return &sqlLogger{Interface: logger.NewSlogLogger(log, logger.Config{LogLevel: logger.Error, IgnoreRecordNotFoundError: true, ParameterizedQueries: true}), log: log, level: logger.Error}
}

func (l *sqlLogger) LogMode(level logger.LogLevel) logger.Interface {
	snapshot := *l
	snapshot.level = level
	snapshot.Interface = l.Interface.LogMode(level)
	return &snapshot
}

func (*sqlLogger) ParamsFilter(_ context.Context, query string, _ ...any) (string, []any) {
	return query, nil
}

func (l *sqlLogger) Trace(ctx context.Context, start time.Time, query func() (string, int64), err error) {
	if l.level == logger.Silent || errors.Is(err, gorm.ErrRecordNotFound) {
		return
	}
	level := slog.LevelDebug
	if err != nil && !IsSQLiteBusy(err) {
		level = slog.LevelError
	} else if err == nil && l.level < logger.Info {
		return
	}
	if !l.log.Enabled(ctx, level) {
		return
	}
	sql, rows := query()
	l.log.Log(ctx, level, "SQL executed", "duration", time.Since(start), "sql", sql, "rows", rows, "error", err)
}
