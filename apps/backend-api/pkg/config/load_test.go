package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestLoadStrict_OK(t *testing.T) {
	p := writeTemp(t, "name: svc\nport: 9000\ntimeout: 15m\n")
	var cfg genSample
	require.NoError(t, LoadStrict(p, &cfg))
	assert.Equal(t, "svc", cfg.Name)
	assert.Equal(t, 9000, cfg.Port)
}

func TestLoadStrict_UnknownKeyErrors(t *testing.T) {
	p := writeTemp(t, "name: svc\nnope: 1\n")
	err := LoadStrict(p, &genSample{})
	require.Error(t, err, "an unknown key must fail validation")
	assert.Contains(t, err.Error(), "nope")
}

func TestLoadStrict_MissingFileErrors(t *testing.T) {
	err := LoadStrict(filepath.Join(t.TempDir(), "absent.yaml"), &genSample{})
	assert.Error(t, err)
}

func TestLoadStrictAppliesTrimAndRequiredTags(t *testing.T) {
	type taggedConfig struct {
		Name string `mapstructure:"name" trim:"true" required:"true"`
	}

	path := writeTemp(t, "name: '  api  '\n")
	var configured taggedConfig
	require.NoError(t, LoadStrict(path, &configured))
	assert.Equal(t, "api", configured.Name)

	missingPath := writeTemp(t, "{}\n")
	require.EqualError(t, LoadStrict(missingPath, &taggedConfig{}), "config: name is required")
}

func TestLoadStrictOrdersTrimDefaultAndRequired(t *testing.T) {
	type taggedConfig struct {
		Name    string `mapstructure:"name" trim:"true" default:"  fallback  " required:"true"`
		Literal string `mapstructure:"literal" default:"fallback" required:"true"`
		Enabled bool   `mapstructure:"enabled" default:"true"`
	}

	path := writeTemp(t, "name: '   '\nliteral: '   '\nenabled: false\n")
	var config taggedConfig
	require.NoError(t, LoadStrict(path, &config))
	assert.Equal(t, "fallback", config.Name)
	assert.Equal(t, "   ", config.Literal)
	assert.False(t, config.Enabled)
}

func TestGenerateThenLoadStrict(t *testing.T) {
	data, err := GenerateYAML(&genSample{})
	require.NoError(t, err)
	p := writeTemp(t, string(data))

	var cfg genSample
	require.NoError(t, LoadStrict(p, &cfg), "a generated template must pass strict validation")
	assert.Equal(t, "app", cfg.Name)
}

func TestLoadStrictFlattensSquashedConfig(t *testing.T) {
	path := writeTemp(t, "name: strict\nmodule: audit\n")
	var cfg squashedConfigSample
	require.NoError(t, LoadStrict(path, &cfg))
	assert.Equal(t, "strict", cfg.Name)
	assert.Equal(t, 5*time.Second, cfg.Timeout)
	assert.Equal(t, "audit", cfg.Module)
}
