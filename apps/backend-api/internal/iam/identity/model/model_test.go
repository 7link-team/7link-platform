package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface {
	TableName() string
}

func TestIdentityModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()
	models := []tableNamer{Principal{}, ServiceAccount{}, ServiceAccountCredential{}}
	expected := []string{"iam_principals", "iam_service_accounts", "iam_service_account_credentials"}
	require.Len(t, models, 3)
	for index, item := range models {
		assert.Equal(t, expected[index], item.TableName())
	}
}
