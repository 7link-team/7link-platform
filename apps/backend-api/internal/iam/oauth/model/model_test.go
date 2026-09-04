package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tableNamer interface {
	TableName() string
}

func TestOAuthModelsOwnExactTableSet(t *testing.T) {
	t.Parallel()
	models := []tableNamer{Client{}, AuthorizationCode{}, Consent{}, RefreshToken{}}
	expected := []string{"iam_oauth_clients", "iam_oauth_codes", "iam_oauth_consents", "iam_refresh_tokens"}
	require.Len(t, models, 4)
	seen := make(map[string]struct{}, len(models))
	for index, item := range models {
		name := item.TableName()
		assert.Equal(t, expected[index], name)
		_, duplicate := seen[name]
		assert.False(t, duplicate, "duplicate model table %s", name)
		seen[name] = struct{}{}
	}
}
