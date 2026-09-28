package testdb

import (
	"database/sql"
	"errors"
	"testing"

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
