package app

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"7link.com/platform/internal/files"
	iamaudit "7link.com/platform/internal/iam/audit"
	iamauthn "7link.com/platform/internal/iam/authn"
	"7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	infraaudit "7link.com/platform/internal/infra/audit"
	"7link.com/platform/internal/infra/dlock"
	"7link.com/platform/internal/infra/migrationstatus"
	"7link.com/platform/internal/infra/wuid"
	"7link.com/platform/internal/todo"
	authnext "7link.com/platform/pkg/extension/authn"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/extension/huma/respx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	iamMigrationDatabaseType = "IAM_MIGRATION_DATABASE_TYPE"
	iamMigrationDatabaseDSN  = "IAM_MIGRATION_DATABASE_DSN"
	iamMigrationTablePrefix  = "IAM_MIGRATION_TABLE_PREFIX"
)

func TestIAMMigrationStatusExternalDatabase(t *testing.T) {
	databaseType := strings.TrimSpace(os.Getenv(iamMigrationDatabaseType))
	dsn := strings.TrimSpace(os.Getenv(iamMigrationDatabaseDSN))
	if databaseType == "" || dsn == "" {
		t.Skip("external IAM migration database is not configured")
	}
	config := iamMigrationStatusAppConfig(Database{
		Type: databaseType, Dsn: dsn, Writable: true, Readable: true,
		MaxOpenConns: 8, MaxIdleConns: 4,
	}, os.Getenv(iamMigrationTablePrefix))

	assertIAMMigrationActionState(t, config, MigrationUp, goosex.StateClean)
	assertIAMMigrationActionState(t, config, MigrationStatus, goosex.StateClean)
	assertIAMMigrationStatusEndpoint(t, config)

	for _, name := range iamMigrationReverseOrder() {
		reports, err := (&App{}).RunMigrations(t.Context(), MigrationConfig{
			AppConfig: config,
			MigrationRequest: MigrationRequest{
				Action: MigrationDownTo, Module: name, To: 0,
			},
		})
		require.NoError(t, err, "roll back %s", name)
		require.Len(t, reports, 1)
		assertMigrationReportState(t, reports[0], goosex.StatePending)
	}
	assertIAMMigrationActionState(t, config, MigrationStatus, goosex.StatePending)
	assertIAMMigrationActionState(t, config, MigrationUp, goosex.StateClean)
}

func iamMigrationStatusAppConfig(database Database, prefix string) AppConfig {
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	key := base64.RawStdEncoding.EncodeToString(seed)
	config := validTestAppConfig()
	config.Name = "iam-migration-status-integration"
	config.TablePrefix = prefix
	config.Database = map[string]Database{"primary": database}
	config.API.MigrationStatus.Enabled = true
	config.Authn = IAMAuthn(authnext.Config{
		Enabled: true, Issuer: "https://iam.example.test", SigningKey: key, AccessTokenTTL: time.Hour,
		Web: authnext.WebConfig{
			Enabled: true, PostLoginURL: "https://app.example.test/home",
			PostLogoutURL:     "https://app.example.test/signed-out",
			AllowedReturnURLs: []string{"https://app.example.test/home"},
			AllowedOrigins:    []string{"https://app.example.test"},
		},
		MFA: authnext.MFAConfig{EncryptionKey: key},
	})
	config.Federation = IAMFederation{Enabled: true, EncryptionKey: key}
	return config
}

func assertIAMMigrationActionState(t *testing.T, config AppConfig, action MigrationAction, state goosex.State) {
	t.Helper()
	reports, err := (&App{}).RunMigrations(t.Context(), MigrationConfig{
		AppConfig: config, MigrationRequest: MigrationRequest{Action: action, To: -1},
	})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, state)
}

func assertIAMMigrationReports(t *testing.T, reports []MigrationReport, state goosex.State) {
	t.Helper()
	require.Len(t, reports, 14)
	names := make([]string, 0, len(reports))
	for _, report := range reports {
		names = append(names, report.Module)
		assertMigrationReportState(t, report, state)
	}
	sort.Strings(names)
	expected := iamMigrationFlowNames()
	sort.Strings(expected)
	assert.Equal(t, expected, names)
}

func assertMigrationReportState(t *testing.T, report MigrationReport, state goosex.State) {
	t.Helper()
	assert.Equal(t, state, report.Status.State, report.Module)
	assert.Empty(t, report.Status.Unknown, report.Module)
	switch state {
	case goosex.StateClean:
		assert.Equal(t, report.Status.Target, report.Status.Current, report.Module)
		assert.Equal(t, report.Status.Embedded, report.Status.Applied, report.Module)
		assert.Empty(t, report.Status.Pending, report.Module)
	case goosex.StatePending:
		assert.Zero(t, report.Status.Current, report.Module)
		assert.Empty(t, report.Status.Applied, report.Module)
		assert.Equal(t, report.Status.Embedded, report.Status.Pending, report.Module)
	}
}

func assertIAMMigrationStatusEndpoint(t *testing.T, config AppConfig) {
	t.Helper()
	application := &App{config: config}
	require.NoError(t, application.bootstrap(t.Context()))
	closed := false
	t.Cleanup(func() {
		if !closed {
			assert.NoError(t, application.shutdown())
		}
	})
	router, api := moduleRouter(t, application)

	for _, path := range []string{migrationstatus.Path, migrationstatus.LegacyPath} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code, path)
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"), path)
		var envelope respx.Envelope[struct {
			State   goosex.State             `json:"state"`
			Modules []migrationstatus.Report `json:"modules"`
		}]
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		assert.Zero(t, envelope.Code)
		assert.Equal(t, goosex.StateClean, envelope.Data.State)
		reports := make([]MigrationReport, len(envelope.Data.Modules))
		for index, report := range envelope.Data.Modules {
			reports[index] = MigrationReport{Module: report.Module, Status: report.Status}
		}
		assertIAMMigrationReports(t, reports, goosex.StateClean)
	}
	assert.Contains(t, api.OpenAPI().Paths, migrationstatus.Path)
	assert.Contains(t, api.OpenAPI().Paths, migrationstatus.LegacyPath)
	assert.True(t, api.OpenAPI().Paths[migrationstatus.LegacyPath].Get.Deprecated)
	require.NoError(t, application.shutdown())
	closed = true
}

func iamMigrationFlowNames() []string {
	return []string{
		dlock.Name, wuid.Name, infraaudit.Name, iamaudit.Name, core.Name, files.Name, iamauthn.Name, identity.Name,
		organization.Name, oauth.Name, provisioning.Name, governance.Name, federation.Name, todo.Name,
	}
}

func iamMigrationReverseOrder() []string {
	return []string{
		federation.Name, iamauthn.Name, oauth.Name, governance.Name, provisioning.Name,
		organization.Name, files.Name, core.Name, identity.Name, iamaudit.Name,
		todo.Name, infraaudit.Name, wuid.Name, dlock.Name,
	}
}
