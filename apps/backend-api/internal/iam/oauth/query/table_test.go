package query

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBindAppliesPrefixToEveryOAuthTable(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{
		DryRun: true, Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	plain, prefixed := Bind(db, ""), Bind(db, "sl_")
	actual := map[string][2]string{
		"clients":        {plain.Clients.TableName(), prefixed.Clients.TableName()},
		"codes":          {plain.AuthorizationCodes.TableName(), prefixed.AuthorizationCodes.TableName()},
		"consents":       {plain.Consents.TableName(), prefixed.Consents.TableName()},
		"refresh_tokens": {plain.RefreshTokens.TableName(), prefixed.RefreshTokens.TableName()},
	}
	require.Len(t, actual, 4)
	for name, pair := range actual {
		assert.Equal(t, "sl_"+pair[0], pair[1], name)
	}
}
