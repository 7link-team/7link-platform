package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/files"
	"7link.com/platform/internal/files/model"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
)

func TestFilesMigrationCommandUsesPreparedMaintenanceAndAudit(t *testing.T) {
	configPath, _ := newRewrapCLIConfig(t)
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = fmt.Fprintf(f, "\nfiles:\n  local_root: %s\n  read_backends:\n    old-local:\n      driver: local\n      options:\n        root: %s\n", filepath.ToSlash(targetRoot), filepath.ToSlash(sourceRoot))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	_, err = executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)
	db, err := (gormx.Datasource{Type: "sqlite", Writable: true, DSN: filepath.ToSlash(filepath.Join(filepath.Dir(configPath), "cli.sqlite"))}).Open(t.Context())
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	source, err := files.NewLocal(sourceRoot)
	require.NoError(t, err)
	body := []byte("operator migration proof")
	preparedService, err := files.NewAccountObjects(source, "old-local", "")
	require.NoError(t, err)
	// Stage uses the actual durable file preparation/object schema.
	_, err = preparedService.Stage(t.Context(), db, 9007199254740993, 7, "objects/9007199254740993", bytes.NewReader(body), int64(len(body)), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Object{}).Where("id = ?", int64(9007199254740993)).Update("state", "verified").Error)
	var report files.ObjectMigrationReport
	for attempt := 0; attempt < 2; attempt++ {
		output, err := executeCommand("files", "migrate-object", "--config", configPath, "--object-id", "9007199254740993", "--from-backend", "old-local")
		require.NoError(t, err)
		require.NoError(t, json.NewDecoder(strings.NewReader(output)).Decode(&report))
		require.Equal(t, attempt == 1, report.Replayed)
		require.Equal(t, "9007199254740993", report.ObjectID.String())
	}
	var count int64
	require.NoError(t, db.Table("iam_audit_events").Where("event_type = ?", "file_object_migrated").Count(&count).Error)
	require.Equal(t, int64(1), count)
	stored, err := os.ReadFile(filepath.Join(targetRoot, "objects", "9007199254740993"))
	require.NoError(t, err)
	require.Equal(t, body, stored)
	stored, err = os.ReadFile(filepath.Join(sourceRoot, "objects", "9007199254740993"))
	require.NoError(t, err)
	require.Equal(t, body, stored)
}
