package migration_test

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	iamaudit "7link.com/platform/internal/iam/audit"
	"7link.com/platform/internal/iam/authn"
	iamcore "7link.com/platform/internal/iam/core"
	"7link.com/platform/internal/iam/federation"
	"7link.com/platform/internal/iam/governance"
	"7link.com/platform/internal/iam/identity"
	"7link.com/platform/internal/iam/oauth"
	"7link.com/platform/internal/iam/organization"
	"7link.com/platform/internal/iam/provisioning"
	goosex "7link.com/platform/pkg/extension/goose"
	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var createIAMTablePattern = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+\{\{\.Prefix\}\}(iam_[a-z0-9_]+)`)

type iamMigrationContract struct {
	name       string
	tableCount int
	module     module.Module
}

func TestIAMMigrationOwnershipIsAutonomousAndComplete(t *testing.T) {
	contracts := iamMigrationContracts()
	owners := make(map[string]string, 63)
	versionTables := make(map[string]string, len(contracts))
	for _, contract := range contracts {
		provider, ok := contract.module.(module.MigrationSourceProvider)
		require.True(t, ok, "%s must own a migration source", contract.name)
		source := provider.MigrationSource()
		assert.NotEmpty(t, source.TableName)
		if previous, duplicate := versionTables[source.TableName]; duplicate {
			t.Errorf("modules %s and %s share migration version table %s", previous, contract.name, source.TableName)
		}
		versionTables[source.TableName] = contract.name

		sets := make(map[string][]string, 3)
		for _, dialect := range []string{"postgres", "mysql", "sqlite"} {
			sets[dialect] = iamMigrationTables(t, source, dialect)
			assert.Len(t, sets[dialect], contract.tableCount, "%s/%s table count", contract.name, dialect)
		}
		assert.Equal(t, sets["postgres"], sets["mysql"], "%s postgres/mysql table set", contract.name)
		assert.Equal(t, sets["postgres"], sets["sqlite"], "%s postgres/sqlite table set", contract.name)
		for _, table := range sets["postgres"] {
			if previous, duplicate := owners[table]; duplicate {
				t.Errorf("table %s is owned by both %s and %s", table, previous, contract.name)
			}
			owners[table] = contract.name
		}
	}
	assert.Len(t, owners, 63)
}

func iamMigrationContracts() []iamMigrationContract {
	return []iamMigrationContract{
		{name: "iam", tableCount: 13, module: &iamcore.Module{}},
		{name: "authn", tableCount: 13, module: &authn.Module{}},
		{name: "identity", tableCount: 3, module: &identity.Module{}},
		{name: "oauth", tableCount: 4, module: &oauth.Module{}},
		{name: "audit", tableCount: 3, module: &iamaudit.Module{}},
		{name: "organization", tableCount: 14, module: &organization.Module{}},
		{name: "governance", tableCount: 4, module: &governance.Module{}},
		{name: "provisioning", tableCount: 5, module: &provisioning.Module{}},
		{name: "federation", tableCount: 4, module: &federation.Module{}},
	}
}

func iamMigrationTables(t *testing.T, source goosex.Source, dialect string) []string {
	t.Helper()
	_, subdirectory, err := goosex.ResolveDialect(dialect)
	require.NoError(t, err)
	root, err := fs.Sub(source.FS, subdirectory)
	require.NoError(t, err)
	tables := make(map[string]struct{})
	require.NoError(t, fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".sql" || strings.Contains(entry.Name(), "constraints") {
			return walkErr
		}
		contents, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return readErr
		}
		for _, match := range createIAMTablePattern.FindAllSubmatch(contents, -1) {
			tables[strings.ToLower(string(match[1]))] = struct{}{}
		}
		return nil
	}))
	result := make([]string, 0, len(tables))
	for table := range tables {
		result = append(result, table)
	}
	sort.Strings(result)
	return result
}
