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
			"00007_client_consent_mode.sql", "00008_consent_requests.sql",
			"00009_principal_erasure_guards.sql",
			"00010_grant_incarnation.sql", "00011_device_and_exchange.sql",
		}, names, dialect)
	}
}

func TestModuleMigrationMilestones(t *testing.T) {
	t.Parallel()
	milestones := (&Module{}).MigrationMilestones()
	require.Len(t, milestones, 7)
	assert.Equal(t, module.MigrationMilestone{Name: "device-and-exchange", Version: 11}, milestones[6])
	assert.Equal(t, module.MigrationMilestone{Name: "grant-incarnation", Version: 10}, milestones[5])
	assert.Equal(t, []string{"base", "constraints", "indexes", "consent", "principal-erasure"}, []string{
		milestones[0].Name, milestones[1].Name, milestones[2].Name, milestones[3].Name, milestones[4].Name,
	})
	assert.Equal(t, []int64{4, 5, 6, 8, 9}, []int64{
		milestones[0].Version, milestones[1].Version, milestones[2].Version, milestones[3].Version, milestones[4].Version,
	})
	assert.Equal(t, []module.MigrationMilestoneRef{{Module: "identity", Name: "lifecycle"}}, milestones[4].Dependencies)
	assert.ElementsMatch(t, []module.MigrationMilestoneRef{
		{Module: "identity", Name: "base"}, {Module: "organization", Name: "base"},
	}, milestones[1].Dependencies)
}
