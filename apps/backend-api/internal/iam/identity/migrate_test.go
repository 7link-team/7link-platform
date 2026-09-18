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
		assert.Equal(t, []string{"00001_principals.sql", "00002_service_accounts.sql",
			"00003_principal_lifecycles.sql", "00004_tombstone_guards.sql", "00005_closure_requests.sql", "00006_avatars.sql"}, names,
			"dialect %s must carry the module's six migrations", dialect)
	}
}

func TestModuleMigrationMilestones(t *testing.T) {
	t.Parallel()

	milestones := (&Module{}).MigrationMilestones()
	require.Len(t, milestones, 4)
	assert.Equal(t, "base", milestones[0].Name)
	assert.Equal(t, int64(2), milestones[0].Version)
	assert.Empty(t, milestones[0].Dependencies, "identity owns the root of the IAM migration DAG")
	assert.Equal(t, "lifecycle", milestones[1].Name)
	assert.Equal(t, int64(4), milestones[1].Version)
	assert.Equal(t, "closure-requests", milestones[2].Name)
	assert.Equal(t, int64(5), milestones[2].Version)
	assert.Equal(t, "avatars", milestones[3].Name)
	assert.Equal(t, int64(6), milestones[3].Version)

	var _ module.MigrationSourceProvider = (*Module)(nil)
	var _ module.MigrationMilestoneProvider = (*Module)(nil)
}
