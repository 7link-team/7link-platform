package goosex

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	goosev3 "github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

type State string

const (
	StateClean    State = "clean"
	StatePending  State = "pending"
	StateAhead    State = "ahead"
	StateDiverged State = "diverged"
)

// Source identifies one module-owned migration stream.
type Source struct {
	FS        fs.FS
	TableName string
}

// Status compares every embedded migration version with every applied version.
type Status struct {
	Current  int64
	Target   int64
	State    State
	Embedded []int64
	Applied  []int64
	Pending  []int64
	Unknown  []int64
}

// ResolveDialect maps a dialect string to a goose dialect and the SQL
// subdirectory holding that dialect's migrations.
func ResolveDialect(dialect string) (goosev3.Dialect, string, error) {
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "sqlite", "sqlite3":
		return goosev3.DialectSQLite3, "sql/sqlite", nil
	case "mysql":
		return goosev3.DialectMySQL, "sql/mysql", nil
	case "postgres", "postgresql", "pgsql", "pg":
		return goosev3.DialectPostgres, "sql/postgres", nil
	default:
		return "", "", fmt.Errorf("goose: unsupported dialect %q", dialect)
	}
}

// Run applies all pending migrations from fsys (rooted at sql/<dialect>) using a
// module-private goose version table. Global registry is disabled so multiple
// modules can run independently in one process.
func Run(ctx context.Context, db *sql.DB, fsys fs.FS, dialect, tableName string) error {
	return Up(ctx, db, Source{FS: fsys, TableName: tableName}, dialect)
}

// Up applies all pending migrations from source.
func Up(ctx context.Context, db *sql.DB, source Source, dialect string) error {
	provider, err := newProvider(db, source.FS, dialect, source.TableName)
	if err != nil {
		return err
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose: run migrations: %w", err)
	}
	return nil
}

// Down rolls back the most recently applied migration from one module.
func Down(ctx context.Context, db *sql.DB, fsys fs.FS, dialect, tableName string) error {
	provider, err := newProvider(db, fsys, dialect, tableName)
	if err != nil {
		return err
	}
	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("goose: rollback migration: %w", err)
	}
	return nil
}

// DownTo rolls a module back to version. The target migration remains applied.
func DownTo(ctx context.Context, db *sql.DB, fsys fs.FS, dialect, tableName string, version int64) error {
	return DownSourceTo(ctx, db, Source{FS: fsys, TableName: tableName}, dialect, version)
}

// DownSourceTo rolls source back to version. The caller must use an artifact
// that still contains every migration above the requested target.
func DownSourceTo(ctx context.Context, db *sql.DB, source Source, dialect string, version int64) error {
	provider, err := newProvider(db, source.FS, dialect, source.TableName)
	if err != nil {
		return err
	}
	if _, err := provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("goose: rollback migrations to %d: %w", version, err)
	}
	return nil
}

// UpToDate reports whether every migration in fsys has been applied to the
// database. A database with no applied migrations reports false.
func UpToDate(ctx context.Context, db *sql.DB, fsys fs.FS, dialect, tableName string) (bool, error) {
	status, err := Inspect(ctx, db, Source{FS: fsys, TableName: tableName}, dialect)
	if err != nil {
		return false, fmt.Errorf("goose: check migrations: %w", err)
	}
	return status.State == StateClean, nil
}

// Inspect compares the complete embedded and applied migration sets. Unknown
// versions are applied database versions for which this artifact has no source.
func Inspect(ctx context.Context, db *sql.DB, source Source, dialect string) (Status, error) {
	provider, err := newProvider(db, source.FS, dialect, source.TableName)
	if err != nil {
		return Status{}, err
	}
	if _, err := provider.Status(ctx); err != nil {
		return Status{}, fmt.Errorf("goose: inspect migrations: %w", err)
	}

	resolvedDialect, _, err := ResolveDialect(dialect)
	if err != nil {
		return Status{}, err
	}
	store, err := database.NewStore(database.Dialect(resolvedDialect), source.TableName)
	if err != nil {
		return Status{}, fmt.Errorf("goose: create migration store: %w", err)
	}
	dbMigrations, err := store.ListMigrations(ctx, db)
	if err != nil {
		return Status{}, fmt.Errorf("goose: list applied migrations: %w", err)
	}

	result := Status{
		State:    StateClean,
		Embedded: make([]int64, 0),
		Applied:  make([]int64, 0),
		Pending:  make([]int64, 0),
		Unknown:  make([]int64, 0),
	}
	embedded := make(map[int64]struct{})
	for _, migration := range provider.ListSources() {
		version := migration.Version
		embedded[version] = struct{}{}
		result.Embedded = append(result.Embedded, version)
		if version > result.Target {
			result.Target = version
		}
	}
	applied := make(map[int64]struct{})
	for _, migration := range dbMigrations {
		if migration.Version <= 0 || !migration.IsApplied {
			continue
		}
		applied[migration.Version] = struct{}{}
	}
	for version := range applied {
		result.Applied = append(result.Applied, version)
		if version > result.Current {
			result.Current = version
		}
		if _, exists := embedded[version]; !exists {
			result.Unknown = append(result.Unknown, version)
		}
	}
	for version := range embedded {
		if _, exists := applied[version]; !exists {
			result.Pending = append(result.Pending, version)
		}
	}
	for _, versions := range [][]int64{result.Embedded, result.Applied, result.Pending, result.Unknown} {
		slices.Sort(versions)
	}
	switch {
	case len(result.Unknown) > 0 && (len(result.Pending) > 0 || result.Current <= result.Target):
		result.State = StateDiverged
	case len(result.Unknown) > 0:
		result.State = StateAhead
	case len(result.Pending) > 0:
		result.State = StatePending
	}
	return result, nil
}

func newProvider(db *sql.DB, fsys fs.FS, dialect, tableName string) (*goosev3.Provider, error) {
	d, subdir, err := ResolveDialect(dialect)
	if err != nil {
		return nil, err
	}

	subFS, err := fs.Sub(fsys, subdir)
	if err != nil {
		return nil, fmt.Errorf("goose: resolve migrations fs: %w", err)
	}

	provider, err := goosev3.NewProvider(d, db, subFS,
		goosev3.WithTableName(tableName),
		goosev3.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("goose: create provider: %w", err)
	}
	return provider, nil
}
