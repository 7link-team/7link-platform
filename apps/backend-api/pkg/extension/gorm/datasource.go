package gormx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Datasource describes one physical database and the roles it may serve.
type Datasource struct {
	Type string
	DSN  string

	Writable bool
	Readable bool

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// Open connects to and verifies a datasource.
func (d Datasource) Open(ctx context.Context) (*gorm.DB, error) {
	return d.open(ctx, false)
}

func (d Datasource) open(ctx context.Context, debug bool) (*gorm.DB, error) {
	dialect, err := NormalizeDialect(d.Type)
	if err != nil {
		return nil, err
	}
	dsn := strings.TrimSpace(d.DSN)
	if dsn == "" {
		return nil, errors.New("gormx: datasource DSN is required")
	}

	var dialector gorm.Dialector
	switch dialect {
	case "sqlite":
		dialector = sqlite.Open(normalizeSQLiteDSN(dsn))
	case "mysql":
		dialector = mysql.Open(dsn)
	case "postgres":
		dialector = postgres.Open(dsn)
	}

	logLevel := logger.Error
	if debug {
		logLevel = logger.Info
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("gormx: open %s datasource: %w", dialect, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("gormx: get %s connection pool: %w", dialect, err)
	}
	d.applyPool(sqlDB)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("gormx: ping %s datasource: %w", dialect, err)
	}
	return db, nil
}

// NormalizeDialect returns the canonical GORM driver name.
func NormalizeDialect(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sqlite", "sqlite3":
		return "sqlite", nil
	case "mysql":
		return "mysql", nil
	case "pg", "pgsql", "postgres", "postgresql":
		return "postgres", nil
	default:
		return "", fmt.Errorf("gormx: unsupported datasource type %q", value)
	}
}

func normalizeSQLiteDSN(dsn string) string {
	return strings.TrimPrefix(dsn, "sqlite://")
}

func (d Datasource) applyPool(db interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
	SetConnMaxIdleTime(time.Duration)
}) {
	if d.MaxOpenConns > 0 {
		db.SetMaxOpenConns(d.MaxOpenConns)
	}
	if d.MaxIdleConns > 0 {
		db.SetMaxIdleConns(d.MaxIdleConns)
	}
	if d.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(d.ConnMaxLifetime)
	}
	if d.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(d.ConnMaxIdleTime)
	}
}

// DatasourceRouter routes writes to primary databases and reads to replicas.
// When no replica is configured, reads fall back to a primary.
type Writer interface {
	Write() *gorm.DB
}

type Reader interface {
	Read() *gorm.DB
}

type Router interface {
	Writer
	Reader
}

type DatasourceRouter struct {
	Writer []*gorm.DB
	Reader []*gorm.DB
}

var writeIndex atomic.Uint64
var readIndex atomic.Uint64

// NewDatasourceRouter opens every configured datasource. Healthy connections
// are retained even when another datasource fails; all open failures are joined
// in the returned error.
func NewDatasourceRouter(ctx context.Context, debug bool, datasources map[string]Datasource) (DatasourceRouter, error) {
	router := DatasourceRouter{
		Writer: make([]*gorm.DB, 0),
		Reader: make([]*gorm.DB, 0),
	}

	names := make([]string, 0, len(datasources))
	for name := range datasources {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		datasource := datasources[name]
		db, err := datasource.open(ctx, debug)
		if err != nil {
			errs = append(errs, fmt.Errorf("database %q: %w", name, err))
			continue
		}
		if datasource.Writable {
			router.Writer = append(router.Writer, db)
		}
		if datasource.Readable {
			router.Reader = append(router.Reader, db)
		}
		if !datasource.Writable && !datasource.Readable {
			sqlDB, closeErr := db.DB()
			if closeErr == nil {
				closeErr = sqlDB.Close()
			}
			if closeErr != nil {
				errs = append(errs, fmt.Errorf("database %q close unused datasource: %w", name, closeErr))
			}
		}
	}
	return router, errors.Join(errs...)
}

func (r *DatasourceRouter) Write() *gorm.DB {
	if r == nil || len(r.Writer) == 0 {
		return nil
	}
	index := writeIndex.Add(1) - 1
	return r.Writer[index%uint64(len(r.Writer))]
}

func (r *DatasourceRouter) Read() *gorm.DB {
	if r == nil {
		return nil
	}
	if len(r.Reader) == 0 {
		return r.Write()
	}
	index := readIndex.Add(1) - 1
	return r.Reader[index%uint64(len(r.Reader))]
}

// Close closes each underlying connection pool once, including datasources
// configured for both read and write roles.
func (r *DatasourceRouter) Close() error {
	if r == nil {
		return nil
	}
	seen := make(map[any]struct{}, len(r.Writer)+len(r.Reader))
	var errs []error
	for _, db := range append(append([]*gorm.DB(nil), r.Writer...), r.Reader...) {
		if db == nil {
			continue
		}
		sqlDB, err := db.DB()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, ok := seen[sqlDB]; ok {
			continue
		}
		seen[sqlDB] = struct{}{}
		if err := sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
