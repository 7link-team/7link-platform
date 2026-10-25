package testdb

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gormProbe struct {
	ID     int64 `gorm:"primaryKey"`
	Status string
}

func TestOpenSQLiteEnablesForeignKeys(t *testing.T) {
	db := OpenSQLite(t)
	db.SetMaxOpenConns(3)
	connections := make([]*sql.Conn, 0, 3)
	for range 3 {
		connection, err := db.Conn(t.Context())
		require.NoError(t, err)
		connections = append(connections, connection)
		t.Cleanup(func() {
			if err := connection.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
				t.Errorf("close SQLite connection: %v", err)
			}
		})
	}
	for _, connection := range connections {
		var enabled int
		require.NoError(t, connection.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled))
		require.Equal(t, 1, enabled)
		require.NoError(t, connection.Close())
	}

	_, err := db.ExecContext(t.Context(), `
CREATE TABLE parents (id INTEGER PRIMARY KEY);
CREATE TABLE children (
    id        INTEGER PRIMARY KEY,
    parent_id INTEGER NOT NULL REFERENCES parents (id)
);`)
	require.NoError(t, err)

	_, err = db.ExecContext(t.Context(), `INSERT INTO children (id, parent_id) VALUES (1, 404)`)
	require.Error(t, err)
}

func TestOpenMySQLWithDSNAllowsAnotherPool(t *testing.T) {
	db, dsn := OpenMySQLWithDSN(t)
	require.NoError(t, db.PingContext(t.Context()))

	other, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, other.Close()) })
	require.NoError(t, other.PingContext(t.Context()))
	_, err = other.ExecContext(t.Context(), "CREATE TABLE tcp_probe (id BIGINT PRIMARY KEY)")
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO tcp_probe (id) VALUES (1)")
	require.NoError(t, err)
	var count int
	require.NoError(t, other.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM tcp_probe").Scan(&count))
	require.Equal(t, 1, count)

	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn: other, SkipInitializeWithVersion: true,
	}), &gorm.Config{DisableAutomaticPing: true})
	require.NoError(t, err)
	require.NoError(t, gormDB.Exec("CREATE TABLE gorm_probe (id BIGINT PRIMARY KEY, status VARCHAR(16) NOT NULL)").Error)
	require.NoError(t, gormDB.Transaction(func(tx *gorm.DB) error {
		result := tx.Table("gorm_probe").Clauses(clause.OnConflict{DoNothing: true}).Create(&gormProbe{
			ID: 1, Status: "active",
		})
		require.NoError(t, result.Error)
		require.Equal(t, int64(1), result.RowsAffected)
		var status string
		require.NoError(t, tx.Table("gorm_probe").Select("status").Where("id = ?", 1).Scan(&status).Error)
		require.Equal(t, "active", status)
		return nil
	}))
}

func TestPostgresPortConflictClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "preflight listener", err: errors.New("process already listening on port 5432"), want: true},
		{name: "unix bind", err: errors.New("could not bind IPv4 address: Address already in use"), want: true},
		{name: "windows bind", err: errors.New("could not bind IPv6 address: Only one usage of each socket address is normally permitted"), want: true},
		{name: "bind permission", err: errors.New("could not bind Unix-domain socket: Permission denied"), want: false},
		{name: "address unavailable", err: errors.New("could not bind IPv4 address: Cannot assign requested address"), want: false},
		{name: "unrelated startup failure", err: errors.New("unable to extract postgres archive"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isPostgresPortConflict(tt.err))
		})
	}
}

// flakyRemover refuses the first failures calls, the way Windows refuses to
// delete a library a just-stopped process still holds open.
type flakyRemover struct {
	failures int
	calls    int
	slept    []time.Duration
}

var errFileInUse = errors.New("unlinkat icudt67.dll: Access is denied")

func (r *flakyRemover) remove(string) error {
	r.calls++
	if r.calls <= r.failures {
		return errFileInUse
	}
	return nil
}

func (r *flakyRemover) sleep(delay time.Duration) { r.slept = append(r.slept, delay) }

func (r *flakyRemover) waited() time.Duration {
	var total time.Duration
	for _, delay := range r.slept {
		total += delay
	}
	return total
}

func TestRemoveEventuallyRemovesAtOnceWhenNothingHoldsTheDirectory(t *testing.T) {
	remover := &flakyRemover{}
	require.NoError(t, removeEventually("root", remover.remove, remover.sleep))
	require.Equal(t, 1, remover.calls)
	require.Empty(t, remover.slept, "a removable directory is not waited on")
}

func TestRemoveEventuallyRetriesWithDoublingDelayUntilReleased(t *testing.T) {
	remover := &flakyRemover{failures: 3}
	require.NoError(t, removeEventually("root", remover.remove, remover.sleep))
	require.Equal(t, 4, remover.calls)
	require.Equal(t, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}, remover.slept)
}

func TestRemoveEventuallyReportsADirectoryThatStaysHeld(t *testing.T) {
	remover := &flakyRemover{failures: 1 << 30}
	err := removeEventually("root", remover.remove, remover.sleep)
	require.ErrorIs(t, err, errFileInUse, "the refusal is returned, not swallowed")
	require.ErrorContains(t, err, "root")
	require.GreaterOrEqual(t, remover.waited(), postgresRemoveBudget, "it waits out the whole budget")
	require.Less(t, remover.waited(), postgresRemoveBudget+2*time.Second, "and not much beyond it")
	for _, delay := range remover.slept {
		require.LessOrEqual(t, delay, 2*time.Second, "no single wait exceeds the cap")
	}
	require.Equal(t, len(remover.slept)+1, remover.calls, "every wait is followed by another attempt")
}

func TestOpenMySQLRebuildRetainsCurrentForeignKeys(t *testing.T) {
	db := OpenMySQL(t)
	for _, statement := range []string{
		"CREATE TABLE parents (tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, label TEXT, PRIMARY KEY(tenant_id,id))",
		"CREATE TABLE children (tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, parent_id BIGINT NOT NULL, PRIMARY KEY(tenant_id,id), FOREIGN KEY(tenant_id,parent_id) REFERENCES parents(tenant_id,id))",
		"INSERT INTO parents VALUES(1,2,'before')",
		"CREATE TABLE children_next (tenant_id BIGINT NOT NULL, id BIGINT NOT NULL, parent_id BIGINT NOT NULL, PRIMARY KEY(tenant_id,id), FOREIGN KEY(tenant_id,parent_id) REFERENCES parents(tenant_id,id))",
		"DROP TABLE children",
		"RENAME TABLE children_next TO children",
		"INSERT INTO children VALUES(1,3,2)",
		"UPDATE parents SET label='after' WHERE tenant_id=1 AND id=2",
	} {
		_, err := db.ExecContext(t.Context(), statement)
		require.NoError(t, err, statement)
	}
	_, err := db.ExecContext(t.Context(), "INSERT INTO children VALUES(1,4,999)")
	require.Error(t, err, "renamed table must still enforce its foreign key")
	var name, definition string
	require.NoError(t, db.QueryRowContext(t.Context(), "SHOW CREATE TABLE children").Scan(&name, &definition))
	require.Contains(t, definition, "REFERENCES `parents` (`tenant_id`,`id`)")
}
