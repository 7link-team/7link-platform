package gormx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
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
	// Timezone configures driver decoding and every new database connection.
	// Empty means UTC.
	Timezone string

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
	location, err := loadLocation(d.Timezone)
	if err != nil {
		return nil, fmt.Errorf("gormx: invalid datasource timezone %q: %w", d.Timezone, err)
	}

	var (
		dialector gorm.Dialector
		ownedPool *sql.DB
	)
	switch dialect {
	case "sqlite":
		dsn, err = normalizeSQLiteDSN(dsn)
		if err != nil {
			return nil, fmt.Errorf("gormx: configure sqlite datasource: %w", err)
		}
		dialector = sqlite.Open(dsn)
	case "mysql":
		dsn, err = mysqlDSNWithTimezone(dsn, location)
		if err != nil {
			return nil, fmt.Errorf("gormx: configure mysql timezone: %w", err)
		}
		dialector = mysql.Open(dsn)
	case "postgres":
		config, configErr := postgresConfigWithTimezone(dsn, location)
		if configErr != nil {
			return nil, fmt.Errorf("gormx: configure postgres timezone: %w", configErr)
		}
		ownedPool = stdlib.OpenDB(*config, stdlib.OptionAfterConnect(func(_ context.Context, conn *pgx.Conn) error {
			conn.TypeMap().RegisterType(&pgtype.Type{
				Name: "timestamp", OID: pgtype.TimestampOID,
				Codec: &pgtype.TimestampCodec{ScanLocation: location},
			})
			conn.TypeMap().RegisterType(&pgtype.Type{
				Name: "timestamptz", OID: pgtype.TimestamptzOID,
				Codec: &pgtype.TimestamptzCodec{ScanLocation: location},
			})
			return nil
		}))
		dialector = postgres.New(postgres.Config{Conn: ownedPool})
	}

	logLevel := logger.Error
	if debug {
		logLevel = logger.Info
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		DisableAutomaticPing: true,
		Logger: logger.NewSlogLogger(slog.Default(), logger.Config{
			SlowThreshold: 200 * time.Millisecond,
			LogLevel:      logLevel,
		}),
	})
	if err != nil {
		if ownedPool != nil {
			_ = ownedPool.Close()
		}
		return nil, fmt.Errorf("gormx: open %s datasource: %w", dialect, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		if ownedPool != nil {
			_ = ownedPool.Close()
		}
		return nil, fmt.Errorf("gormx: get %s connection pool: %w", dialect, err)
	}
	d.applyPool(sqlDB)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("gormx: ping %s datasource: %w", dialect, err)
	}
	return db, nil
}

func loadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "UTC"
	}
	return time.LoadLocation(name)
}

func mysqlDSNWithTimezone(dsn string, location *time.Location) (string, error) {
	config, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		return "", err
	}
	config.Loc = location
	config.ParseTime = true
	if config.Params == nil {
		config.Params = make(map[string]string)
	}
	timezone := location.String()
	if timezone == "UTC" {
		timezone = "+00:00"
	}
	config.Params["time_zone"] = "'" + timezone + "'"
	return config.FormatDSN(), nil
}

func postgresConfigWithTimezone(dsn string, location *time.Location) (*pgx.ConnConfig, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.RuntimeParams["timezone"] = location.String()
	return config, nil
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

func normalizeSQLiteDSN(dsn string) (string, error) {
	dsn = strings.TrimPrefix(dsn, "sqlite://")
	filename, rawQuery, _ := strings.Cut(dsn, "?")
	params, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("parse DSN parameters: %w", err)
	}

	foreignKeyConfigured := false
	for _, key := range []string{"_foreign_keys", "_fk"} {
		values, ok := params[key]
		if !ok {
			continue
		}
		foreignKeyConfigured = true
		for _, value := range values {
			switch strings.ToLower(value) {
			case "1", "yes", "true", "on":
			case "0", "no", "false", "off":
				return "", fmt.Errorf("foreign keys cannot be disabled with %s=%q", key, value)
			default:
				return "", fmt.Errorf("invalid %s value %q: expected 1, yes, true, or on", key, value)
			}
		}
	}
	if !foreignKeyConfigured {
		params.Set("_foreign_keys", "on")
	}
	return filename + "?" + params.Encode(), nil
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
	nodes      []*datasourceNode
	writers    []*gorm.DB
	readers    []*gorm.DB
	writeIndex atomic.Uint64
	readIndex  atomic.Uint64
}

type datasourceNode struct {
	name     string
	writable bool
	readable bool
	db       *gorm.DB
	// startupErr is permanent for this router instance; a node that never
	// opened cannot enter a routing set until the process rebuilds the router.
	startupErr error
	up         atomic.Bool
}

// DatasourceHealth is the result of probing one configured datasource.
type DatasourceHealth struct {
	Name     string
	Duration time.Duration
	Err      error
}

// HealthSnapshot contains one internally consistent datasource probe result.
// RoutingError only considers nodes that are currently part of a routing set;
// configured nodes that failed during startup remain visible by name without
// making a usable routing set unhealthy.
type HealthSnapshot struct {
	Datasources []DatasourceHealth
	CheckedAt   time.Time
	Duration    time.Duration
	routingErr  error
}

func (snapshot HealthSnapshot) RoutingError() error { return snapshot.routingErr }

// DatasourceError returns the probe error for one named datasource.
func (snapshot HealthSnapshot) DatasourceError(name string) error {
	for _, datasource := range snapshot.Datasources {
		if datasource.Name == name {
			return datasource.Err
		}
	}
	return fmt.Errorf("gormx: datasource %q is not configured", name)
}

// DatasourceStats is a point-in-time status and connection-pool snapshot for
// one configured datasource.
type DatasourceStats struct {
	Name     string
	Writable bool
	Readable bool
	Up       bool
	Pool     sql.DBStats
}

// NewDatasourceRouter opens every configured datasource. Healthy connections
// are retained even when another datasource fails; all open failures are joined
// in the returned error.
func NewDatasourceRouter(ctx context.Context, debug bool, datasources map[string]Datasource) (*DatasourceRouter, error) {
	names := make([]string, 0, len(datasources))
	for name := range datasources {
		names = append(names, name)
	}
	sort.Strings(names)

	var configErrs []error
	for _, name := range names {
		datasource := datasources[name]
		if !datasource.Writable && !datasource.Readable {
			configErrs = append(configErrs, fmt.Errorf("gormx: datasource %q must be readable or writable", name))
		}
	}
	if len(configErrs) > 0 {
		return nil, errors.Join(configErrs...)
	}

	router := &DatasourceRouter{
		nodes:   make([]*datasourceNode, 0, len(names)),
		writers: make([]*gorm.DB, 0, len(names)),
		readers: make([]*gorm.DB, 0, len(names)),
	}

	var errs []error
	for _, name := range names {
		datasource := datasources[name]
		node := &datasourceNode{name: name, writable: datasource.Writable, readable: datasource.Readable}
		router.nodes = append(router.nodes, node)
		db, err := datasource.open(ctx, debug)
		if err != nil {
			node.startupErr = err
			errs = append(errs, fmt.Errorf("database %q: %w", name, err))
			continue
		}
		node.db = db
		node.up.Store(true)
		if datasource.Writable {
			router.writers = append(router.writers, db)
		}
		if datasource.Readable {
			router.readers = append(router.readers, db)
		}
	}
	return router, errors.Join(errs...)
}

func (r *DatasourceRouter) Write() *gorm.DB {
	if r == nil || len(r.writers) == 0 {
		return nil
	}
	index := r.writeIndex.Add(1) - 1
	return r.writers[index%uint64(len(r.writers))]
}

func (r *DatasourceRouter) Read() *gorm.DB {
	if r == nil {
		return nil
	}
	if len(r.readers) == 0 {
		return r.Write()
	}
	index := r.readIndex.Add(1) - 1
	return r.readers[index%uint64(len(r.readers))]
}

// HasWriter reports whether at least one writable datasource is available for
// routing. Configured nodes that failed to open are not included.
func (r *DatasourceRouter) HasWriter() bool { return r != nil && len(r.writers) > 0 }

// DatasourceNames returns configured, active-role datasource names in stable
// order, including nodes that failed during initial connection.
func (r *DatasourceRouter) DatasourceNames() []string {
	if r == nil {
		return nil
	}
	names := make([]string, len(r.nodes))
	for index, node := range r.nodes {
		names[index] = node.name
	}
	return names
}

func (node *datasourceNode) probe(ctx context.Context) error {
	if node.startupErr != nil {
		node.up.Store(false)
		return node.startupErr
	}
	if node.db == nil {
		node.up.Store(false)
		return errors.New("gormx: datasource is unavailable")
	}
	pool, err := node.db.DB()
	if err == nil {
		err = pool.PingContext(ctx)
	}
	node.up.Store(err == nil)
	return err
}

// Probe checks every configured datasource exactly once and returns named and
// aggregate routing health from the same observations.
func (r *DatasourceRouter) Probe(ctx context.Context) HealthSnapshot {
	if r == nil {
		return HealthSnapshot{}
	}
	started := time.Now()
	snapshot := HealthSnapshot{Datasources: make([]DatasourceHealth, len(r.nodes))}

	var wait sync.WaitGroup
	for index, node := range r.nodes {
		snapshot.Datasources[index] = DatasourceHealth{Name: node.name}
		wait.Add(1)
		go func(index int, node *datasourceNode) {
			defer wait.Done()
			started := time.Now()
			err := node.probe(ctx)
			snapshot.Datasources[index].Duration = time.Since(started)
			snapshot.Datasources[index].Err = err
		}(index, node)
	}
	wait.Wait()
	snapshot.CheckedAt = time.Now().UTC()
	snapshot.Duration = time.Since(started)

	var routingErrs []error
	for index, node := range r.nodes {
		health := snapshot.Datasources[index]
		if health.Err == nil {
			continue
		}
		roles := make([]string, 0, 2)
		if node.db != nil {
			if node.writable {
				roles = append(roles, "writer")
			}
			if node.readable {
				roles = append(roles, "reader")
			}
		}
		if len(roles) > 0 {
			routingErrs = append(routingErrs, fmt.Errorf(
				"gormx: routed datasource %q (%s) is unhealthy: %w",
				node.name, strings.Join(roles, ","), health.Err,
			))
		}
	}
	snapshot.routingErr = errors.Join(routingErrs...)
	return snapshot
}

// DatasourceStats returns the last observed health and current pool counters.
func (r *DatasourceRouter) DatasourceStats() []DatasourceStats {
	if r == nil {
		return nil
	}
	result := make([]DatasourceStats, 0, len(r.nodes))
	for _, node := range r.nodes {
		stats := DatasourceStats{
			Name: node.name, Writable: node.writable, Readable: node.readable, Up: node.up.Load(),
		}
		if node.db != nil {
			if pool, err := node.db.DB(); err == nil {
				stats.Pool = pool.Stats()
			}
		}
		result = append(result, stats)
	}
	return result
}

// Close closes each underlying connection pool once, including datasources
// configured for both read and write roles.
func (r *DatasourceRouter) Close() error {
	if r == nil {
		return nil
	}
	var errs []error
	for _, node := range r.nodes {
		if node.db == nil {
			continue
		}
		sqlDB, err := node.db.DB()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
