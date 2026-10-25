package templates

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMigrationsContainEquivalentPrefixedVersionAndBindingTables(t *testing.T) {
	source := (&Module{}).MigrationSource()
	require.Equal(t, "goose_templates", source.TableName)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		b, e := migrationsFS.ReadFile("sql/" + dialect + "/00001_templates.sql")
		require.NoError(t, e)
		for _, part := range []string{"{{.Prefix}}templates_versions", "{{.Prefix}}templates_project_bindings", "UNIQUE (tenant_id,template_key,semver)", "UNIQUE (tenant_id,project_id)", "FOREIGN KEY (template_version_id)", "definition_sha256", "DROP TABLE"} {
			require.Contains(t, string(b), part)
		}
	}
}
