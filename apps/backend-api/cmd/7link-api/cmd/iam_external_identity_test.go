package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/maintenance"
	gormx "7link.com/platform/pkg/extension/gorm"
	"github.com/stretchr/testify/require"
)

func TestExternalIdentityOperatorCutoverAndEvidence(t *testing.T) {
	configPath, passwordPath := newRewrapCLIConfig(t)
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = fmt.Fprintf(file, "\nfederation:\n  enabled: true\n  encryption_key: %s\n", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("f", 32))))
	require.NoError(t, err)
	require.NoError(t, file.Close())
	_, err = executeCommand("migrate", "auto", "--config", configPath)
	require.NoError(t, err)
	_, err = executeCommand("iam", "bootstrap-platform-admin", "--config", configPath, "--login", "admin", "--password-file", passwordPath)
	require.NoError(t, err)
	output, err := executeCommand("iam", "external-identity-inventory", "--config", configPath)
	require.NoError(t, err)
	require.Equal(t, "[]", strings.TrimSpace(output))
	db, err := (gormx.Datasource{Type: "sqlite", Writable: true, DSN: filepath.ToSlash(filepath.Join(filepath.Dir(configPath), "cli.sqlite"))}).Open(t.Context())
	require.NoError(t, err)
	connection, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	var principalID int64
	require.NoError(t, db.Raw("SELECT id FROM iam_principals LIMIT 1").Scan(&principalID).Error)
	now := time.Now().UTC()
	require.NoError(t, db.Table("iam_identity_providers").Create(map[string]any{"id": 10, "tenant_id": 7, "name": "Legacy", "provider_type": "oidc", "issuer": "https://legacy.example.test", "client_id": "legacy", "created_at": now, "updated_at": now}).Error)
	require.NoError(t, db.Table("iam_identity_links").Create(map[string]any{"provider_id": 10, "tenant_id": 7, "principal_id": principalID, "external_subject": "legacy", "last_login_at": now, "created_at": now, "updated_at": now}).Error)
	output, err = executeCommand("iam", "external-identity-inventory", "--config", configPath)
	require.ErrorIs(t, err, maintenance.ErrLegacyIdentityUnresolved)
	var inventory []federation.LegacyIdentityInventory
	require.NoError(t, json.NewDecoder(strings.NewReader(output)).Decode(&inventory))
	require.Len(t, inventory, 1)
	require.Equal(t, int64(1), inventory[0].UnresolvedLinkCount)
	var quarantineCount int64
	require.NoError(t, db.Table("iam_identity_links").Where("quarantined_at IS NOT NULL").Count(&quarantineCount).Error)
	require.Zero(t, quarantineCount, "inventory must not perform cutover")
	_, err = executeCommand("iam", "quarantine-external-identities", "--config", configPath)
	require.NoError(t, err)
	require.NoError(t, db.Table("iam_identity_links").Where("quarantined_at IS NOT NULL").Count(&quarantineCount).Error)
	require.Equal(t, int64(1), quarantineCount)
	evidencePath := filepath.Join(filepath.Dir(configPath), "ownership-evidence")
	require.NoError(t, os.WriteFile(evidencePath, []byte("Operator reviewed independently enrolled password and verified pre-federation mailbox history; incident record 123."), 0o600))
	_, err = executeCommand("iam", "record-external-identity-evidence", "--config", configPath, "--principal-id", fmt.Sprint(principalID), "--evidence-file", evidencePath)
	require.NoError(t, err)
	_, err = executeCommand("iam", "external-identity-inventory", "--config", configPath)
	require.NoError(t, err)
	var trusted int64
	require.NoError(t, db.Table("iam_identity_links").Where("binding_provenance <> 'unknown' OR quarantined_at IS NULL").Count(&trusted).Error)
	require.Zero(t, trusted, "operator evidence must not trust or unquarantine a binding")
}
