package oauthi18n

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	platformi18n "7link.com/platform/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var declaredKeys = []string{
	InvalidClientConfiguration, ClientNotFound, InvalidRequest, InvalidGrant,
	InvalidClient, InvalidToken, InsufficientScope, UnsupportedTokenType, ServerError,
	ConsentRequired, ConsentTitle, ConsentPrompt, ConsentAllow, ConsentDeny,
	ScopeKey("openid"), ScopeKey("profile"), ScopeKey("email"), ScopeKey("offline_access"),
}

func TestRegisterPublishesEveryOAuthLocale(t *testing.T) {
	require.NoError(t, platformi18n.NewModule(nil, platformi18n.Base).Initialize(t.Context()))
	require.NoError(t, Register())
	for _, locale := range platformi18n.Supported() {
		ctx := platformi18n.WithLocale(t.Context(), locale.Code)
		for _, key := range declaredKeys {
			assert.NotEqual(t, key, platformi18n.TContext(ctx, key), "%s is untranslated in %s", key, locale.Code)
		}
	}
	assert.Error(t, Register(), "a module locale namespace is registered exactly once")
}

func TestScopeKeyOnlyEverAddressesAScopeMessage(t *testing.T) {
	assert.Equal(t, Namespace+".scope_offline_access", ScopeKey("offline_access"))
	assert.Equal(t, Namespace+".scope_openid", ScopeKey("OpenID"))
	// A scope is client configuration, so it can carry anything. Everything
	// outside the message-key alphabet collapses to an underscore, which keeps
	// a hostile scope from addressing a message other than its own.
	assert.Equal(t, Namespace+".scope_a_b_c", ScopeKey("a.b-c"))
	assert.Equal(t, Namespace+".scope_", ScopeKey(""))
}

func TestEveryLocaleCarriesExactlyTheDeclaredKeys(t *testing.T) {
	expected := make([]string, 0, len(declaredKeys))
	for _, key := range declaredKeys {
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
		assert.ElementsMatch(t, expected, actual, entry.Name())
	}
}
