package identityi18n

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPublishesEveryIdentityLocale(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, Register())

	keys := []string{
		PrincipalNotFound, LoginNameExists, InvalidPrincipal, LastTenantAdministrator,
		IdentityUnavailable, ServiceAccountNotFound, ServiceAccountCredentialNotFound,
		ServiceAccountVersionConflict, ServiceAccountCredentialLimit, InvalidServiceAccount,
	}
	for _, locale := range platformi18n.Supported() {
		ctx := platformi18n.WithLocale(t.Context(), locale.Code)
		for _, key := range keys {
			assert.NotEqual(t, key, platformi18n.TContext(ctx, key), "%s is untranslated in %s", key, locale.Code)
		}
	}
	assert.Error(t, Register(), "a module locale namespace is registered exactly once")
}

func TestEveryLocaleCarriesExactlyTheDeclaredKeys(t *testing.T) {
	declared := []string{
		PrincipalNotFound, LoginNameExists, InvalidPrincipal, LastTenantAdministrator,
		IdentityUnavailable, ServiceAccountNotFound, ServiceAccountCredentialNotFound,
		ServiceAccountVersionConflict, ServiceAccountCredentialLimit, InvalidServiceAccount,
	}
	expected := make([]string, 0, len(declared))
	for _, key := range declared {
		expected = append(expected, strings.TrimPrefix(key, Namespace+"."))
	}

	entries, err := fs.ReadDir(locales, "locales")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	for _, entry := range entries {
		data, err := fs.ReadFile(locales, "locales/"+entry.Name())
		require.NoError(t, err)
		messages := map[string]string{}
		require.NoError(t, json.Unmarshal(data, &messages))
		actual := make([]string, 0, len(messages))
		for key, message := range messages {
			assert.NotEmpty(t, message, "%s: %s is empty", entry.Name(), key)
			actual = append(actual, key)
		}
		assert.ElementsMatch(t, expected, actual, "%s must declare exactly the module's message keys", entry.Name())
	}
}
