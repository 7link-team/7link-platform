package identity

import (
	"io/fs"
	"testing"

	"7link.com/platform/pkg/module"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModuleMigrationContract(t *testing.T) {
	t.Parallel()

	subject := &Module{}
	assert.Equal(t, "identity", subject.ModuleName())
	assert.Equal(t, Name, subject.ModuleName())

	source := subject.MigrationSource()
	assert.Equal(t, "goose_identity", source.TableName)
	assert.Empty(t, source.Prefix, "the migrator owns the deployment prefix")
	require.NotNil(t, source.FS)

	entries, err := fs.ReadDir(source.FS, "sql")
	require.NoError(t, err)
	dialects := make([]string, 0, len(entries))
	for _, entry := range entries {
		dialects = append(dialects, entry.Name())
	}
	assert.ElementsMatch(t, []string{"mysql", "postgres", "sqlite"}, dialects)

	for _, dialect := range dialects {
		files, err := fs.ReadDir(source.FS, "sql/"+dialect)
		require.NoError(t, err)
		names := make([]string, 0, len(files))
		for _, file := range files {
			names = append(names, file.Name())
		}
		assert.Equal(t, []string{"00001_principals.sql", "00002_service_accounts.sql"}, names,
			"dialect %s must carry the module's two migrations", dialect)
	}
}

func TestModuleMigrationMilestones(t *testing.T) {
	t.Parallel()

	milestones := (&Module{}).MigrationMilestones()
	require.Len(t, milestones, 1)
	assert.Equal(t, "base", milestones[0].Name)
	assert.Equal(t, int64(2), milestones[0].Version)
	assert.Empty(t, milestones[0].Dependencies, "identity owns the root of the IAM migration DAG")

	var _ module.MigrationSourceProvider = (*Module)(nil)
	var _ module.MigrationMilestoneProvider = (*Module)(nil)
}
