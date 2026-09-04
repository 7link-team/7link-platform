package oauth

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
	assert.Equal(t, Name, subject.ModuleName())
	source := subject.MigrationSource()
	assert.Equal(t, "goose_oauth", source.TableName)
	assert.Empty(t, source.Prefix)
	require.NotNil(t, source.FS)
	for _, dialect := range []string{"mysql", "postgres", "sqlite"} {
		files, err := fs.ReadDir(source.FS, "sql/"+dialect)
		require.NoError(t, err)
		names := make([]string, len(files))
		for index, file := range files {
			names[index] = file.Name()
		}
		assert.Equal(t, []string{
			"00001_clients.sql", "00002_codes.sql", "00003_consents.sql",
			"00004_refresh_tokens.sql", "00005_constraints.sql", "00006_refresh_principal_index.sql",
		}, names, dialect)
	}
}

func TestModuleMigrationMilestones(t *testing.T) {
	t.Parallel()
	milestones := (&Module{}).MigrationMilestones()
	require.Len(t, milestones, 3)
	assert.Equal(t, []string{"base", "constraints", "indexes"}, []string{
		milestones[0].Name, milestones[1].Name, milestones[2].Name,
	})
	assert.Equal(t, []int64{4, 5, 6}, []int64{
		milestones[0].Version, milestones[1].Version, milestones[2].Version,
	})
	assert.ElementsMatch(t, []module.MigrationMilestoneRef{
		{Module: "identity", Name: "base"}, {Module: "organization", Name: "base"},
	}, milestones[1].Dependencies)
}
