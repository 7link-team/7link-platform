package gormx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSQLTraceLevelsAndExpectedFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		debug     bool
		threshold slog.Level
		err       error
		mode      logger.LogLevel
		expected  string
	}{
		{"dev normal", true, slog.LevelInfo, nil, logger.Error, ""},
		{"production normal", false, slog.LevelDebug, nil, logger.Error, ""},
		{"dev debug handler", true, slog.LevelDebug, nil, logger.Error, ""},
		{"explicit query debug", true, slog.LevelDebug, nil, logger.Info, `"level":"DEBUG"`},
		{"expected missing", true, slog.LevelDebug, gorm.ErrRecordNotFound, logger.Error, ""},
		{"contention", true, slog.LevelInfo, sqlite3.Error{Code: sqlite3.ErrBusy}, logger.Error, ""},
		{"contention debug", true, slog.LevelDebug, sqlite3.Error{Code: sqlite3.ErrLocked}, logger.Error, `"level":"DEBUG"`},
		{"real failure", false, slog.LevelInfo, errors.New("broken storage"), logger.Error, `"level":"ERROR"`},
		{"silent", true, slog.LevelDebug, errors.New("broken storage"), logger.Silent, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			log := newSQLLogger(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: test.threshold})), test.debug).LogMode(test.mode)
			calls := 0
			log.Trace(context.Background(), time.Now().Add(-time.Second), func() (string, int64) { calls++; return "SELECT 1", 1 }, test.err)
			if test.expected == "" {
				require.Empty(t, out.String())
				require.Zero(t, calls)
			} else {
				require.Contains(t, out.String(), test.expected)
				require.Equal(t, 1, calls)
			}
		})
	}
}
