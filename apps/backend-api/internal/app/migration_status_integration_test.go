package app

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
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
	"7link.com/platform/pkg/module"
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

	// Preserve the actual historical rollback contract before installing the
	// irreversible binding-trust milestone on this same database.
	historical := &App{config: config}
	require.NoError(t, configureApplication(&historical.config))
	require.NoError(t, historical.prepare(t.Context()))
	historicalClosed := false
	t.Cleanup(func() {
		if !historicalClosed {
			assert.NoError(t, historical.shutdown())
		}
	})
	owners := historical.modules.Modules()
	for index, owner := range owners {
		if owner.ModuleName() == federation.Name {
			owners[index] = historicalFederationStatusModule{Module: owner, source: historicalFederationStatusSource(t, owner)}
		}
	}
	runHistorical := func(request MigrationRequest) ([]MigrationReport, error) {
		return ExecuteModuleMigrations(t.Context(), historical.runtime.Database().Write(), owners, request, config.TablePrefix)
	}
	reports, err := runHistorical(MigrationRequest{Action: MigrationUp, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StateClean)
	reports, err = runHistorical(MigrationRequest{Action: MigrationStatus, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StateClean)
	// The retirement milestone depends on organization; remove it before its base.
	reports, err = runHistorical(MigrationRequest{Action: MigrationDownTo, Module: core.Name, To: 16})
	require.NoError(t, err)
	require.Len(t, reports, 1)
	assert.Equal(t, int64(16), reports[0].Status.Current)

	for _, name := range iamMigrationReverseOrder() {
		reports, err := runHistorical(MigrationRequest{Action: MigrationDownTo, Module: name, To: 0})
		require.NoError(t, err, "roll back %s", name)
		require.Len(t, reports, 1)
		assertMigrationReportState(t, reports[0], goosex.StatePending)
	}
	reports, err = runHistorical(MigrationRequest{Action: MigrationStatus, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StatePending)
	reports, err = runHistorical(MigrationRequest{Action: MigrationUp, To: -1})
	require.NoError(t, err)
	assertIAMMigrationReports(t, reports, goosex.StateClean)
	require.NoError(t, historical.shutdown())
	historicalClosed = true

	assertIAMMigrationActionState(t, config, MigrationUp, goosex.StateClean)
	assertIAMMigrationActionState(t, config, MigrationStatus, goosex.StateClean)
	assertIAMMigrationStatusEndpoint(t, config)
	// Downgrading past binding trust is refused, and refused before anything is
	// undone: the milestone declares itself irreversible, so the request never
	// reaches the database. The status assertion that follows is what proves it
	// -- a rollback that had started would leave federation between milestones.
	_, err = (&App{}).RunMigrations(t.Context(), MigrationConfig{
		AppConfig: config, MigrationRequest: MigrationRequest{Action: MigrationDownTo, Module: federation.Name, To: 6},
	})
	require.ErrorContains(t, err, "is irreversible")
	require.ErrorContains(t, err, "nothing was rolled back")
	assertIAMMigrationActionState(t, config, MigrationStatus, goosex.StateClean)
	assertIAMMigrationActionState(t, config, MigrationUp, goosex.StateClean)
}

// historicalFederationVersion is the last federation migration this double
// presents. Both the milestone list and the embedded files are cut at it, so a
// migration added after it does not leave the source ahead of the milestones.
const historicalFederationVersion = 6

type historicalFederationStatusModule struct {
	module.Module
	source goosex.Source
}

func (m historicalFederationStatusModule) MigrationSource() goosex.Source { return m.source }

func (m historicalFederationStatusModule) MigrationMilestones() []module.MigrationMilestone {
	var milestones []module.MigrationMilestone
	for _, milestone := range m.Module.(module.MigrationMilestoneProvider).MigrationMilestones() {
		if milestone.Version <= historicalFederationVersion {
			milestones = append(milestones, milestone)
		}
	}
	return milestones
}

func historicalFederationStatusSource(t *testing.T, owner module.Module) goosex.Source {
	t.Helper()
	source := owner.(module.MigrationSourceProvider).MigrationSource()
	files := fstest.MapFS{}
	err := fs.WalkDir(source.FS, "sql", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		version, parseErr := strconv.ParseInt(strings.SplitN(entry.Name(), "_", 2)[0], 10, 64)
		require.NoError(t, parseErr, "migration file without a version prefix: %s", path)
		if version > historicalFederationVersion {
			return nil
		}
		contents, err := fs.ReadFile(source.FS, path)
		if err != nil {
			return err
		}
		files[path] = &fstest.MapFile{Data: contents}
		return nil
	})
	require.NoError(t, err)
	source.FS = files
	return source
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
