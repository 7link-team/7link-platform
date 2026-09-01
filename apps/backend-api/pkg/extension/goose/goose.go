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

	// Prefix is prepended to every object a migration creates and to this
	// stream's own version table, so one schema can hold several independent
	// installations. Empty means unprefixed, which is what an existing
	// deployment already has. Validate it with ValidatePrefix before use.
	Prefix string
}

// versionTable is where this stream records applied versions. It carries the
// prefix too: two prefixed installations in one schema must not share it.
func (s Source) versionTable() string { return s.Prefix + s.TableName }

// Status compares every embedded migration version with every applied version.
type Status struct {
	Current  int64   `json:"current"`
	Target   int64   `json:"target"`
	State    State   `json:"state"`
	Embedded []int64 `json:"embedded"`
	Applied  []int64 `json:"applied"`
	Pending  []int64 `json:"pending"`
	Unknown  []int64 `json:"unknown"`
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

// Migrate moves the database to the highest embedded migration version. It is
// idempotent when the embedded and applied version sets already match.
func Migrate(ctx context.Context, db *sql.DB, source Source, dialect string) error {
	status, err := Inspect(ctx, db, source, dialect)
	if err != nil {
		return err
	}
	switch {
	case status.Current < status.Target:
		return migrateUpTo(ctx, db, source, dialect, status, status.Target)
	case status.Current > status.Target:
		return migrateDownTo(ctx, db, source, dialect, status, status.Target)
	case status.State == StateClean:
		return nil
	default:
		return migrationStateError(status)
	}
}

// MigrateUp applies all pending embedded migrations.
func MigrateUp(ctx context.Context, db *sql.DB, source Source, dialect string) error {
	status, err := Inspect(ctx, db, source, dialect)
	if err != nil {
		return err
	}
	return migrateUpTo(ctx, db, source, dialect, status, status.Target)
}

// MigrateUpTo applies embedded migrations through version.
func MigrateUpTo(ctx context.Context, db *sql.DB, source Source, dialect string, version int64) error {
	status, err := Inspect(ctx, db, source, dialect)
	if err != nil {
		return err
	}
	return migrateUpTo(ctx, db, source, dialect, status, version)
}

func migrateUpTo(
	ctx context.Context,
	db *sql.DB,
	source Source,
	dialect string,
	status Status,
	version int64,
) error {
	if version < 0 {
		return fmt.Errorf("goose: target version cannot be negative: %d", version)
	}
	if err := validateMigrationState(status); err != nil {
		return err
	}
	if version > status.Target {
		return fmt.Errorf("goose: up target %d exceeds embedded version %d", version, status.Target)
	}
	if version != 0 && !slices.Contains(status.Embedded, version) {
		return fmt.Errorf("goose: up target %d is not embedded", version)
	}
	if version < status.Current {
		return fmt.Errorf("goose: cannot migrate up from %d to lower version %d", status.Current, version)
	}
	if version == status.Current {
		return nil
	}

	provider, err := newProvider(db, source, dialect)
	if err != nil {
		return err
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		return fmt.Errorf("goose: migrate up to %d: %w", version, err)
	}
	return nil
}

// MigrateDown rolls back the most recently applied embedded migration.
func MigrateDown(ctx context.Context, db *sql.DB, source Source, dialect string) error {
	status, err := Inspect(ctx, db, source, dialect)
	if err != nil {
		return err
	}
	if err := validateMigrationState(status); err != nil {
		return err
	}
	if status.Current == 0 {
		return nil
	}

	provider, err := newProvider(db, source, dialect)
	if err != nil {
		return err
	}
	if _, err := provider.Down(ctx); err != nil {
		return fmt.Errorf("goose: migrate down from %d: %w", status.Current, err)
	}
	return nil
}

// MigrateDownTo rolls embedded migrations back to version. The target
// migration remains applied.
func MigrateDownTo(ctx context.Context, db *sql.DB, source Source, dialect string, version int64) error {
	status, err := Inspect(ctx, db, source, dialect)
	if err != nil {
		return err
	}
	return migrateDownTo(ctx, db, source, dialect, status, version)
}

func migrateDownTo(
	ctx context.Context,
	db *sql.DB,
	source Source,
	dialect string,
	status Status,
	version int64,
) error {
	if version < 0 {
		return fmt.Errorf("goose: target version cannot be negative: %d", version)
	}
	if err := validateMigrationState(status); err != nil {
		return err
	}
	if version > status.Current {
		return fmt.Errorf("goose: cannot migrate down from %d to higher version %d", status.Current, version)
	}
	if version == status.Current {
		return nil
	}
	if version != 0 && !slices.Contains(status.Applied, version) {
		return fmt.Errorf("goose: down target %d is not applied", version)
	}

	provider, err := newProvider(db, source, dialect)
	if err != nil {
		return err
	}
	if _, err := provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("goose: migrate down to %d: %w", version, err)
	}
	return nil
}

func validateMigrationState(status Status) error {
	if len(status.Unknown) > 0 {
		return fmt.Errorf("goose: database contains migrations unavailable in embedded SQL: %v", status.Unknown)
	}
	for _, version := range status.Pending {
		if version <= status.Current {
			return migrationStateError(status)
		}
	}
	return nil
}

func migrationStateError(status Status) error {
	return fmt.Errorf(
		"goose: embedded and applied migrations diverge (current=%d target=%d pending=%v unknown=%v)",
		status.Current,
		status.Target,
		status.Pending,
		status.Unknown,
	)
}

// UpToDate reports whether every embedded migration has been applied. A
// database with no applied migrations reports false.
func UpToDate(ctx context.Context, db *sql.DB, source Source, dialect string) (bool, error) {
	status, err := Inspect(ctx, db, source, dialect)
	if err != nil {
		return false, fmt.Errorf("goose: check migrations: %w", err)
	}
	return status.State == StateClean, nil
}

// Inspect compares the complete embedded and applied migration sets. Unknown
// versions are applied database versions for which this artifact has no source.
func Inspect(ctx context.Context, db *sql.DB, source Source, dialect string) (Status, error) {
	provider, err := newProvider(db, source, dialect)
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
	store, err := database.NewStore(resolvedDialect, source.versionTable())
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

func newProvider(db *sql.DB, source Source, dialect string) (*goosev3.Provider, error) {
	d, subdir, err := ResolveDialect(dialect)
	if err != nil {
		return nil, err
	}
	if err := ValidatePrefix(source.Prefix); err != nil {
		return nil, err
	}

	subFS, err := fs.Sub(source.FS, subdir)
	if err != nil {
		return nil, fmt.Errorf("goose: resolve migrations fs: %w", err)
	}

	provider, err := goosev3.NewProvider(d, db,
		templateFS{fsys: subFS, data: templateData{Prefix: source.Prefix}},
		goosev3.WithTableName(source.versionTable()),
		goosev3.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("goose: create provider: %w", err)
	}
	return provider, nil
}
